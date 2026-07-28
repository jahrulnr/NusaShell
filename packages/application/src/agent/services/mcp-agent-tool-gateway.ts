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
  private readonly routes = new Map<string, McpToolRoute>();

  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async listTools(_pluginIds: readonly string[]): Promise<readonly AgentToolDefinition[]> {
    return [
      definition("mcp_list", "List MCP plugins, runtime state, and autostart preference"),
      definition("mcp_enable", "Start a selected MCP plugin", { pluginId: stringSchema() }),
      definition("mcp_disable", "Stop a selected MCP plugin", { pluginId: stringSchema() }),
      definition("tool_search", "Search a running MCP plugin's tools by name or description", { pluginId: stringSchema(), query: stringSchema() }),
      definition("tool_schema", "Load one searched MCP tool schema for the next round", { pluginId: stringSchema(), toolName: stringSchema() }),
      definition("resource_search", "Search text resources exposed by a running MCP plugin", { pluginId: stringSchema(), query: stringSchema() }),
      definition("resource_read", "Read a discovered text resource as bounded agent context", { pluginId: stringSchema(), uri: stringSchema() }),
      ...[...this.routes.entries()].map(([name, route]) => ({
        name,
        ...(route.description ? { description: route.description } : {}),
        inputSchema: route.inputSchema,
      })),
    ];
  }

  async execute(name: string, args: Readonly<Record<string, unknown>>, requestId: string): Promise<unknown> {
    switch (name) {
      case "mcp_list": return this.runtimeManager.listPlugins();
      case "mcp_enable": return this.changeMcpState(args, true);
      case "mcp_disable": return this.changeMcpState(args, false);
      case "tool_search": return this.searchTools(args);
      case "tool_schema": return this.grantTool(args);
      case "resource_search": return this.searchResources(args);
      case "resource_read": return this.readResource(args);
      default: return this.callGrantedTool(name, args, requestId);
    }
  }

  private async changeMcpState(args: Readonly<Record<string, unknown>>, start: boolean): Promise<unknown> {
    const pluginId = parsePluginId(args.pluginId);
    const view = start ? await this.runtimeManager.startPlugin(pluginId) : await this.runtimeManager.stopPlugin(pluginId);
    return { pluginId: view.pluginId, state: view.state };
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

  private async grantTool(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const toolName = requireString(args.toolName, "toolName");
    const tool = (await this.runtimeManager.listTools(parsePluginId(pluginIdValue))).find((item) => item.name === toolName);
    if (!tool) throw new ApplicationError("TOOL_NOT_FOUND", `MCP tool not found: ${toolName}`);
    const providerName = toProviderToolName(pluginIdValue, tool.name);
    const inputSchema = tool.inputSchema ?? emptySchema;
    this.routes.set(providerName, { pluginId: pluginIdValue, toolName: tool.name, inputSchema, ...(tool.description ? { description: tool.description } : {}) });
    return { name: providerName, ...(tool.description ? { description: tool.description } : {}), inputSchema };
  }

  private async searchResources(args: Readonly<Record<string, unknown>>): Promise<unknown> {
    const pluginIdValue = requireString(args.pluginId, "pluginId");
    const query = requireString(args.query, "query").toLowerCase();
    return (await this.runtimeManager.listResources(parsePluginId(pluginIdValue)))
      .filter((resource) => `${resource.name} ${resource.uri} ${resource.description ?? ""}`.toLowerCase().includes(query))
      .slice(0, 20);
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

  private async callGrantedTool(name: string, args: Readonly<Record<string, unknown>>, requestId: string): Promise<unknown> {
    const route = this.routes.get(name);
    if (!route) throw new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "AI provider requested a tool outside the MCP allowlist", { name });
    return this.runtimeManager.callTool(parsePluginId(route.pluginId), { requestId, toolName: route.toolName, args });
  }
}

function definition(name: string, description: string, properties: Readonly<Record<string, unknown>> = {}): AgentToolDefinition {
  return { name, description, inputSchema: { type: "object", properties, required: Object.keys(properties) } };
}
function stringSchema(): Readonly<Record<string, unknown>> { return { type: "string" }; }
function requireString(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim().length === 0) throw new ApplicationError("AGENT_INVALID_INPUT", `${name} is required`);
  return value;
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
