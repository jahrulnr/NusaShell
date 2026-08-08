import type { AgentTurnContext } from "../ports/agent-tool-gateway.port.js";
import type { ReasoningEffort } from "../ports/agent-provider.port.js";
import type { McpToolRoute } from "./gateway-types.js";
import type { WriteOrigin } from "./gateway-types.js";

/**
 * Per-turn / per-conversation state for the agent tool gateway: the granted
 * MCP tool routes (turn + sticky conversation grants), active in-flight calls,
 * and the per-turn context maps (workspace, provider, model, effort,
 * interactive, conversationId).
 *
 * Extracted from `McpAgentToolGateway` (#11) so the main class stays a thin
 * composition over focused modules. The route store owns no runtime side
 * effects (no `runtimeManager` calls); lifecycle methods mutate maps only.
 */
export class GatewayRouteStore {
  private readonly turnRoutes = new Map<string, Map<string, McpToolRoute>>();
  /** Sticky grants per conversation (seeded into each turn; cleared by endConversation). */
  private readonly conversationRoutes = new Map<string, Map<string, McpToolRoute>>();
  private readonly activeCalls = new Map<string, Map<string, string>>();
  private readonly turnInteractive = new Map<string, boolean>();
  private readonly turnWorkspace = new Map<string, string | undefined>();
  private readonly turnProviderId = new Map<string, string | undefined>();
  private readonly turnModel = new Map<string, string | undefined>();
  private readonly turnEffort = new Map<string, ReasoningEffort | undefined>();
  private readonly turnConversationId = new Map<string, string | undefined>();
  private readonly turnWriteOrigin = new Map<string, WriteOrigin | undefined>();

  beginTurn(turnId: string, context?: AgentTurnContext): void {
    if (!this.turnRoutes.has(turnId)) {
      const turnMap = new Map<string, McpToolRoute>();
      // Sticky grant seeding: copy routes from the conversation store so the
      // model starts the turn with previously-granted tools advertised. We
      // copy (not share) so endTurn for this turn does not mutate the
      // conversation store.
      const conversationId = context?.conversationId;
      if (conversationId) {
        const convMap = this.conversationRoutes.get(conversationId);
        if (convMap) {
          for (const [name, route] of convMap) turnMap.set(name, route);
        }
      }
      this.turnRoutes.set(turnId, turnMap);
    }
    if (!this.activeCalls.has(turnId)) this.activeCalls.set(turnId, new Map());
    // Merge only provided fields so a later beginTurn (e.g. AgentTurnRunner)
    // cannot wipe workspace / provider context set by RunAgentTurnHandler.
    if (context?.interactive !== undefined) this.turnInteractive.set(turnId, context.interactive);
    if (context?.workspace !== undefined) this.turnWorkspace.set(turnId, context.workspace);
    if (context?.providerId !== undefined) this.turnProviderId.set(turnId, context.providerId);
    if (context?.model !== undefined) this.turnModel.set(turnId, context.model);
    if (context?.effort !== undefined) this.turnEffort.set(turnId, context.effort);
    if (context?.conversationId !== undefined) this.turnConversationId.set(turnId, context.conversationId);
    if (context?.writeOrigin !== undefined) this.turnWriteOrigin.set(turnId, context.writeOrigin);
  }

  /**
   * Clear sticky grants for a conversation. Called when the conversation is
   * deleted or sealed permanently so a future turn with the same id (rare)
   * does not inherit stale grants.
   */
  endConversation(conversationId: string): void {
    this.conversationRoutes.delete(conversationId);
  }

  endTurn(turnId: string): void {
    this.turnRoutes.delete(turnId);
    this.activeCalls.delete(turnId);
    this.turnInteractive.delete(turnId);
    this.turnWorkspace.delete(turnId);
    this.turnProviderId.delete(turnId);
    this.turnModel.delete(turnId);
    this.turnEffort.delete(turnId);
    this.turnConversationId.delete(turnId);
    this.turnWriteOrigin.delete(turnId);
  }

  /** Get (creating if needed) the turn's route map. */
  routesFor(turnId: string): Map<string, McpToolRoute> {
    let routes = this.turnRoutes.get(turnId);
    if (!routes) {
      routes = new Map();
      this.turnRoutes.set(turnId, routes);
    }
    return routes;
  }

  /** Raw turn route map — undefined when the turn has no routes yet. */
  turnRouteMap(turnId: string): Map<string, McpToolRoute> | undefined {
    return this.turnRoutes.get(turnId);
  }

  /**
   * Persist a granted route to the conversation sticky store so subsequent
   * turns in the same conversation seed it without re-granting. No-op when
   * the turn has no bound conversationId.
   */
  persistConversationRoute(turnId: string, providerName: string, route: McpToolRoute): void {
    const conversationId = this.turnConversationId.get(turnId);
    if (!conversationId) return;
    let convMap = this.conversationRoutes.get(conversationId);
    if (!convMap) {
      convMap = new Map();
      this.conversationRoutes.set(conversationId, convMap);
    }
    convMap.set(providerName, route);
  }

  isTurnInteractive(turnId: string): boolean {
    return this.turnInteractive.get(turnId) === true;
  }

  workspaceOf(turnId: string): string | undefined {
    return this.turnWorkspace.get(turnId);
  }

  conversationIdOf(turnId: string): string | undefined {
    return this.turnConversationId.get(turnId);
  }

  providerIdOf(turnId: string): string | undefined {
    return this.turnProviderId.get(turnId);
  }

  modelOf(turnId: string): string | undefined {
    return this.turnModel.get(turnId);
  }

  effortOf(turnId: string): ReasoningEffort | undefined {
    return this.turnEffort.get(turnId);
  }

  writeOriginOf(turnId: string): WriteOrigin {
    return this.turnWriteOrigin.get(turnId) ?? "foreground";
  }

  /** In-flight calls for a turn (requestId -> pluginId). Creates an empty map. */
  activeCallsFor(turnId: string): Map<string, string> {
    let calls = this.activeCalls.get(turnId);
    if (!calls) {
      calls = new Map();
      this.activeCalls.set(turnId, calls);
    }
    return calls;
  }

  registerActiveCall(turnId: string, requestId: string, pluginId: string): void {
    this.activeCallsFor(turnId).set(requestId, pluginId);
  }

  unregisterActiveCall(turnId: string, requestId: string): void {
    this.activeCalls.get(turnId)?.delete(requestId);
  }
}
