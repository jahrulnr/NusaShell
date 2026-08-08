import type { PluginRuntimeManager } from "../../plugin/services/plugin-runtime-manager.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { AgentToolDefinition } from "../ports/agent-provider.port.js";
import type { McpLiveSnapshot, McpLiveSnapshotTool } from "./mcp-live-prompt-formatter.js";
import { MCP_LIVE_TOOLS_CAP } from "./mcp-live-prompt-formatter.js";
import { parsePluginId, toProviderToolName } from "./gateway-utils.js";
import { emptySchema, type McpToolRoute } from "./gateway-types.js";
import type { GatewayRouteStore } from "./gateway-route-store.js";

/**
 * Live MCP snapshot + running-plugin tool seeding for the agent tool gateway.
 * Extracted from `McpAgentToolGateway` (#11). Read-only with respect to plugin
 * lifecycle: it never starts/stops plugins, only enumerates running ones.
 */
export class GatewayLiveSnapshot {
  constructor(
    private readonly runtimeManager: PluginRuntimeManager,
    private readonly routeStore: GatewayRouteStore,
    private readonly logger?: LoggerPort,
  ) {}

  /**
   * Build a Live MCP runtime snapshot for the Live MCP system-prompt block.
   * Read-only: does not start plugins or mutate turnRoutes. `running` comes
   * from `runtimeManager.listPlugins` (state === "running"); `tools` is the
   * full catalog (name + description + inputSchema) for every tool on those
   * running plugins, plus any sticky conversation grants still active this
   * turn. This checkpoint catalog is intentionally not limited by the
   * provider `tools[]` cap; the formatter owns its character budget while
   * `cappedRouteDefinitions` separately bounds typed function definitions.
   * Fail-soft: on listPlugins / listTools error, log + skip the offending
   * plugin (never fail the turn).
   */
  async getMcpLiveSnapshot(turnId: string): Promise<McpLiveSnapshot> {
    const running = await this.listRunningPlugins();
    const routes = this.routeStore.turnRouteMap(turnId) ?? new Map<string, McpToolRoute>();
    const catalog = await this.enumerateRunningTools(running);
    // Merge: running tools first, then sticky extras not covered by running.
    const seen = new Set<string>();
    const merged: McpLiveSnapshotTool[] = [];
    for (const tool of catalog) {
      if (seen.has(tool.providerName)) continue;
      seen.add(tool.providerName);
      merged.push(tool);
    }
    for (const [name, route] of routes) {
      if (seen.has(name)) continue;
      seen.add(name);
      merged.push({
        providerName: name,
        pluginId: route.pluginId,
        toolName: route.toolName,
        inputSchema: route.inputSchema,
        ...(route.description ? { description: route.description } : {}),
      });
    }
    merged.sort((a, b) => a.providerName < b.providerName ? -1 : a.providerName > b.providerName ? 1 : 0);
    return {
      running: running.map((pluginId) => ({ pluginId })),
      tools: merged,
    };
  }

  /**
   * One-line trust signal for tool results (mcp_enable / tool_schema /
   * tool_schemas). Shows running plugin ids + this-turn advertised tool
   * names so the model can decide "ready to call" without re-enabling or
   * re-granting. Fail-soft: returns a minimal line on error.
   */
  async buildLiveStateLine(turnId: string): Promise<string> {
    try {
      const snap = await this.getMcpLiveSnapshot(turnId);
      const running = snap.running.map((p) => p.pluginId).sort();
      const advertised = snap.tools.map((t) => t.providerName).sort();
      const parts: string[] = [];
      if (running.length > 0) parts.push(`running: ${running.join(", ")}`);
      if (advertised.length > 0) parts.push(`granted: ${advertised.join(", ")}`);
      if (parts.length === 0) return "no plugins running; no tools granted";
      return `${parts.join(" | ")} → ready to call`;
    } catch (error) {
      this.logger?.warn(
        "buildLiveStateLine failed: %s",
        error instanceof Error ? error.message : String(error),
      );
      return "live state unavailable";
    }
  }

  /**
   * Auto-seed the turn's routes for every tool on currently running plugins
   * so the model can call provider names directly without prior `tool_schema`.
   * Idempotent: tools already in the route map are not re-written. Fail-soft:
   * enumeration errors do not block `listTools`.
   */
  async autoSeedRunningTools(turnId: string): Promise<void> {
    const routes = this.routeStore.routesFor(turnId);
    const running = await this.listRunningPlugins();
    const catalog = await this.enumerateRunningTools(running);
    for (const tool of catalog) {
      if (routes.has(tool.providerName)) continue;
      const route: McpToolRoute = {
        pluginId: tool.pluginId,
        toolName: tool.toolName,
        inputSchema: tool.inputSchema,
        ...(tool.description ? { description: tool.description } : {}),
      };
      routes.set(tool.providerName, route);
      // Persist to conversation sticky store so auto-continue turns inherit.
      this.routeStore.persistConversationRoute(turnId, tool.providerName, route);
    }
  }

  /**
   * Build the capped list of route definitions for the provider `tools[]`
   * array. Running-plugin tools win sort order; sticky extras fill remaining
   * slots. Hard cap at `MCP_LIVE_TOOLS_CAP` entries beyond meta-tools.
   */
  cappedRouteDefinitions(routes: Map<string, McpToolRoute>): AgentToolDefinition[] {
    return [...routes.entries()]
      .sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)
      .slice(0, MCP_LIVE_TOOLS_CAP)
      .map(([name, route]) => ({
        name,
        ...(route.description ? { description: route.description } : {}),
        inputSchema: route.inputSchema,
      }));
  }

  /**
   * List currently running plugin ids. Fail-soft: returns [] on error.
   */
  private async listRunningPlugins(): Promise<readonly string[]> {
    try {
      const plugins = await this.runtimeManager.listPlugins();
      return plugins
        .filter((p) => p.state === "running")
        .map((p) => p.pluginId)
        .sort();
    } catch (error) {
      this.logger?.warn(
        "listRunningPlugins failed: %s",
        error instanceof Error ? error.message : String(error),
      );
      return [];
    }
  }

  /**
   * Enumerate the full tool catalog for every running plugin. Fail-soft per
   * plugin: a `listTools` error on one plugin is logged and that plugin is
   * skipped; other plugins still contribute. Returns tools sorted by
   * `providerName` for prompt-cache stability.
   */
  private async enumerateRunningTools(
    runningPluginIds: readonly string[],
  ): Promise<McpLiveSnapshotTool[]> {
    const catalog: McpLiveSnapshotTool[] = [];
    for (const pluginId of runningPluginIds) {
      try {
        const tools = await this.runtimeManager.listTools(parsePluginId(pluginId));
        for (const tool of tools) {
          catalog.push({
            providerName: toProviderToolName(pluginId, tool.name),
            pluginId,
            toolName: tool.name,
            inputSchema: tool.inputSchema ?? emptySchema,
            ...(tool.description ? { description: tool.description } : {}),
          });
        }
      } catch (error) {
        this.logger?.warn(
          "enumerateRunningTools skipped plugin=%s: %s",
          pluginId,
          error instanceof Error ? error.message : String(error),
        );
      }
    }
    catalog.sort((a, b) => a.providerName < b.providerName ? -1 : a.providerName > b.providerName ? 1 : 0);
    return catalog;
  }
}
