import type { AgentToolDefinition } from "./agent-provider.port.js";

export interface AgentToolGateway {
  listTools(pluginIds: readonly string[]): Promise<readonly AgentToolDefinition[]>;
  execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
  ): Promise<unknown>;
}
