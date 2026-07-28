import { createHash } from "node:crypto";
import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { PluginRuntimeManager } from "../../plugin/services/plugin-runtime-manager.js";
import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../ports/agent-tool-gateway.port.js";

interface McpToolRoute {
  readonly pluginId: string;
  readonly toolName: string;
}

/** Adapts the shell-owned plugin runtime into the agent's only tool boundary. */
export class McpAgentToolGateway implements AgentToolGateway {
  private readonly routes = new Map<string, McpToolRoute>();

  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async listTools(pluginIds: readonly string[]): Promise<readonly AgentToolDefinition[]> {
    const uniquePluginIds = [...new Set(pluginIds)];
    const exposed: AgentToolDefinition[] = [];

    for (const pluginIdValue of uniquePluginIds) {
      const pluginId = PluginId.create(pluginIdValue);
      if (!pluginId.ok) {
        throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${pluginId.error.message}`);
      }
      const tools = await this.runtimeManager.listTools(pluginId.value);
      for (const tool of tools) {
        const name = toProviderToolName(pluginIdValue, tool.name);
        const existing = this.routes.get(name);
        if (existing && (existing.pluginId !== pluginIdValue || existing.toolName !== tool.name)) {
          throw new ApplicationError("AGENT_INVALID_INPUT", "MCP tool name collision", { name });
        }
        this.routes.set(name, { pluginId: pluginIdValue, toolName: tool.name });
        exposed.push({
          name,
          ...(tool.description ? { description: tool.description } : {}),
          ...(tool.inputSchema ? { inputSchema: tool.inputSchema } : {}),
        });
      }
    }
    return exposed;
  }

  async execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
  ): Promise<unknown> {
    const route = this.routes.get(name);
    if (!route) {
      throw new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "MCP tool is not exposed to this agent", { name });
    }
    const pluginId = PluginId.create(route.pluginId);
    if (!pluginId.ok) {
      throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${pluginId.error.message}`);
    }
    return this.runtimeManager.callTool(pluginId.value, {
      requestId,
      toolName: route.toolName,
      args,
    });
  }
}

function toProviderToolName(pluginId: string, toolName: string): string {
  const readablePlugin = pluginId.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 28);
  const readableTool = toolName.replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 20);
  const fingerprint = createHash("sha256").update(`${pluginId}\u0000${toolName}`).digest("hex").slice(0, 12);
  return `mcp_${readablePlugin}_${readableTool}_${fingerprint}`;
}
