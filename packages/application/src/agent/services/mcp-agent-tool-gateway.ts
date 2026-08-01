import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { PluginRuntimeManager } from "../../plugin/services/plugin-runtime-manager.js";
import type { SkillRegistryPort } from "../../skill/ports/skill-registry.port.js";
import type { SkillProvenancePort } from "../../skill/ports/skill-provenance.port.js";
import type { SkillUsagePort, UsageBumpKind } from "../../skill/ports/skill-usage.port.js";
import type { MemoryStorePort, MemoryTarget } from "../../memory/ports/memory-store.port.js";
import type { DocsIndexPort } from "../ports/docs-index.port.js";
import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { AgentToolGateway, AgentTurnContext } from "../ports/agent-tool-gateway.port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AskQuestionOption, AskQuestionService } from "./ask-question-service.js";
import { wrapToolArgs } from "./workspace-tool-wrap.js";

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
 * Shell-owned progressive MCP catalog. The model first discovers servers and
 * tools, then grants one typed MCP tool for the following provider round.
 */
export class McpAgentToolGateway implements AgentToolGateway {
  private readonly turnRoutes = new Map<string, Map<string, McpToolRoute>>();
  private readonly activeCalls = new Map<string, Map<string, string>>();
  private readonly turnInteractive = new Map<string, boolean>();
  private readonly turnWorkspace = new Map<string, string | undefined>();
  private writeOrigin: WriteOrigin = "foreground";
  private writeApprovalEnabled = false;

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

  setWriteOrigin(origin: WriteOrigin): void {
    this.writeOrigin = origin;
  }

  getWriteOrigin(): WriteOrigin {
    return this.writeOrigin;
  }

  setWriteApprovalEnabled(enabled: boolean): void {
    this.writeApprovalEnabled = enabled;
  }

  beginTurn(turnId: string, context?: AgentTurnContext): void {
    if (!this.turnRoutes.has(turnId)) this.turnRoutes.set(turnId, new Map());
    if (!this.activeCalls.has(turnId)) this.activeCalls.set(turnId, new Map());
    if (context?.interactive !== undefined) this.turnInteractive.set(turnId, context.interactive);
    this.turnWorkspace.set(turnId, context?.workspace);
  }

  endTurn(turnId: string): void {
    this.askQuestions?.clearTurn(turnId);
    this.turnRoutes.delete(turnId);
    this.activeCalls.delete(turnId);
    this.turnInteractive.delete(turnId);
    this.turnWorkspace.delete(turnId);
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
      definition("tool_schema", "Load one searched MCP tool schema for the next round", { pluginId: stringSchema(), toolName: stringSchema() }),
      definition("tool_schemas", "Load multiple MCP tool schemas for the next round in one call", {
        pluginId: stringSchema(),
        toolNames: { type: "array", items: { type: "string" }, minItems: 1, description: "Tool names to grant for the current turn" },
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
      definition("skill_manage", "Create, edit, write a support file in, or delete an agent-owned skill", {
        action: { type: "string", enum: ["create", "edit", "write_file", "delete"], description: "Mutation action" },
        name: { type: "string", description: "Skill ID (lowercase slug); must match the frontmatter name in SKILL.md" },
        content: { type: "string", description: "Full SKILL.md content (for create/edit) or file content (for write_file)" },
        path: { type: "string", description: "Relative file path under the skill (for write_file only); must be under references/, templates/, scripts/, or assets/" },
      }, ["action", "name"]),
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
      ...[...routes.entries()].map(([name, route]) => ({
        name,
        ...(route.description ? { description: route.description } : {}),
        inputSchema: route.inputSchema,
      })),
    ];
  }

  async execute(name: string, args: Readonly<Record<string, unknown>>, requestId: string, turnId: string, callId?: string): Promise<unknown> {
    switch (name) {
      case "mcp_list": return this.listMcpPlugins();
      case "mcp_enable": return this.changeMcpState(args, true, turnId);
      case "mcp_disable": return this.changeMcpState(args, false, turnId);
      case "tool_list": return this.listAllTools(args);
      case "tool_search": return this.searchTools(args);
      case "tool_schema": return this.grantTool(args, turnId);
      case "tool_schemas": return this.grantTools(args, turnId);
      case "mcp_context": return this.context(args);
      case "docs_search": return this.execDocsSearch(args);
      case "docs_list": return this.execDocsList(args);
      case "docs_read": return this.execDocsRead(args);
      case "skill_list": return this.execSkillList(args);
      case "skill_search": return this.execSkillSearch(args);
      case "skill_read": return this.execSkillRead(args);
      case "memory": return this.execMemory(args);
      case "skill_manage": return this.execSkillManage(args);
      case "ask_question": return this.execAskQuestion(args, callId ?? requestId, turnId);
      default: return this.callGrantedTool(name, args, requestId, turnId);
    }
  }

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
      if (Array.isArray(args.args) && args.args.every((v) => typeof v === "string")) overrides.args = args.args as string[];
      if (args.env && typeof args.env === "object" && !Array.isArray(args.env)) {
        overrides.env = Object.fromEntries(
          Object.entries(args.env as Record<string, unknown>).filter(([, v]) => typeof v === "string"),
        ) as Record<string, string>;
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

  private async callGrantedTool(name: string, args: Readonly<Record<string, unknown>>, requestId: string, turnId: string): Promise<unknown> {
    const route = this.routesFor(turnId).get(name);
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
        this.logger?.warn("Workspace sync failed for plugin %s: %s", route.pluginId, error instanceof Error ? error.message : String(error));
      }
    }
    const calls = this.activeCalls.get(turnId);
    calls?.set(requestId, route.pluginId);
    try {
      return await this.runtimeManager.callTool(parsePluginId(route.pluginId), { requestId, toolName: route.toolName, args: wrappedArgs });
    } finally {
      calls?.delete(requestId);
    }
  }

  private routesFor(turnId: string): Map<string, McpToolRoute> {
    let routes = this.turnRoutes.get(turnId);
    if (!routes) {
      routes = new Map();
      this.turnRoutes.set(turnId, routes);
    }
    return routes;
  }

  private async execDocsSearch(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const index = this.docsIndex;
    if (!index) return docsNotConfigured();
    if (!index.usable()) return docsNotReady();
    const query = requireString(args.query, "query");
    const topK = clampInt(args.top_k, 5, 1, 10);
    const hits = await index.search(query, topK);
    return {
      ok: true,
      data: { chunks: hits },
      meta: {
        count: hits.length,
        truncated: hits.length >= topK,
        index_ready: true,
        data_is_untrusted: true,
      },
    };
  }

  private async execDocsList(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const index = this.docsIndex;
    if (!index) return docsNotConfigured();
    if (!index.usable()) return docsNotReady();
    const limit = clampInt(args.limit, 50, 1, 100);
    const documents = await index.listDocs();
    const truncated = documents.length > limit;
    const limited = documents.slice(0, limit);
    return {
      ok: true,
      data: { documents: limited },
      meta: {
        count: limited.length,
        truncated,
        index_ready: true,
        data_is_untrusted: true,
      },
    };
  }

  private async execDocsRead(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const index = this.docsIndex;
    if (!index) return docsNotConfigured();
    if (!index.usable()) return docsNotReady();
    const path = requireString(args.path, "path");
    const chunkId = optionalString(args.chunk_id) || undefined;
    const doc = await index.readDoc(path, chunkId);
    if (!doc) {
      return {
        ok: false,
        error: { code: "not_found", message: "Document not found in docs corpus" },
        meta: { index_ready: true, data_is_untrusted: true },
      };
    }
    const offset = clampInt(args.offset, 0, 0, 1_000_000);
    const maxChars = clampInt(args.max_chars, 0, 0, 20_000);
    const text = maxChars > 0 ? doc.text.slice(offset, offset + maxChars) : doc.text.slice(offset);
    const fullEnd = offset + (maxChars > 0 ? maxChars : doc.text.length);
    const hasMore = fullEnd < doc.text.length;
    return {
      ok: true,
      data: {
        path: doc.path,
        title: doc.title,
        headings: doc.headings,
        domain: doc.domain,
        text,
        chunk_id: doc.chunkId,
        has_more: hasMore,
        next_offset: hasMore ? fullEnd : undefined,
      },
      meta: { index_ready: true, data_is_untrusted: true },
    };
  }

  private async execSkillList(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const registry = this.skillRegistry;
    if (!registry) return skillsNotConfigured();
    const limit = clampInt(args.limit, 50, 1, 100);
    const all = await registry.list();
    const skills = all.slice(0, limit);
    return {
      ok: true,
      data: { skills },
      meta: { count: skills.length, truncated: all.length > limit, data_is_untrusted: true },
    };
  }

  private async execSkillSearch(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const registry = this.skillRegistry;
    if (!registry) return skillsNotConfigured();
    const query = requireString(args.query, "query");
    const limit = clampInt(args.limit, 20, 1, 50);
    const matches = await registry.search(query, limit + 1);
    const skills = matches.slice(0, limit);
    return {
      ok: true,
      data: { skills },
      meta: { count: skills.length, truncated: matches.length > limit, data_is_untrusted: true },
    };
  }

  private async execSkillRead(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const registry = this.skillRegistry;
    if (!registry) return skillsNotConfigured();
    const skillId = requireString(args.skill_id, "skill_id");
    const path = optionalString(args.path) || "SKILL.md";
    const offset = clampInt(args.offset, 0, 0, 10_000_000);
    const maxChars = clampInt(args.max_chars, 20_000, 1, 100_000);
    try {
      const file = await registry.read(skillId, path, offset, maxChars);
      void this.bumpUsage(skillId, "view");
      return { ok: true, data: file, meta: { data_is_untrusted: true } };
    } catch {
      return {
        ok: false,
        error: { code: "not_found", message: "Skill or skill file not found" },
        meta: { data_is_untrusted: true },
      };
    }
  }

  private bumpUsage(skillId: string, kind: UsageBumpKind): Promise<void> {
    if (!this.skillUsage) return Promise.resolve();
    return this.skillUsage.record(skillId, kind).catch((error) => {
      this.logger?.warn("skill usage bump failed skill=%s kind=%s: %s", skillId, kind, error instanceof Error ? error.message : String(error));
    });
  }

  private async execMemory(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const store = this.memoryStore;
    if (!store) return memoryNotConfigured();
    const action = requireString(args.action, "action");
    const target = requireString(args.target, "target") as MemoryTarget;
    if (target !== "memory" && target !== "user") {
      throw new ApplicationError("AGENT_INVALID_INPUT", `target must be "memory" or "user"`);
    }
    const content = optionalString(args.content);
    const oldText = optionalString(args.old_text);
    try {
      switch (action) {
        case "add":
          if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for add");
          return await store.add(target, content);
        case "replace":
          if (!oldText) throw new ApplicationError("AGENT_INVALID_INPUT", "old_text is required for replace");
          return await store.replace(target, oldText, content);
        case "remove":
          if (!oldText) throw new ApplicationError("AGENT_INVALID_INPUT", "old_text is required for remove");
          return await store.remove(target, oldText);
        default:
          throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported memory action: ${action}`);
      }
    } catch (error) {
      return {
        ok: false,
        error: {
          code: "memory_error",
          message: error instanceof Error ? error.message : String(error),
        },
        meta: {},
      };
    }
  }

  private async execSkillManage(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const registry = this.skillRegistry;
    if (!registry) return skillsNotConfigured();
    const provenance = this.skillProvenance;
    if (!provenance) return skillsNotConfigured();
    const action = requireString(args.action, "action");
    const skillId = requireString(args.name, "name");
    const content = optionalString(args.content);
    const filePath = optionalString(args.path);
    const shouldStage = this.writeOrigin === "background_review" && this.writeApprovalEnabled && this.approvalStaging;
    try {
      switch (action) {
        case "create": {
          if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for create");
          if (shouldStage) {
            const pending = await this.approvalStaging!.stage(skillId, "create", "SKILL.md", content);
            return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
          }
          const detail = await registry.create(skillId, content);
          await provenance.markAgent(skillId);
          void this.bumpUsage(skillId, "patch");
          return { ok: true, data: detail, meta: { provenance: "agent" } };
        }
        case "edit": {
          if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for edit");
          const origin = await provenance.get(skillId);
          if (origin !== "agent") return skillProtected(skillId);
          if (shouldStage) {
            const pending = await this.approvalStaging!.stage(skillId, "edit", "SKILL.md", content);
            return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
          }
          const result = await registry.write(skillId, "SKILL.md", content);
          void this.bumpUsage(skillId, "patch");
          return { ok: true, data: result, meta: { provenance: "agent" } };
        }
        case "write_file": {
          if (!content) throw new ApplicationError("AGENT_INVALID_INPUT", "content is required for write_file");
          if (!filePath) throw new ApplicationError("AGENT_INVALID_INPUT", "path is required for write_file");
          const origin = await provenance.get(skillId);
          if (origin !== "agent") return skillProtected(skillId);
          if (shouldStage) {
            const pending = await this.approvalStaging!.stage(skillId, "write_file", filePath, content);
            return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
          }
          const result = await registry.write(skillId, filePath, content);
          void this.bumpUsage(skillId, "patch");
          return { ok: true, data: result, meta: { provenance: "agent" } };
        }
        case "delete": {
          const origin = await provenance.get(skillId);
          if (origin !== "agent") return skillProtected(skillId);
          if (this.skillUsage) {
            const usage = await this.skillUsage.getRecord(skillId);
            if (usage.pinned) return skillPinned(skillId);
          }
          if (shouldStage) {
            const pending = await this.approvalStaging!.stage(skillId, "delete", "", "");
            return { ok: true, data: { staged: true, id: pending.id }, meta: { provenance: "agent", staged: true } };
          }
          await registry.delete(skillId);
          await provenance.clear(skillId);
          return { ok: true, data: { deleted: skillId }, meta: { provenance: "agent" } };
        }
        default:
          throw new ApplicationError("AGENT_INVALID_INPUT", `Unsupported skill_manage action: ${action}`);
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      const code = message.includes("already exists") ? "skill_exists"
        : message.includes("60 characters") ? "description_too_long"
        : "skill_error";
      return { ok: false, error: { code, message }, meta: {} };
    }
  }

  private async execAskQuestion(
    args: Readonly<Record<string, unknown>>,
    callId: string,
    turnId: string,
  ): Promise<unknown> {
    if (!this.askQuestions) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "ask_question is not available in this runtime");
    }
    if (!this.isInteractive(turnId)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "ask_question is only available during interactive agent turns");
    }
    const question = requireString(args.question, "question").trim();
    if (!question) throw new ApplicationError("AGENT_INVALID_INPUT", "question must not be empty");
    const options = parseAskOptions(args.options);
    const allowFreeText = args.allow_free_text === undefined ? true : Boolean(args.allow_free_text);
    const multiSelect = Boolean(args.multi_select);
    return this.askQuestions.ask(turnId, callId, {
      question,
      options,
      allowFreeText,
      multiSelect,
    });
  }

  private isInteractive(turnId: string): boolean {
    return this.askQuestions !== undefined && this.turnInteractive.get(turnId) === true;
  }
}

function parseAskOptions(raw: unknown): AskQuestionOption[] {
  if (!Array.isArray(raw) || raw.length === 0) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "options must be a non-empty array");
  }
  if (raw.length > MAX_ASK_OPTIONS) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `options must contain at most ${MAX_ASK_OPTIONS} items`);
  }
  const seen = new Set<string>();
  const options: AskQuestionOption[] = [];
  for (const entry of raw) {
    if (typeof entry !== "object" || entry === null || Array.isArray(entry)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "each option must be an object");
    }
    const id = requireString(entry.id, "options[].id").trim();
    const label = requireString(entry.label, "options[].label").trim();
    if (!id || !label) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "each option requires non-empty id and label");
    }
    if (seen.has(id)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `duplicate option id: ${id}`);
    }
    seen.add(id);
    options.push({
      id,
      label,
      ...(typeof entry.description === "string" && entry.description.trim() ? { description: entry.description.trim() } : {}),
      ...(entry.default === true ? { default: true } : {}),
      ...(typeof entry.icon === "string" && entry.icon.trim() ? { icon: entry.icon.trim() } : {}),
      ...(typeof entry.image === "string" && entry.image.trim() ? { image: entry.image.trim() } : {}),
    });
  }
  return options;
}

function skillProtected(skillId: string): unknown {
  return {
    ok: false,
    error: { code: "skill_protected", message: `Skill "${skillId}" is not agent-owned and cannot be mutated by the model` },
    meta: {},
  };
}

function skillPinned(skillId: string): unknown {
  return {
    ok: false,
    error: { code: "skill_pinned", message: `Skill "${skillId}" is pinned and cannot be deleted` },
    meta: {},
  };
}

function definition(
  name: string,
  description: string,
  properties: Readonly<Record<string, unknown>> = {},
  required: readonly string[] = Object.keys(properties),
): AgentToolDefinition {
  return { name, description, inputSchema: { type: "object", properties, required } };
}
function stringSchema(): Readonly<Record<string, unknown>> { return { type: "string" }; }
function optionalString(value: unknown): string { return typeof value === "string" ? value.trim() : ""; }
function requireString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim().length === 0) throw new ApplicationError("AGENT_INVALID_INPUT", `${name} is required`);
  return value;
}
function stringRecord(value: unknown): Readonly<Record<string, string>> {
  if (value === undefined) return {};
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "arguments must be an object of strings");
  }
  const out: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    if (typeof item !== "string") throw new ApplicationError("AGENT_INVALID_INPUT", `Prompt argument must be a string: ${key}`);
    out[key] = item;
  }
  return out;
}
function parsePluginId(value: unknown): PluginId {
  const parsed = PluginId.create(requireString(value, "pluginId"));
  if (!parsed.ok) throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${parsed.error.message}`);
  return parsed.value;
}
function toProviderToolName(pluginId: string, toolName: string): string {
  const readablePlugin = pluginId.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 28);
  const readableTool = toolName.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 20);
  return `mcp_${readablePlugin}_${readableTool}`;
}
function docsNotConfigured(): unknown {
  return {
    ok: false,
    error: { code: "docs_not_configured", message: "Documentation index is not configured" },
    meta: { index_ready: false },
  };
}
function docsNotReady(): unknown {
  return {
    ok: false,
    error: { code: "docs_index_not_ready", message: "Documentation index is not ready" },
    meta: { index_ready: false },
  };
}
function skillsNotConfigured(): unknown {
  return {
    ok: false,
    error: { code: "skills_not_configured", message: "Skill registry is not configured" },
    meta: { data_is_untrusted: true },
  };
}
function memoryNotConfigured(): unknown {
  return {
    ok: false,
    error: { code: "memory_not_configured", message: "Memory store is not configured" },
    meta: {},
  };
}
function clampInt(value: unknown, fallback: number, min: number, max: number): number {
  let parsed: number;
  if (typeof value === "number") parsed = value;
  else if (typeof value === "string") parsed = Number.parseInt(value, 10);
  else parsed = NaN;
  if (!Number.isFinite(parsed) || Number.isNaN(parsed)) parsed = fallback;
  return Math.max(min, Math.min(max, parsed));
}
