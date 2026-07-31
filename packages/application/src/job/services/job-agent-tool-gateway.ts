import type { AgentToolDefinition } from "../../agent/ports/agent-provider.port.js";
import type { AgentToolGateway } from "../../agent/ports/agent-tool-gateway.port.js";
import type { McpAgentToolGateway } from "../../agent/services/mcp-agent-tool-gateway.js";

/**
 * Tools a scheduled job may NOT touch this ship. Jobs must not mutate the
 * learning stores (memory/skills) — they are automation, not learning.
 */
const JOB_DENYLIST = new Set([
  "memory",
  "skill_manage",
  "skill_list",
  "skill_search",
  "skill_read",
]);

/**
 * Restricted gateway for headless job agent turns. Allows MCP plugin tool
 * discovery/granting and docs tools, but denies memory and skill tools so a
 * scheduled job cannot mutate the user's learning stores. Thin wrapper over
 * the shared `McpAgentToolGateway` (no separate turn state).
 */
export class JobAgentToolGateway implements AgentToolGateway {
  constructor(private readonly inner: McpAgentToolGateway) {}

  beginTurn(turnId: string): void {
    this.inner.beginTurn(turnId);
  }

  endTurn(turnId: string): void {
    this.inner.endTurn(turnId);
  }

  cancelTurn(turnId: string): Promise<void> | void {
    return this.inner.cancelTurn(turnId);
  }

  async listTools(pluginIds: readonly string[], turnId: string): Promise<readonly AgentToolDefinition[]> {
    const all = await this.inner.listTools(pluginIds, turnId);
    return all.filter((tool) => !JOB_DENYLIST.has(tool.name));
  }

  async execute(
    name: string,
    args: Readonly<Record<string, unknown>>,
    requestId: string,
    turnId: string,
  ): Promise<unknown> {
    if (JOB_DENYLIST.has(name)) {
      throw new Error(`Tool "${name}" is not allowed in a scheduled job`);
    }
    return this.inner.execute(name, args, requestId, turnId);
  }
}
