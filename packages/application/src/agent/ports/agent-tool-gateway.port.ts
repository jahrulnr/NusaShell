import type { AgentToolDefinition } from "./agent-provider.port.js";

export interface AgentToolGateway {
  beginTurn?(turnId: string): void;
  endTurn?(turnId: string): void;
  cancelTurn?(turnId: string): Promise<void> | void;
  listTools(pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]>;
  execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
    turnId: string,
  ): Promise<unknown>;
}
