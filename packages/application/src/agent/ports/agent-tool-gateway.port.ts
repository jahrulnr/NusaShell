import type { AgentToolDefinition, ReasoningEffort } from "./agent-provider.port.js";
import type { McpLiveSnapshot } from "../services/mcp-live-prompt-formatter.js";
import type { WriteOrigin } from "../services/gateway-types.js";

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
  /** Origin used by write-capable meta-tools; scoped to this turn only. */
  readonly writeOrigin?: WriteOrigin;
}

export interface AgentToolGateway {
  beginTurn?(turnId: string, context?: AgentTurnContext): void;
  endTurn?(turnId: string): void;
  /**
   * Clear sticky grants for a conversation. Called when the conversation is
   * deleted or sealed permanently so future turns do not inherit stale grants.
   * Optional: stub/review gateways may omit this.
   */
  endConversation?(conversationId: string): void;
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
  /**
   * Optional: build a Live MCP runtime snapshot (running plugin ids + full
   * tool catalog for those plugins). Used by `RunAgentTurnHandler` to build a
   * hidden, conversation-scoped runtime checkpoint with the complete
   * tool name/description/inputSchema for every running MCP tool.
   * Stub/review gateways may omit this; the handler duck-types before calling.
   */
  getMcpLiveSnapshot?(turnId: string): Promise<McpLiveSnapshot>;
}
