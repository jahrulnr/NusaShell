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
import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { AgentToolGateway, AgentTurnContext } from "../ports/agent-tool-gateway.port.js";
import type { McpLiveSnapshot } from "./mcp-live-prompt-formatter.js";
import {
  tokenizeQuery,
  rankToolsByTokens,
  TOOL_SEARCH_MAX_MATCHES,
  TOOL_SEARCH_ZERO_HIT_HINT,
} from "./tool-discovery-match.js";
import type { ConversationTodoPort } from "../ports/conversation-todo.port.js";
import type { SubagentPort } from "../ports/subagent-port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AskQuestionService } from "./ask-question-service.js";
import { wrapToolArgs } from "./workspace-tool-wrap.js";
import {
  requireString, optionalString, stringRecord, parsePluginId,
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
import { emptySchema, type McpToolRoute, type SkillApprovalStagingPort } from "./gateway-types.js";
import { GatewayRouteStore } from "./gateway-route-store.js";
import { GatewayLiveSnapshot } from "./gateway-live-snapshot.js";
import { buildMetaToolDefinitions, compact } from "./gateway-meta-tools.js";

export type { WriteOrigin, SkillApprovalStagingPort, McpToolRoute } from "./gateway-types.js";

/**
 * Shell-owned progressive MCP catalog. The model starts with meta-tools, then
 * discovers servers and tools. Concrete MCP tools may be advertised via
 * `tool_schema`/`tool_schemas`, or lazily resolved when the model recalls a
 * previously used `mcp_<plugin>_<tool>` name and that plugin is already running.
 *
 * The class is a thin composition (#11) over focused modules:
 * - `GatewayRouteStore` — per-turn / per-conversation grant + lifecycle state
 * - `GatewayLiveSnapshot` — live MCP snapshot + running-plugin tool seeding
 * - `buildMetaToolDefinitions` — the meta-tool `definition(...)` catalog
 * The `execute()` dispatch + MCP plugin management / granted-call handlers stay
 * here because they are tightly coupled to `runtimeManager` and both stores.
 */
export class McpAgentToolGateway implements AgentToolGateway {
  private readonly routes: GatewayRouteStore;
  private readonly live: GatewayLiveSnapshot;
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
  ) {
    this.routes = new GatewayRouteStore();
    this.live = new GatewayLiveSnapshot(runtimeManager, this.routes, this.logger);
  }

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
    this.routes.beginTurn(turnId, context);
  }

  endConversation(conversationId: string): void {
    this.routes.endConversation(conversationId);
  }

  endTurn(turnId: string): void {
    this.askQuestions?.clearTurn(turnId);
    this.routes.endTurn(turnId);
  }

  async cancelTurn(turnId: string): Promise<void> {
    this.askQuestions?.rejectTurn(turnId);
    const calls = [...this.routes.activeCallsFor(turnId).entries()];
    await Promise.allSettled(calls.map(([requestId, pluginId]) =>
      this.runtimeManager.cancelTool(parsePluginId(pluginId), requestId),
    ));
  }

  async listTools(_pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]> {
    // Auto-seed routes for every tool on currently running plugins so the
    // model can call provider names directly without prior tool_schema.
    // Fail-soft: enumeration errors do not block the turn.
    await this.live.autoSeedRunningTools(turnId);
    const routes = this.routes.routesFor(turnId);
    return [
      ...buildMetaToolDefinitions(compact({
        pluginRegistration: this.pluginRegistration,
        todoPort: this.todoPort,
        asyncToolRuntime: this.asyncToolRuntime,
        jobStore: this.jobStore,
        jobScheduler: this.jobScheduler,
        pipelineStore: this.pipelineStore,
        pipelineScheduler: this.pipelineScheduler,
        subagentPort: this.subagentPort,
        interactive: this.isInteractive(turnId),
      })),
      ...this.live.cappedRouteDefinitions(routes),
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
        const result = await execTodo(this.todoPort, args, turnId, this.routes.conversationIdOf(turnId));
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
        const conversationId = this.routes.conversationIdOf(turnId);
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
        // Spawn the granted tool call as background work. The handle's abort
        // signal is combined with the turn's signal so either kill or turn
        // cancel aborts the in-flight MCP call. Progress notifications are
        // piped into the handle's tail buffer for streaming peek.
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
      case "skill_manage": return execSkillManage(this.skillRegistry, this.skillProvenance, this.skillUsage, this.approvalStaging, this.logger, this.routes.writeOriginOf(turnId), this.writeApprovalEnabled, args);
      case "job": {
        const providerId = this.routes.providerIdOf(turnId);
        const model = this.routes.modelOf(turnId);
        const effort = this.routes.effortOf(turnId);
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
          const conversationId = this.routes.conversationIdOf(turnId);
          if (!conversationId) {
            throw new ApplicationError("AGENT_INVALID_INPUT", "async subagent requires a conversation context");
          }
          const runtime = this.asyncToolRuntime;
          const workspace = this.routes.workspaceOf(turnId);
          const parentConversationId = this.routes.conversationIdOf(turnId);
          return execAsyncRun(runtime, args, {
            conversationId,
            ...(turnId ? { traceId: turnId } : {}),
            kind: "subagent",
            spawnWork: (_handleSignal, handleId) => execSubagent(this.subagentPort, args, turnId, workspace, this.logger, parentConversationId).then((result) => {
              // Store the subagent result in the handle's tail for peek.
              if (result && typeof result === "object" && "summary" in result) {
                runtime.appendTail(handleId, String((result as { summary?: unknown }).summary ?? ""));
              }
              return result;
            }),
          });
        }
        return execSubagent(
          this.subagentPort,
          args,
          turnId,
          this.routes.workspaceOf(turnId),
          this.logger,
          this.routes.conversationIdOf(turnId),
        );
      }
      default: return this.callGrantedTool(name, args, requestId, turnId, options?.signal);
    }
  }

  async getMcpLiveSnapshot(turnId: string): Promise<McpLiveSnapshot> {
    return this.live.getMcpLiveSnapshot(turnId);
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
      // Idempotent enable: if the plugin is already running, return its state
      // with an `alreadyRunning` trust signal so the model does not re-enable.
      try {
        const plugins = await this.runtimeManager.listPlugins();
        const existing = plugins.find((p) => p.pluginId === PluginId.toString(pluginId));
        if (existing?.state === "running") {
          return { pluginId: existing.pluginId, state: "running", alreadyRunning: true, liveState: await this.live.buildLiveStateLine(turnId) };
        }
      } catch (error) {
        this.logger?.warn(
          "MCP enable pre-check listPlugins failed: %s",
          error instanceof Error ? error.message : String(error),
        );
      }
      const workspace = this.routes.workspaceOf(turnId);
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
      return { pluginId: view.pluginId, state: view.state, liveState: await this.live.buildLiveStateLine(turnId) };
    }
    const view = await this.runtimeManager.stopPlugin(pluginId);
    return { pluginId: view.pluginId, state: view.state };
  }

  private async listAllTools(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    // pluginId omitted: list tools across ALL running plugins using the read-only
    // live snapshot (name + description + inputSchema, redacted).
    if (args.pluginId === undefined) {
      const snapshot = await this.live.getMcpLiveSnapshot("__hydration__");
      const tools = snapshot.tools.map((t) => ({
        name: t.providerName,
        pluginId: t.pluginId,
        toolName: t.toolName,
        description: t.description,
        inputSchema: t.inputSchema,
      }));
      return { pluginId: undefined, count: tools.length, tools, acrossAll: true, runningPlugins: snapshot.running.map((p) => p.pluginId) };
    }
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const tools = await this.runtimeManager.listTools(parsePluginId(pluginIdValue));
    const hits = tools.map((tool) => ({ name: tool.name, ...(tool.description ? { description: tool.description } : {}) }));
    return { pluginId: pluginIdValue, count: hits.length, tools: hits };
  }

  private async searchTools(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const query = requireString(args.query, "query");
    const tools = await this.runtimeManager.listTools(parsePluginId(pluginIdValue));
    const hits = tools.map((tool) => ({ name: tool.name, ...(tool.description ? { description: tool.description } : {}) }));
    const tokens = tokenizeQuery(query);
    const ranked = rankToolsByTokens(hits, tokens);
    const totalMatches = ranked.length;
    const matches = ranked.slice(0, TOOL_SEARCH_MAX_MATCHES).map(({ score: _score, ...rest }) => rest);
    const result: { pluginId: string; query: string; matchMode: "token_or"; count: number; matches: typeof matches; hint?: string } = {
      pluginId: pluginIdValue,
      query,
      matchMode: "token_or",
      count: totalMatches,
      matches,
    };
    if (totalMatches === 0) result.hint = TOOL_SEARCH_ZERO_HIT_HINT;
    return result;
  }

  private async grantTool(args: Readonly<Record<string, unknown>>, turnId: string): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const toolName = requireString(args.toolName, "toolName");
    const providerName = toProviderToolName(pluginIdValue, toolName);
    // Idempotent grant: if the route already exists this turn, return it with
    // an `alreadyGranted` trust signal so the model does not re-grant.
    const routes = this.routes.routesFor(turnId);
    const existing = routes.get(providerName);
    if (existing) {
      return { name: providerName, ...(existing.description ? { description: existing.description } : {}), inputSchema: existing.inputSchema, alreadyGranted: true, liveState: await this.live.buildLiveStateLine(turnId) };
    }
    const tool = (await this.runtimeManager.listTools(parsePluginId(pluginIdValue))).find((item) => item.name === toolName);
    if (!tool) throw new ApplicationError("TOOL_NOT_FOUND", `MCP tool not found: ${toolName}`);
    const inputSchema = tool.inputSchema ?? emptySchema;
    const route: McpToolRoute = { pluginId: pluginIdValue, toolName: tool.name, inputSchema, ...(tool.description ? { description: tool.description } : {}) };
    routes.set(providerName, route);
    this.routes.persistConversationRoute(turnId, providerName, route);
    return { name: providerName, ...(tool.description ? { description: tool.description } : {}), inputSchema, liveState: await this.live.buildLiveStateLine(turnId) };
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
    const routes = this.routes.routesFor(turnId);
    const granted: Array<{ name: string; description?: string; inputSchema: unknown; alreadyGranted?: boolean }> = [];
    const missing: string[] = [];
    for (const rawName of names) {
      const providerName = toProviderToolName(pluginIdValue, rawName);
      const existing = routes.get(providerName);
      if (existing) {
        granted.push({ name: providerName, ...(existing.description ? { description: existing.description } : {}), inputSchema: existing.inputSchema, alreadyGranted: true });
        continue;
      }
      const tool = byName.get(rawName);
      if (!tool) {
        missing.push(rawName);
        continue;
      }
      const inputSchema = tool.inputSchema ?? emptySchema;
      const route: McpToolRoute = {
        pluginId: pluginIdValue,
        toolName: tool.name,
        inputSchema,
        ...(tool.description ? { description: tool.description } : {}),
      };
      routes.set(providerName, route);
      this.routes.persistConversationRoute(turnId, providerName, route);
      granted.push({ name: providerName, ...(tool.description ? { description: tool.description } : {}), inputSchema });
    }
    return { granted, ...(missing.length ? { missing } : {}), liveState: await this.live.buildLiveStateLine(turnId) };
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
    let route = this.routes.routesFor(turnId).get(name);
    if (!route) {
      route = await this.resolveRunningToolRoute(name);
      if (route) {
        // Auto-grant for the rest of this turn so subsequent rounds advertise the schema.
        this.routes.routesFor(turnId).set(name, route);
        this.routes.persistConversationRoute(turnId, name, route);
      }
    }
    if (!route) {
      this.logger?.warn("Agent MCP tool rejected (not in allowlist) tool=%s turnId=%s", name, turnId);
      throw new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "AI provider requested a tool outside the MCP allowlist", { name });
    }
    const workspace = this.routes.workspaceOf(turnId);
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
    this.routes.registerActiveCall(turnId, requestId, route.pluginId);
    try {
      return await this.runtimeManager.callTool(
        parsePluginId(route.pluginId),
        { requestId, toolName: route.toolName, args: wrappedArgs, ...(onProgress ? { onProgress } : {}) },
        signal,
      );
    } finally {
      this.routes.unregisterActiveCall(turnId, requestId);
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

  private isInteractive(turnId: string): boolean {
    return this.askQuestions !== undefined && this.routes.isTurnInteractive(turnId);
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
