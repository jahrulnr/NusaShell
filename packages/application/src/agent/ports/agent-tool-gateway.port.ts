import type { AgentToolDefinition, ReasoningEffort } from "./agent-provider.port.js";

export interface AgentTurnContext {
  readonly interactive?: boolean;
  /**
   * Conversation id scoped to this turn. Used by conversation-scoped meta-tools
   * (e.g. `todo`) to address the right conversation todo list.
   */
  readonly conversationId?: string;
  /**
   * Conversation workspace, the source of truth for agent tool I/O. When set,
   * the gateway injects it into bundled path/cwd-shaped tool arguments and
   * syncs it to roots-capable MCP servers (Phase 2) / respawns static ones
   * (Phase 3). Prompt-only injection is the legacy fallback.
   */
  readonly workspace?: string;
  /** Caller turn's provider — inherited by agent-mode jobs created via the `job` tool. */
  readonly providerId?: string;
  /** Caller turn's model — inherited by agent-mode jobs created via the `job` tool. */
  readonly model?: string;
  /** Caller turn's reasoning effort — inherited by agent-mode jobs created via the `job` tool. */
  readonly effort?: ReasoningEffort;
}

export interface AgentToolGateway {
  beginTurn?(turnId: string, context?: AgentTurnContext): void;
  endTurn?(turnId: string): void;
  cancelTurn?(turnId: string): Promise<void> | void;
  listTools(pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]>;
  execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
    turnId: string,
    callId?: string,
    options?: { readonly signal?: AbortSignal },
  ): Promise<unknown>;
}
