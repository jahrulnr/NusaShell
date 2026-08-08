import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { AgentToolGateway, AgentTurnContext } from "../ports/agent-tool-gateway.port.js";
import type { McpAgentToolGateway } from "./mcp-agent-tool-gateway.js";

const REVIEW_WHITELIST = new Set([
  "memory",
  "skill_list",
  "skill_search",
  "skill_read",
  "skill_manage",
]);

/**
 * Restricted gateway for background review turns. Only allows the memory and
 * skill meta-tools; all MCP/plugin tools are denied. Marks the entire turn as
 * background_review using turn-scoped context, so concurrent foreground turns
 * cannot inherit a mutable global write origin.
 */
export class ReviewAgentToolGateway implements AgentToolGateway {
  constructor(private readonly inner: McpAgentToolGateway) {}

  beginTurn(turnId: string, context?: AgentTurnContext): void {
    this.inner.beginTurn(turnId, { ...context, writeOrigin: "background_review" });
  }

  endTurn(turnId: string): void {
    this.inner.endTurn(turnId);
  }

  endConversation(conversationId: string): void {
    this.inner.endConversation(conversationId);
  }

  cancelTurn(turnId: string): Promise<void> | void {
    return this.inner.cancelTurn(turnId);
  }

  async listTools(_pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]> {
    const all = await this.inner.listTools([], turnId);
    return all.filter((tool) => REVIEW_WHITELIST.has(tool.name));
  }

  async execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
    turnId: string,
    callId?: string,
  ): Promise<unknown> {
    if (!REVIEW_WHITELIST.has(name)) {
      throw new Error(`Tool "${name}" is not allowed in background review`);
    }
    return this.inner.execute(name, args, requestId, turnId, callId);
  }
}
