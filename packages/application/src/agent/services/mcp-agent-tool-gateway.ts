import { createHash } from "node:crypto";
import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { PluginRuntimeManager } from "../../plugin/services/plugin-runtime-manager.js";
import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../ports/agent-tool-gateway.port.js";

interface McpToolRoute {
  readonly pluginId: string;
  readonly toolName: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
  readonly description?: string;
}

const emptySchema = { type: "object", properties: {} } as const;

/**
 * Shell-owned progressive MCP catalog. The model first discovers servers and
 * tools, then grants one typed MCP tool for the following provider round.
 */
export class McpAgentToolGateway implements AgentToolGateway {
  private readonly turnRoutes = new Map<string, Map<string, McpToolRoute>>();
  private readonly activeCalls = new Map<string, Map<string, string>>();

  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  beginTurn(turnId: string): void {
    this.turnRoutes.set(turnId, new Map());
    this.activeCalls.set(turnId, new Map());
  }

  endTurn(turnId: string): void {
    this.turnRoutes.delete(turnId);
    this.activeCalls.delete(turnId);
  }

  async cancelTurn(turnId: string): Promise<void> {
    const calls = [...(this.activeCalls.get(turnId)?.entries() ?? [])];
    await Promise.allSettled(calls.map(([requestId, pluginId]) =>
      this.runtimeManager.cancelTool(parsePluginId(pluginId), requestId),
    ));
  }

  async listTools(_pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]> {
    const routes = this.routesFor(turnId);
    return [
      definition("mcp_list", "List MCP plugins, runtime state, and autostart preference"),
      definition("mcp_enable", "Start a selected MCP plugin", { pluginId: stringSchema() }),
      definition("mcp_disable", "Stop a selected MCP plugin", { pluginId: stringSchema() }),
      definition("tool_search", "Search a running MCP plugin's tools by name or description", { pluginId: stringSchema(), query: stringSchema() }),
      definition("tool_list", "List all tools from a running MCP plugin (names and descriptions only)", { pluginId: stringSchema() }),
      definition("tool_schema", "Load one searched MCP tool schema for the next round", { pluginId: stringSchema(), toolName: stringSchema() }),
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
      ...[...routes.entries()].map(([name, route]) => ({
        name,
        ...(route.description ? { description: route.description } : {}),
        inputSchema: route.inputSchema,
      })),
    ];
  }

  async execute(name: string, args: Readonly<Record<string, unknown>>, requestId: string, turnId: string): Promise<unknown> {
    switch (name) {
      case "mcp_list": return this.runtimeManager.listPlugins();
      case "mcp_enable": return this.changeMcpState(args, true);
      case "mcp_disable": return this.changeMcpState(args, false);
      case "tool_list": return this.listAllTools(args);
      case "tool_search": return this.searchTools(args);
      case "tool_schema": return this.grantTool(args, turnId);
      case "mcp_context": return this.context(args);
      default: return this.callGrantedTool(name, args, requestId, turnId);
    }
  }

  private async changeMcpState(args: Readonly<Record<string, unknown>>, start: boolean): Promise<unknown> {
    const pluginId = parsePluginId(args.pluginId);
    const view = start ? await this.runtimeManager.startPlugin(pluginId) : await this.runtimeManager.stopPlugin(pluginId);
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
    if (!route) throw new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "AI provider requested a tool outside the MCP allowlist", { name });
    const calls = this.activeCalls.get(turnId);
    calls?.set(requestId, route.pluginId);
    try {
      return await this.runtimeManager.callTool(parsePluginId(route.pluginId), { requestId, toolName: route.toolName, args });
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
  const fingerprint = createHash("sha256").update(`${pluginId}\u0000${toolName}`).digest("hex").slice(0, 12);
  return `mcp_${readablePlugin}_${readableTool}_${fingerprint}`;
}
