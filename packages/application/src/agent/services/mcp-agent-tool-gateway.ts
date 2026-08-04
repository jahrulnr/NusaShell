import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { PluginRuntimeManager } from "../../plugin/services/plugin-runtime-manager.js";
import type { SkillRegistryPort } from "../../skill/ports/skill-registry.port.js";
import type { SkillProvenancePort } from "../../skill/ports/skill-provenance.port.js";
import type { SkillUsagePort } from "../../skill/ports/skill-usage.port.js";
import type { MemoryStorePort } from "../../memory/ports/memory-store.port.js";
import type { JobStorePort } from "../../job/ports/job-store.port.js";
import type { PipelineStorePort } from "../../job/ports/pipeline-store.port.js";
import type { JobScheduler } from "../../job/services/job-scheduler.js";
import type { PipelineScheduler } from "../../job/services/pipeline-scheduler.js";
import type { DocsIndexPort } from "../ports/docs-index.port.js";
import type { AgentToolDefinition, ReasoningEffort } from "../ports/agent-provider.port.js";
import type { AgentToolGateway, AgentTurnContext } from "../ports/agent-tool-gateway.port.js";
import type { ConversationTodoPort } from "../ports/conversation-todo.port.js";
import type { SubagentPort } from "../ports/subagent-port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AskQuestionService } from "./ask-question-service.js";
import { wrapToolArgs } from "./workspace-tool-wrap.js";
import {
  definition, stringSchema, requireString, optionalString, stringRecord, parsePluginId,
  toProviderToolName,
} from "./gateway-utils.js";
import { execDocsSearch, execDocsList, execDocsRead } from "./docs-tool-handlers.js";
import { execSkillList, execSkillSearch, execSkillRead, execSkillManage } from "./skill-tool-handlers.js";
import { execMemory } from "./memory-tool-handler.js";
import { execJob } from "./job-tool-handler.js";
import { execPipeline } from "./pipeline-tool-handler.js";
import { execAskQuestion } from "./ask-question-tool-handler.js";
import { execSubagent } from "./subagent-tool-handler.js";
import { execTodo } from "./todo-tool-handler.js";
import { execAsyncRun, execAsyncWait, execAsyncPeek, execAsyncKill } from "./async-tool-handlers.js";
import type { AsyncToolRuntime } from "./async-tool-runtime.js";
import { execMcpRegister, execMcpUnregister, type McpPluginRegistrationDeps } from "./mcp-plugin-tool-handlers.js";

export type WriteOrigin = "foreground" | "background_review";

export interface SkillApprovalStagingPort {
  stage(skillId: string, action: "create" | "edit" | "write_file" | "delete", path: string, content: string): Promise<{ id: string }>;
}

interface McpToolRoute {
  readonly pluginId: string;
  readonly toolName: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
  readonly description?: string;
}

const emptySchema = { type: "object", properties: {} } as const;
const MAX_ASK_OPTIONS = 8;

/**
 * Shell-owned progressive MCP catalog. The model starts with meta-tools, then
 * discovers servers and tools. Concrete MCP tools may be advertised via
 * `tool_schema`/`tool_schemas`, or lazily resolved when the model recalls a
 * previously used `mcp_<plugin>_<tool>` name and that plugin is already running.
 */
export class McpAgentToolGateway implements AgentToolGateway {
  private readonly turnRoutes = new Map<string, Map<string, McpToolRoute>>();
  private readonly activeCalls = new Map<string, Map<string, string>>();
  private readonly turnInteractive = new Map<string, boolean>();
  private readonly turnWorkspace = new Map<string, string | undefined>();
  private readonly turnProviderId = new Map<string, string | undefined>();
  private readonly turnModel = new Map<string, string | undefined>();
  private readonly turnEffort = new Map<string, ReasoningEffort | undefined>();
  private readonly turnConversationId = new Map<string, string | undefined>();
  private writeOrigin: WriteOrigin = "foreground";
  private writeApprovalEnabled = false;
  private jobStore?: JobStorePort;
  private jobScheduler?: JobScheduler;
  private pipelineStore?: PipelineStorePort;
  private pipelineScheduler?: PipelineScheduler;
  private pluginRegistration?: McpPluginRegistrationDeps;
  private subagentPort?: SubagentPort;
  private todoPort?: ConversationTodoPort | undefined;
  private todoEventPublisher?: ((conversationId: string, items: readonly import("./agent-todo.js").AgentTodoItem[]) => void) | undefined;
  private asyncToolRuntime?: AsyncToolRuntime | undefined;

  constructor(
    private readonly runtimeManager: PluginRuntimeManager,
    private readonly docsIndex?: DocsIndexPort,
    private readonly skillRegistry?: SkillRegistryPort,
    private readonly logger?: LoggerPort,
    private readonly memoryStore?: MemoryStorePort,
    private readonly skillProvenance?: SkillProvenancePort,
    private readonly approvalStaging?: SkillApprovalStagingPort,
    private readonly skillUsage?: SkillUsagePort,
    private readonly askQuestions?: AskQuestionService,
  ) {}

  setWriteOrigin(origin: WriteOrigin): void { this.writeOrigin = origin; }
  getWriteOrigin(): WriteOrigin { return this.writeOrigin; }
  setWriteApprovalEnabled(enabled: boolean): void { this.writeApprovalEnabled = enabled; }

  /** Late-bind job deps after construction (agent is built before jobs in the container). */
  bindJobs(store: JobStorePort, scheduler: JobScheduler): void {
    this.jobStore = store;
    this.jobScheduler = scheduler;
  }

  /** Late-bind pipeline deps (same reason as bindJobs). */
  bindPipelines(store: PipelineStorePort, scheduler: PipelineScheduler): void {
    this.pipelineStore = store;
    this.pipelineScheduler = scheduler;
  }

  bindPluginRegistration(deps: McpPluginRegistrationDeps): void {
    this.pluginRegistration = deps;
  }

  /** Late-bind conversation todo port + event publisher. */
  bindTodos(
    port: ConversationTodoPort,
    publish?: ((conversationId: string, items: readonly import("./agent-todo.js").AgentTodoItem[]) => void) | undefined,
  ): void {
    this.todoPort = port;
    this.todoEventPublisher = publish;
  }

  /** Late-bind subagent port (ACP provider resolver + session runner). */
  bindSubagent(port: SubagentPort): void {
    this.subagentPort = port;
  }

  /** Late-bind the async tool runtime (background handle registry). */
  bindAsyncToolRuntime(runtime: AsyncToolRuntime): void {
    this.asyncToolRuntime = runtime;
  }

  beginTurn(turnId: string, context?: AgentTurnContext): void {
    if (!this.turnRoutes.has(turnId)) this.turnRoutes.set(turnId, new Map());
    if (!this.activeCalls.has(turnId)) this.activeCalls.set(turnId, new Map());
    // Merge only provided fields so a later beginTurn (e.g. AgentTurnRunner)
    // cannot wipe workspace / provider context set by RunAgentTurnHandler.
    if (context?.interactive !== undefined) this.turnInteractive.set(turnId, context.interactive);
    if (context?.workspace !== undefined) this.turnWorkspace.set(turnId, context.workspace);
    if (context?.providerId !== undefined) this.turnProviderId.set(turnId, context.providerId);
    if (context?.model !== undefined) this.turnModel.set(turnId, context.model);
    if (context?.effort !== undefined) this.turnEffort.set(turnId, context.effort);
    if (context?.conversationId !== undefined) this.turnConversationId.set(turnId, context.conversationId);
  }

  endTurn(turnId: string): void {
    this.askQuestions?.clearTurn(turnId);
    this.turnRoutes.delete(turnId);
    this.activeCalls.delete(turnId);
    this.turnInteractive.delete(turnId);
    this.turnWorkspace.delete(turnId);
    this.turnProviderId.delete(turnId);
    this.turnModel.delete(turnId);
    this.turnEffort.delete(turnId);
    this.turnConversationId.delete(turnId);
  }

  async cancelTurn(turnId: string): Promise<void> {
    this.askQuestions?.rejectTurn(turnId);
    const calls = [...(this.activeCalls.get(turnId)?.entries() ?? [])];
    await Promise.allSettled(calls.map(([requestId, pluginId]) =>
      this.runtimeManager.cancelTool(parsePluginId(pluginId), requestId),
    ));
  }

  async listTools(_pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]> {
    const routes = this.routesFor(turnId);
    return [
      definition("mcp_list", "List MCP plugins, runtime state, autostart preference, and launch spec (command, args, env keys — values redacted)"),
      definition("mcp_enable", "Start a selected MCP plugin. Optional args/env override the launch spec (command is immutable); a different launchSpec while running triggers a respawn.", {
        pluginId: stringSchema(),
        args: { type: "array", items: { type: "string" }, description: "Optional full replacement for the manifest args array." },
        env: { type: "object", additionalProperties: { type: "string" }, description: "Optional env overrides merged onto the manifest env (values are not echoed back)." },
      }),
      definition("mcp_disable", "Stop a selected MCP plugin", { pluginId: stringSchema() }),
      definition("tool_search", "Search a running MCP plugin's tools by name or description", { pluginId: stringSchema(), query: stringSchema() }),
      definition("tool_list", "List all tools from a running MCP plugin (names and descriptions only)", { pluginId: stringSchema() }),
      definition("tool_schema", "Load one MCP tool schema and advertise it for this turn (optional when recalling a known mcp_* name on a running plugin)", { pluginId: stringSchema(), toolName: stringSchema() }),
      definition("tool_schemas", "Load multiple MCP tool schemas and advertise them for this turn in one call (optional when recalling known mcp_* names on a running plugin)", {
        pluginId: stringSchema(),
        toolNames: { type: "array", items: { type: "string" }, minItems: 1, description: "Tool names to advertise for the current turn" },
      }, ["pluginId", "toolNames"]),
      definition("mcp_context", "Discover or load MCP prompts and text resources", {
        pluginId: stringSchema(),
        action: { type: "string", enum: ["list_prompts", "get_prompt", "search_resources", "list_resource_templates", "complete", "read_resource"] },
        query: stringSchema(),
        name: stringSchema(),
        uri: stringSchema(),
        refType: { type: "string", enum: ["prompt", "resource"] },
        argumentName: stringSchema(),
        argumentValue: stringSchema(),
        arguments: { type: "object", additionalProperties: { type: "string" } },
      }, ["pluginId", "action"]),
      ...(this.pluginRegistration ? [
        definition("mcp_register", "Register an existing valid MCP plugin folder under the writable user plugins root (interactive confirmation required)", {
          folder: { type: "string", description: "One folder name under the user plugins root" },
          path: { type: "string", description: "Absolute path to one folder under the user plugins root" },
        }, []),
        definition("mcp_unregister", "Unregister and remove a user-installed MCP plugin (interactive confirmation required)", {
          pluginId: stringSchema(),
        }),
      ] : []),
      definition("docs_search", "Search the internal NusaShell documentation corpus (how-to, feature, and UI guidance)", {
        query: stringSchema(),
        top_k: { type: "integer", minimum: 1, maximum: 10, description: "Maximum number of chunks to return" },
      }, ["query"]),
      definition("docs_list", "List all documents in the internal NusaShell docs corpus", {
        limit: { type: "integer", minimum: 1, maximum: 100, description: "Maximum number of documents" },
      }),
      definition("docs_read", "Read the full content of one internal NusaShell documentation document by path", {
        path: stringSchema(),
        chunk_id: { type: "string", description: "Chunk ID from a docs_search hit" },
        max_chars: { type: "integer", minimum: 0, maximum: 20000, description: "Maximum characters to return; 0 means no limit" },
        offset: { type: "integer", minimum: 0, description: "Character offset for pagination" },
      }, ["path"]),
      definition("skill_list", "List installed agent skills and their descriptions", {
        limit: { type: "integer", minimum: 1, maximum: 100, description: "Maximum number of skills" },
      }),
      definition("skill_search", "Search installed agent skills by name or description", {
        query: stringSchema(),
        limit: { type: "integer", minimum: 1, maximum: 50, description: "Maximum number of skills" },
      }, ["query"]),
      definition("skill_read", "Read SKILL.md or another text file inside an installed agent skill", {
        skill_id: { type: "string", description: "Installed skill ID from skill_list or skill_search" },
        path: { type: "string", description: "Relative file path; defaults to SKILL.md" },
        offset: { type: "integer", minimum: 0, description: "Character offset for pagination" },
        max_chars: { type: "integer", minimum: 1, maximum: 100000, description: "Maximum characters to return" },
      }, ["skill_id"]),
      definition("memory", "Save, update, or remove a personal memory or user-profile entry", {
        action: { type: "string", enum: ["add", "replace", "remove"], description: "Mutation action" },
        target: { type: "string", enum: ["memory", "user"], description: "\"memory\" for personal notes, \"user\" for user-profile facts" },
        content: { type: "string", description: "New entry text (required for add and replace; omit or empty to delete via replace)" },
        old_text: { type: "string", description: "Unique substring of the existing entry to match (required for replace and remove)" },
      }, ["action", "target"]),
      ...(this.todoPort ? [definition("todo", "Replace the conversation task checklist (full replace, Claude TodoWrite style). Empty items clears the list. The user can delete items from the UI — treat deleted items as gone and do not re-add them.", {
        items: {
          type: "array",
          description: "Full replacement list of todo items (max 50)",
          items: {
            type: "object",
            properties: {
              id: { type: "string", description: "Stable item id (unique within the list)" },
              content: { type: "string", description: "Short task description (max 500 chars)" },
              status: { type: "string", enum: ["pending", "in_progress", "completed"], description: "Item status; prefer exactly one in_progress at a time" },
            },
            required: ["id", "content", "status"],
          },
        },
      }, ["items"])] : []),
      ...(this.asyncToolRuntime ? [
        definition("async_run", "Start a granted MCP tool in the background and return immediately with a handleId. The handle survives turn end. Use for long-running commands (docker logs -f, builds, servers). The tool name must be in the current allowlist.", {
          tool: { type: "string", description: "Granted MCP tool name to run in background" },
          args: { type: "object", additionalProperties: true, description: "Arguments to pass to the tool" },
          maxRuntimeMs: { type: "integer", minimum: 1000, description: "Optional max runtime before auto-fail (ms)" },
        }, ["tool"]),
        definition("async_wait", "Block this tool-call until the handle settles or timeoutMs elapses (1s–5min). Returns the final status if settled, or running if still in-flight. Prefer this over busy-loop peeking.", {
          handleId: { type: "string", description: "Handle id from async_run" },
          timeoutMs: { type: "integer", minimum: 1000, maximum: 300000, description: "Max time to wait in milliseconds (1s–5min)" },
        }, ["handleId", "timeoutMs"]),
        definition("async_peek", "Non-blocking read of a handle's buffered output and current status (running/ok/fail/killed). Does not mark the handle done.", {
          handleId: { type: "string", description: "Handle id from async_run" },
        }, ["handleId"]),
        definition("async_kill", "Soft-cancel a running handle. Returns the final status. Use when the user asks to stop a background job.", {
          handleId: { type: "string", description: "Handle id from async_run" },
        }, ["handleId"]),
      ] : []),
      definition("skill_manage", "Create, edit, write a support file in, or delete an agent-owned skill", {
        action: { type: "string", enum: ["create", "edit", "write_file", "delete"], description: "Mutation action" },
        name: { type: "string", description: "Skill ID (lowercase slug); must match the frontmatter name in SKILL.md" },
        content: { type: "string", description: "Full SKILL.md content (for create/edit) or file content (for write_file)" },
        path: { type: "string", description: "Relative file path under the skill (for write_file only); must be under references/, templates/, scripts/, or assets/" },
      }, ["action", "name"]),
      ...(this.jobStore && this.jobScheduler ? [definition("job", "Manage scheduled automation jobs (run only while NusaShell is open; cron hours and bare timestamps are UTC — convert from the user's local timezone before scheduling; missed one-shots are not silently fired)", {
        action: { type: "string", enum: ["list", "validate_schedule", "add", "update", "set_enabled", "run", "cancel", "remove", "output"], description: "Job operation" },
        id: { type: "string", description: "Job ID (required for update, set_enabled, run, cancel, remove, output)" },
        name: { type: "string", description: "Job name (required for add)" },
        trigger: { type: "object", description: "Trigger object: { kind: 'schedule', schedule: '...' } or { kind: 'event', pattern: '...', pluginId?: '...', conditions?: [...], throttleMs?: N, maxFiresPerHour?: N }" },
        schedule: { type: "string", description: "Schedule expression (legacy shorthand for trigger.kind=schedule): \"every 30m\", \"2h\", \"0 9 * * *\", or an ISO timestamp" },
        mode: { type: "string", enum: ["agent", "tool"], description: "Job mode (required for add)" },
        prompt: { type: "string", description: "Agent prompt (required when mode=agent)" },
        pluginId: { type: "string", description: "Plugin ID (required when mode=tool)" },
        toolName: { type: "string", description: "Tool name (required when mode=tool)" },
        args: { type: "object", additionalProperties: true, description: "Static tool args (when mode=tool)" },
        enabled: { type: "boolean", description: "Pause/resume flag (required for set_enabled)" },
        repeat_times: { type: "integer", minimum: 1, maximum: 100000, description: "Optional finite repeat count (add only); omit for repeat forever" },
        on_complete: { type: "object", description: "Emit an automation event on successful completion (soft chain): { type: '...', payload?: {...} }. Set null to clear." },
        limit: { type: "integer", minimum: 1, maximum: 100, description: "Max output entries (output only; default 20)" },
      }, ["action"])] : []),
      ...(this.pipelineStore && this.pipelineScheduler ? [definition("pipeline", "Manage multi-step DAG pipelines (event/schedule triggered, step dependencies, conditional branching, context passing). Runs only while NusaShell is open; schedule cron/bare timestamps are UTC like jobs.", {
        action: { type: "string", enum: ["list", "add", "update", "remove", "run"], description: "Pipeline operation" },
        id: { type: "string", description: "Pipeline ID (required for update, remove, run)" },
        name: { type: "string", description: "Pipeline name (required for add)" },
        description: { type: "string", description: "Optional pipeline description" },
        trigger: { type: "object", description: "Trigger object: { kind: 'schedule', schedule: '...' } or { kind: 'event', pattern: '...', pluginId?: '...' }" },
        enabled: { type: "boolean", description: "Enable/disable pipeline (update only)" },
        steps: { type: "array", description: "Pipeline steps in topological order", items: { type: "object", properties: {
          id: { type: "string", description: "Unique step ID within pipeline" },
          name: { type: "string", description: "Step display name" },
          action: { type: "object", description: "Step action: { type: 'agent', prompt: '...' } or { type: 'tool', pluginId: '...', toolName: '...', args: {...} }" },
          dependsOn: { type: "array", items: { type: "string" }, description: "Step IDs that must complete before this step" },
          outputKey: { type: "string", description: "Store step output in context under this key for downstream steps" },
          condition: { type: "object", description: "Skip step if condition is false: { path: 'payload.x', op: 'eq', value: '...' } or { or: [...] } / { not: ... }" },
        } } },
      }, ["action"])] : []),
      ...(this.isInteractive(turnId) ? [definition("ask_question", "Pause and ask the user a structured clarifying question before continuing. Use only for genuine decisions the user must make.", {
        question: { type: "string", description: "The question to show the user" },
        options: {
          type: "array",
          minItems: 1,
          maxItems: MAX_ASK_OPTIONS,
          description: "Selectable choices (1-8). Mark one default when possible.",
          items: {
            type: "object",
            properties: {
              id: { type: "string", description: "Stable option id" },
              label: { type: "string", description: "Short option label" },
              description: { type: "string", description: "Optional one-line explanation" },
              default: { type: "boolean", description: "Whether this option is the recommended default" },
              icon: { type: "string", description: "Optional emoji or short icon glyph" },
              image: { type: "string", description: "Optional image URL or compact data URI" },
            },
            required: ["id", "label"],
            additionalProperties: false,
          },
        },
        allow_free_text: { type: "boolean", description: "Whether the user may type a custom answer (default true)" },
        multi_select: { type: "boolean", description: "Whether the user may select multiple options (default false)" },
      }, ["question", "options"])] : []),
      ...(this.subagentPort ? [definition("subagent", "Delegate a task to a connected ACP coding agent. The subagent runs with its own tools and repo access; its live stream appears in the side pane. Returns a summary when done. Provider, model, and mode are set in Settings → ACP Agents, not in the tool call. Pass `async: true` to run the subagent in the background (returns a handleId immediately; use async_wait/peek/kill to manage it).", {
        prompt: { type: "string", description: "The task brief for the subagent (required)" },
        title: { type: "string", description: "Optional label for the side pane and inline run card" },
        workspace: { type: "string", description: "Optional absolute cwd override; defaults to the conversation workspace, or the user home directory when unset. Never invent a path." },
        async: { type: "boolean", description: "If true, run the subagent in the background and return a handleId immediately (default false). Use async_wait/peek/kill to manage the background handle." },
      }, ["prompt"])] : []),
      ...[...routes.entries()].map(([name, route]) => ({
        name,
        ...(route.description ? { description: route.description } : {}),
        inputSchema: route.inputSchema,
      })),
    ];
  }

  async execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
    turnId: string,
    callId?: string,
    options?: { readonly signal?: AbortSignal },
  ): Promise<unknown> {
    switch (name) {
      case "mcp_list": return this.listMcpPlugins();
      case "mcp_enable": return this.changeMcpState(args, true, turnId);
      case "mcp_disable": return this.changeMcpState(args, false, turnId);
      case "tool_list": return this.listAllTools(args);
      case "tool_search": return this.searchTools(args);
      case "tool_schema": return this.grantTool(args, turnId);
      case "tool_schemas": return this.grantTools(args, turnId);
      case "mcp_context": return this.context(args);
      case "mcp_register": return execMcpRegister(this.pluginRegistration!, args, turnId, callId ?? requestId, this.isInteractive(turnId));
      case "mcp_unregister": return execMcpUnregister(this.pluginRegistration!, args, turnId, callId ?? requestId, this.isInteractive(turnId));
      case "docs_search": return execDocsSearch(this.docsIndex, args);
      case "docs_list": return execDocsList(this.docsIndex, args);
      case "docs_read": return execDocsRead(this.docsIndex, args);
      case "skill_list": return execSkillList(this.skillRegistry, args);
      case "skill_search": return execSkillSearch(this.skillRegistry, args);
      case "skill_read": return execSkillRead(this.skillRegistry, this.skillUsage, this.logger, args);
      case "memory": return execMemory(this.memoryStore, args);
      case "todo": {
        const result = await execTodo(this.todoPort, args, turnId, this.turnConversationId.get(turnId));
        if (result && typeof result === "object" && "ok" in result && result.ok && "conversationId" in result && "items" in result) {
          const r = result as unknown as { conversationId: string; items: readonly import("./agent-todo.js").AgentTodoItem[] };
          this.todoEventPublisher?.(r.conversationId, r.items);
        }
        return result;
      }
      case "async_run": {
        if (!this.asyncToolRuntime) {
          return { ok: false, error: { code: "async_not_configured", message: "Async tool runtime is not available." } };
        }
        const conversationId = this.turnConversationId.get(turnId);
        if (!conversationId) {
          throw new ApplicationError("AGENT_INVALID_INPUT", "async_run requires a conversation context");
        }
        const toolName = typeof args.tool === "string" ? args.tool.trim() : "";
        if (!toolName) {
          throw new ApplicationError("AGENT_INVALID_INPUT", "async_run requires a non-empty 'tool' name");
        }
        const toolArgs = (args.args && typeof args.args === "object" ? args.args : {}) as Readonly<Record<string, unknown>>;
        const turnSignal = options?.signal;
        const runtime = this.asyncToolRuntime;
        // Spawn the granted tool call as background work.
        // The handle's abort signal is combined with the turn's signal so
        // either kill or turn cancel aborts the in-flight MCP call.
        // Progress notifications from the MCP server are piped into the
        // handle's tail buffer for streaming peek.
        return execAsyncRun(this.asyncToolRuntime, args, {
          conversationId,
          ...(turnId ? { traceId: turnId } : {}),
          kind: "mcp",
          spawnWork: (handleSignal: AbortSignal, handleId: string) => {
            const combined = combineSignals(handleSignal, turnSignal);
            return this.callGrantedTool(toolName, toolArgs, requestId, turnId, combined, (progress) => {
              if (progress.message) {
                runtime.appendTail(handleId, progress.message);
              }
            });
          },
        });
      }
      case "async_wait": {
        if (!this.asyncToolRuntime) {
          return { ok: false, error: { code: "async_not_configured", message: "Async tool runtime is not available." } };
        }
        return execAsyncWait(this.asyncToolRuntime, args, options?.signal);
      }
      case "async_peek": {
        if (!this.asyncToolRuntime) {
          return { ok: false, error: { code: "async_not_configured", message: "Async tool runtime is not available." } };
        }
        return execAsyncPeek(this.asyncToolRuntime, args);
      }
      case "async_kill": {
        if (!this.asyncToolRuntime) {
          return { ok: false, error: { code: "async_not_configured", message: "Async tool runtime is not available." } };
        }
        return execAsyncKill(this.asyncToolRuntime, args);
      }
      case "skill_manage": return execSkillManage(this.skillRegistry, this.skillProvenance, this.skillUsage, this.approvalStaging, this.logger, this.writeOrigin, this.writeApprovalEnabled, args);
      case "job": {
        const providerId = this.turnProviderId.get(turnId);
        const model = this.turnModel.get(turnId);
        const effort = this.turnEffort.get(turnId);
        return execJob(this.jobStore, this.jobScheduler, args, {
          ...(providerId !== undefined ? { providerId } : {}),
          ...(model !== undefined ? { model } : {}),
          ...(effort !== undefined ? { effort } : {}),
        });
      }
      case "pipeline": return execPipeline(this.pipelineStore, this.pipelineScheduler, args);
      case "ask_question": return execAskQuestion(this.askQuestions, this.isInteractive(turnId), args, callId ?? requestId, turnId);
      case "subagent": {
        const isAsync = args.async === true;
        if (isAsync && this.asyncToolRuntime) {
          const conversationId = this.turnConversationId.get(turnId);
          if (!conversationId) {
            throw new ApplicationError("AGENT_INVALID_INPUT", "async subagent requires a conversation context");
          }
          const runtime = this.asyncToolRuntime;
          const workspace = this.turnWorkspace.get(turnId);
          return execAsyncRun(runtime, args, {
            conversationId,
            ...(turnId ? { traceId: turnId } : {}),
            kind: "subagent",
            spawnWork: (_handleSignal, handleId) => execSubagent(this.subagentPort, args, turnId, workspace, this.logger).then((result) => {
              // Store the subagent result in the handle's tail for peek.
              if (result && typeof result === "object" && "summary" in result) {
                runtime.appendTail(handleId, String((result as { summary?: unknown }).summary ?? ""));
              }
              return result;
            }),
          });
        }
        return execSubagent(this.subagentPort, args, turnId, this.turnWorkspace.get(turnId), this.logger);
      }
      default: return this.callGrantedTool(name, args, requestId, turnId, options?.signal);
    }
  }

  // --- MCP plugin management handlers (tightly coupled to gateway state) ---

  private async listMcpPlugins(): Promise<unknown> {
    const plugins = await this.runtimeManager.listPlugins();
    const enriched = await Promise.all(plugins.map(async (plugin) => {
      try {
        const spec = await this.runtimeManager.getLaunchSpec?.(parsePluginId(plugin.pluginId));
        return { ...plugin, ...(spec ? { launchSpec: spec } : {}) };
      } catch {
        return plugin;
      }
    }));
    return enriched;
  }

  private async changeMcpState(args: Readonly<Record<string, unknown>>, start: boolean, turnId: string): Promise<unknown> {
    const pluginId = parsePluginId(args.pluginId);
    this.logger?.info("Agent MCP plugin %s via agent tool plugin=%s", start ? "start" : "stop", PluginId.toString(pluginId));
    if (start) {
      const workspace = this.turnWorkspace.get(turnId);
      const overrides: { args?: readonly string[]; env?: Readonly<Record<string, string>>; workspace?: string } = {};
      // Ignore empty args arrays — they would wipe the manifest script path and
      // hang `node` on stdin eval of the MCP handshake (Bug C).
      if (
        Array.isArray(args.args)
        && args.args.length > 0
        && args.args.every((v) => typeof v === "string")
      ) {
        overrides.args = args.args as string[];
      }
      if (args.env && typeof args.env === "object" && !Array.isArray(args.env)) {
        const envEntries = Object.entries(args.env as Record<string, unknown>).filter(
          ([, v]) => typeof v === "string",
        ) as Array<[string, string]>;
        if (envEntries.length > 0) {
          overrides.env = Object.fromEntries(envEntries);
        }
      }
      if (workspace) overrides.workspace = workspace;
      const view = await this.runtimeManager.startPlugin(pluginId, Object.keys(overrides).length > 0 ? overrides : undefined);
      return { pluginId: view.pluginId, state: view.state };
    }
    const view = await this.runtimeManager.stopPlugin(pluginId);
    return { pluginId: view.pluginId, state: view.state };
  }

  private async listAllTools(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const tools = await this.runtimeManager.listTools(parsePluginId(pluginIdValue));
    return tools.map((tool) => ({ name: tool.name, ...(tool.description ? { description: tool.description } : {}) }));
  }

  private async searchTools(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const query = requireString(args.query, "query").toLowerCase();
    const tools = await this.runtimeManager.listTools(parsePluginId(pluginIdValue));
    return tools
      .filter((tool) => `${tool.name} ${tool.description ?? ""}`.toLowerCase().includes(query))
      .slice(0, 20)
      .map((tool) => ({ name: tool.name, ...(tool.description ? { description: tool.description } : {}) }));
  }

  private async grantTool(args: Readonly<Record<string, unknown>>, turnId: string): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const toolName = requireString(args.toolName, "toolName");
    const tool = (await this.runtimeManager.listTools(parsePluginId(pluginIdValue))).find((item) => item.name === toolName);
    if (!tool) throw new ApplicationError("TOOL_NOT_FOUND", `MCP tool not found: ${toolName}`);
    const providerName = toProviderToolName(pluginIdValue, tool.name);
    const inputSchema = tool.inputSchema ?? emptySchema;
    this.routesFor(turnId).set(providerName, { pluginId: pluginIdValue, toolName: tool.name, inputSchema, ...(tool.description ? { description: tool.description } : {}) });
    return { name: providerName, ...(tool.description ? { description: tool.description } : {}), inputSchema };
  }

  private async grantTools(args: Readonly<Record<string, unknown>>, turnId: string): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const names = Array.isArray(args.toolNames)
      ? args.toolNames.filter((n): n is string => typeof n === "string" && n.trim().length > 0)
      : [];
    if (names.length === 0) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "tool_schemas requires a non-empty toolNames array");
    }
    const tools = await this.runtimeManager.listTools(parsePluginId(pluginIdValue));
    const byName = new Map(tools.map((tool) => [tool.name, tool]));
    const granted: Array<{ name: string; description?: string; inputSchema: unknown }> = [];
    const missing: string[] = [];
    for (const rawName of names) {
      const tool = byName.get(rawName);
      if (!tool) {
        missing.push(rawName);
        continue;
      }
      const providerName = toProviderToolName(pluginIdValue, tool.name);
      const inputSchema = tool.inputSchema ?? emptySchema;
      this.routesFor(turnId).set(providerName, {
        pluginId: pluginIdValue,
        toolName: tool.name,
        inputSchema,
        ...(tool.description ? { description: tool.description } : {}),
      });
      granted.push({ name: providerName, ...(tool.description ? { description: tool.description } : {}), inputSchema });
    }
    return { granted, ...(missing.length ? { missing } : {}) };
  }

  private async context(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const pluginId = parsePluginId(pluginIdValue);
    const action = requireString(args.action, "action");
    const query = optionalString(args.query).toLowerCase();
    switch (action) {
      case "list_prompts":
        return (await this.runtimeManager.listPrompts(pluginId))
          .filter((prompt) => !query || `${prompt.name} ${prompt.description ?? ""}`.toLowerCase().includes(query))
          .slice(0, 20);
      case "get_prompt":
        return this.runtimeManager.getPrompt(
          pluginId,
          requireString(args.name, "name"),
          stringRecord(args.arguments),
        );
      case "search_resources":
        return (await this.runtimeManager.listResources(pluginId))
          .filter((resource) => !query || `${resource.name} ${resource.uri} ${resource.description ?? ""}`.toLowerCase().includes(query))
          .slice(0, 20);
      case "list_resource_templates":
        return (await this.runtimeManager.listResourceTemplates(pluginId))
          .filter((template) =>
            !query
            || `${template.name} ${template.uriTemplate} ${template.description ?? ""}`.toLowerCase().includes(query))
          .slice(0, 20);
      case "complete":
        return this.completeContext(pluginId, args);
      case "read_resource":
        return this.readResource(args);
      default:
        throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported MCP context action: ${action}`);
    }
  }

  private async completeContext(pluginId: PluginId, args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const refType = requireString(args.refType, "refType");
    const reference = refType === "prompt"
      ? { type: "ref/prompt" as const, name: requireString(args.name, "name") }
      : refType === "resource"
        ? { type: "ref/resource" as const, uri: requireString(args.uri, "uri") }
        : null;
    if (!reference) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported completion reference: ${refType}`);
    }
    const result = await this.runtimeManager.complete(
      pluginId,
      reference,
      {
        name: requireString(args.argumentName, "argumentName"),
        value: optionalString(args.argumentValue),
      },
      { arguments: stringRecord(args.arguments) },
    );
    return { ...result, values: result.values.slice(0, 100) };
  }

  private async readResource(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const resource = await this.runtimeManager.readResource(
      parsePluginId(args.pluginId),
      requireString(args.uri, "uri"),
    );
    let remaining = 50_000;
    return {
      contents: resource.contents.flatMap((content) => {
        if (typeof content.text !== "string" || remaining <= 0) return [];
        const text = content.text.slice(0, remaining);
        remaining -= text.length;
        return [{
          uri: content.uri,
          ...(content.mimeType !== undefined ? { mimeType: content.mimeType } : {}),
          text,
          ...(text.length < content.text.length ? { truncated: true } : {}),
        }];
      }),
    };
  }

  private async callGrantedTool(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
    turnId: string,
    signal?: AbortSignal,
    onProgress?: (progress: { progress: number; total?: number | undefined; message?: string | undefined }) => void,
  ): Promise<unknown> {
    let route = this.routesFor(turnId).get(name);
    if (!route) {
      route = await this.resolveRunningToolRoute(name);
      if (route) {
        // Auto-grant for the rest of this turn so subsequent rounds advertise the schema.
        this.routesFor(turnId).set(name, route);
      }
    }
    if (!route) {
      this.logger?.warn("Agent MCP tool rejected (not in allowlist) tool=%s turnId=%s", name, turnId);
      throw new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "AI provider requested a tool outside the MCP allowlist", { name });
    }
    const workspace = this.turnWorkspace.get(turnId);
    const wrappedArgs = wrapToolArgs(route.pluginId, route.toolName, args, workspace);
    if (workspace) {
      try {
        await this.runtimeManager.syncWorkspace?.(parsePluginId(route.pluginId), workspace);
      } catch (error) {
        const msg = error instanceof Error ? error.message : String(error);
        this.logger?.warn("Workspace sync failed for plugin %s: %s", route.pluginId, msg);
        // C4: fail-fast with a clear error rather than silently running the
        // tool against a stale workspace (wrong file context).
        throw new ApplicationError("WORKSPACE_SYNC_FAILED", `Workspace sync failed for plugin ${route.pluginId}: ${msg}`, {
          pluginId: route.pluginId,
          workspace,
          cause: msg,
        });
      }
    }
    const calls = this.activeCalls.get(turnId);
    calls?.set(requestId, route.pluginId);
    try {
      return await this.runtimeManager.callTool(
        parsePluginId(route.pluginId),
        { requestId, toolName: route.toolName, args: wrappedArgs, ...(onProgress ? { onProgress } : {}) },
        signal,
      );
    } finally {
      calls?.delete(requestId);
    }
  }

  /**
   * Resolve an ungranted `mcp_<plugin>_<tool>` name against currently running
   * plugins. Used when the model recalls a tool from a prior turn without
   * re-running `tool_schema`. Idle/stopped plugins are never matched.
   */
  private async resolveRunningToolRoute(name: string): Promise<McpToolRoute | undefined> {
    const plugins = await this.runtimeManager.listPlugins();
    for (const plugin of plugins) {
      if (plugin.state !== "running") continue;
      let tools: Awaited<ReturnType<PluginRuntimeManager["listTools"]>>;
      try {
        tools = await this.runtimeManager.listTools(parsePluginId(plugin.pluginId));
      } catch (error) {
        const msg = error instanceof Error ? error.message : String(error);
        this.logger?.warn("Lazy MCP resolve skipped plugin=%s: %s", plugin.pluginId, msg);
        continue;
      }
      for (const tool of tools) {
        if (toProviderToolName(plugin.pluginId, tool.name) !== name) continue;
        const inputSchema = tool.inputSchema ?? emptySchema;
        return {
          pluginId: plugin.pluginId,
          toolName: tool.name,
          inputSchema,
          ...(tool.description ? { description: tool.description } : {}),
        };
      }
    }
    return undefined;
  }

  private routesFor(turnId: string): Map<string, McpToolRoute> {
    let routes = this.turnRoutes.get(turnId);
    if (!routes) {
      routes = new Map();
      this.turnRoutes.set(turnId, routes);
    }
    return routes;
  }

  private isInteractive(turnId: string): boolean {
    return this.askQuestions !== undefined && this.turnInteractive.get(turnId) === true;
  }
}

/**
 * Combine multiple abort signals into one. If any signal aborts, the combined
 * signal aborts. Uses `AbortSignal.any` when available (Node 20+), otherwise
 * falls back to a manual controller.
 */
function combineSignals(...signals: (AbortSignal | undefined)[]): AbortSignal | undefined {
  const valid = signals.filter((s): s is AbortSignal => s !== undefined);
  if (valid.length === 0) return undefined;
  if (valid.length === 1) return valid[0];
  // If any is already aborted, return an already-aborted signal.
  const alreadyAborted = valid.find((s) => s.aborted);
  if (alreadyAborted) return alreadyAborted;
  // Use AbortSignal.any if available (Node 20+).
  if (typeof (AbortSignal as unknown as { any?: unknown }).any === "function") {
    return (AbortSignal as unknown as { any: (signals: AbortSignal[]) => AbortSignal }).any(valid);
  }
  // Fallback: manual controller.
  const controller = new AbortController();
  for (const signal of valid) {
    signal.addEventListener("abort", () => controller.abort(), { once: true });
  }
  return controller.signal;
}
