import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { ConversationTodoPort } from "../ports/conversation-todo.port.js";
import type { SubagentPort } from "../ports/subagent-port.js";
import type { AsyncToolRuntime } from "./async-tool-runtime.js";
import { definition, stringSchema } from "./gateway-utils.js";
import type { McpPluginRegistrationDeps } from "./mcp-plugin-tool-handlers.js";
import { MAX_ASK_OPTIONS } from "./gateway-types.js";

/**
 * Configuration for the always-available / conditionally-wired meta-tools.
 * Extracted from `McpAgentToolGateway.listTools` (#11) so the ~170-line block
 * of `definition(...)` calls lives in a focused module.
 */


/** Build a {@link MetaToolContext} from a loose record, dropping undefined keys. */
export function compact(obj: Record<string, unknown>): MetaToolContext {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) if (v !== undefined) out[k] = v;
  return out as unknown as MetaToolContext;
}

export interface MetaToolContext {
  readonly pluginRegistration: McpPluginRegistrationDeps | undefined;
  readonly todoPort: ConversationTodoPort | undefined;
  readonly asyncToolRuntime: AsyncToolRuntime | undefined;
  readonly jobStore: unknown | undefined;
  readonly jobScheduler: unknown | undefined;
  readonly pipelineStore: unknown | undefined;
  readonly pipelineScheduler: unknown | undefined;
  readonly subagentPort: SubagentPort | undefined;
  /** true only for interactive turns — gates the `ask_question` tool. */
  readonly interactive: boolean;
}


export function buildMetaToolDefinitions(ctx: MetaToolContext): AgentToolDefinition[] {
  return [
    definition("mcp_list", "List MCP plugins, runtime state, autostart preference, and launch spec (command, args, env keys — values redacted)"),
    definition("mcp_enable", "Start a selected MCP plugin. Optional args/env override the launch spec (command is immutable); a different launchSpec while running triggers a respawn.", {
      pluginId: stringSchema(),
      args: { type: "array", items: { type: "string" }, description: "Optional full replacement for the manifest args array." },
      env: { type: "object", additionalProperties: { type: "string" }, description: "Optional env overrides merged onto the manifest env (values are not echoed back)." },
    }),
    definition("mcp_disable", "Stop a selected MCP plugin", { pluginId: stringSchema() }),
    definition("tool_search", "Search a running MCP plugin's tools by name or description (case-insensitive token match — any term matches; returns {pluginId, query, matchMode, count, matches, hint?}; count 0 is success with a hint, not a turn interrupt or failure)", { pluginId: stringSchema(), query: stringSchema() }),
    definition("tool_list", "List tools from a running MCP plugin (names and descriptions only), or from ALL running plugins when pluginId is omitted (includes provider names and optional schemas)", { pluginId: { type: "string", description: "Optional plugin id; when omitted, lists tools across all running plugins" } }),
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
    ...(ctx.pluginRegistration ? [
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
    definition("memory", "Save, update, or remove a personal memory or user-profile entry, or read the current memory snapshot with action=list (read-only)", {
      action: { type: "string", enum: ["add", "replace", "remove", "list"], description: "Mutation action, or `list` for read-only snapshot" },
      target: { type: "string", enum: ["memory", "user"], description: "\"memory\" for personal notes, \"user\" for user-profile facts (not used by list)" },
      content: { type: "string", description: "New entry text (required for add and replace; omit or empty to delete via replace)" },
      old_text: { type: "string", description: "Unique substring of the existing entry to match (required for replace and remove)" },
    }, ["action", "target"]),
    ...(ctx.todoPort ? [definition("todo", "Replace the conversation task checklist (full replace, Claude TodoWrite style). Empty items clears the list. The user can delete items from the UI — treat deleted items as gone and do not re-add them.", {
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
    ...(ctx.asyncToolRuntime ? [
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
    ...(ctx.jobStore && ctx.jobScheduler ? [definition("job", "Manage scheduled automation jobs (run only while NusaShell is open; cron hours and bare timestamps use the host machine's local clock; explicit offsets identify an instant; missed one-shots are not silently fired)", {
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
    ...(ctx.pipelineStore && ctx.pipelineScheduler ? [definition("pipeline", "Manage multi-step DAG pipelines (event/schedule triggered, step dependencies, conditional branching, context passing). Runs only while NusaShell is open; schedule cron/bare timestamps use the host machine's local clock like jobs.", {
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
    ...(ctx.interactive ? [definition("ask_question", "Pause and ask the user a structured clarifying question before continuing. Use only for genuine decisions the user must make.", {
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
    ...(ctx.subagentPort ? [definition("subagent", "Delegate a task to a connected ACP coding agent. The subagent runs with its own tools and repo access; its live stream appears in the side pane. Returns a summary when done. The subagent has none of your MCP plugins, skills, or shell meta-tools — inline any needed skill content or MCP data into `prompt`. Provider, model, and mode are set in Settings → ACP Agents, not in the tool call. Pass `async: true` to run the subagent in the background (returns a handleId immediately; use async_wait/peek/kill to manage it).", {
      prompt: { type: "string", description: "The task brief for the subagent (required)" },
      title: { type: "string", description: "Optional label for the side pane and inline run card" },
      workspace: { type: "string", description: "Optional absolute cwd override; defaults to the conversation workspace, or the user home directory when unset. Never invent a path." },
      async: { type: "boolean", description: "If true, run the subagent in the background and return a handleId immediately (default false). Use async_wait/peek/kill to manage the background handle." },
    }, ["prompt"])] : []),
  ];
}
