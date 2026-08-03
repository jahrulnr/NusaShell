import {
  type DomainEvent,
  PluginId,
  PluginRuntime,
  PluginStartedEvent,
} from "@nusashell/domain";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { ApplicationError } from "../../errors/application-error.js";
import type {
  McpClientPort,
  RootDescriptor,
} from "../ports/mcp-client.port.js";
import type { PluginRuntimeManagerDeps } from "./plugin-runtime-manager.js";
import type { RuntimeEntry, WorkspaceSyncResult } from "./plugin-runtime-types.js";

/**
 * `node` / `node.exe` with no script path reads stdin as JS — MCP initialize
 * JSON then throws SyntaxError and the start hangs until connect timeout.
 */
export function assertNodeStdioHasScript(
  command: string,
  args: readonly string[],
  pluginId: PluginId,
): void {
  const base = command.split(/[/\\]/).pop()?.toLowerCase() ?? command.toLowerCase();
  if (base !== "node" && base !== "node.exe") return;
  if (args.some((arg) => typeof arg === "string" && arg.length > 0 && !arg.startsWith("-"))) {
    return;
  }
  throw new ApplicationError(
    "PLUGIN_START_FAILED",
    `Plugin ${PluginId.toString(pluginId)} stdio command "${command}" requires a script path in args (empty args hang the MCP handshake)`,
    { pluginId: PluginId.toString(pluginId), command },
  );
}
/**
 * Manages MCP client sessions: creates stdio/http/sse connections, provides
 * access to running clients, syncs workspace roots, and proxies MCP
 * discovery calls (tools/prompts/resources/completion).
 *
 * Stateless — receives `entry` per call. The `runtimes` map stays in the
 * `PluginRuntimeManager` facade (single SoT).
 */
export class McpSessionManager {
  constructor(
    private readonly deps: PluginRuntimeManagerDeps,
  ) {}

  /**
   * Create the MCP transport connection (stdio/http/sse) and attach the client
   * to the entry. Does NOT transition to "running" — the caller must call
   * `transitionToRunning` after registering the exit watcher.
   */
  async connectTransport(entry: RuntimeEntry, plugin: import("@nusashell/domain").Plugin): Promise<void> {
    const manifest = plugin.manifest;
    this.registerAutomationEmits(entry, manifest);
    const automation = this.buildAutomationDeps(entry);
    if (manifest.mcp.transport === "stdio") {
      const command = manifest.mcp.command;
      if (!command) {
        throw new ApplicationError(
          "PLUGIN_START_FAILED",
          `Plugin ${PluginId.toString(entry.pluginId)} stdio transport missing command`,
        );
      }

      const runtimeEnvironment = await this.deps.resolveRuntimeEnvironment?.(
        PluginId.toString(entry.pluginId),
      ) ?? {};
      const environment = {
        ...manifest.mcp.env,
        ...runtimeEnvironment,
        ...(entry.workspace ? { NUSASHELL_WORKSPACE: entry.workspace } : {}),
        ...(entry.launchEnv ?? {}),
      };
      const args = entry.launchArgs ?? manifest.mcp.args;
      assertNodeStdioHasScript(command, args, entry.pluginId);
      const mcpClient = this.deps.mcpClientFactory.createForStdio(
        command,
        args,
        environment,
        plugin.installPath,
        automation,
      );
      // Attach before connect so stop can abort a hung handshake.
      entry.mcpClient = mcpClient;
      this.deps.logger?.debug(`Starting MCP stdio process: command=${command} args=${JSON.stringify(args)} cwd=${plugin.installPath} workspace=${entry.workspace ?? "(none)"}`);
      await mcpClient.connect();
      this.deps.logger?.info(`MCP client connected (stdio) for plugin ${PluginId.toString(entry.pluginId)}`);
      entry.runningArgs = args;
      entry.runningEnv = environment;
      if (entry.workspace) {
        try {
          await this.applyRoots(entry, entry.workspace);
        } catch (error) {
          this.deps.logger?.warn(`Initial roots apply failed for ${PluginId.toString(entry.pluginId)}: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
    } else if (manifest.mcp.transport === "http") {
      const url = manifest.mcp.url;
      if (!url) {
        throw new ApplicationError(
          "PLUGIN_START_FAILED",
          `Plugin ${PluginId.toString(entry.pluginId)} http transport missing url`,
        );
      }
      const mcpClient = this.deps.mcpClientFactory.createForHttp(url, manifest.mcp.headers, automation);
      entry.mcpClient = mcpClient;
      await mcpClient.connect();
    } else if (manifest.mcp.transport === "sse") {
      const url = manifest.mcp.url;
      if (!url) {
        throw new ApplicationError(
          "PLUGIN_START_FAILED",
          `Plugin ${PluginId.toString(entry.pluginId)} sse transport missing url`,
        );
      }
      const mcpClient = this.deps.mcpClientFactory.createForSse(url, manifest.mcp.headers, automation);
      entry.mcpClient = mcpClient;
      await mcpClient.connect();
    }
  }

  /**
   * Register the plugin's automation emits with the shared registry so the
   * MCP automation handler can enforce emit ownership. Called on
   * `connectTransport` before the client is created.
   */
  private registerAutomationEmits(entry: RuntimeEntry, manifest: import("@nusashell/domain").PluginManifest): void {
    if (!this.deps.automationEmitRegistry) return;
    const pluginIdStr = PluginId.toString(entry.pluginId);
    try {
      this.deps.automationEmitRegistry.register(pluginIdStr, manifest.automation);
    } catch (error) {
      this.deps.logger?.warn(
        `Automation emit registration failed for ${pluginIdStr}: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
  }

  /**
   * Unregister the plugin's automation emits (e.g. on stop/uninstall).
   */
  unregisterAutomationEmits(entry: RuntimeEntry): void {
    if (!this.deps.automationEmitRegistry) return;
    const pluginIdStr = PluginId.toString(entry.pluginId);
    this.deps.automationEmitRegistry.unregister(pluginIdStr);
    this.deps.automationRateLimiter?.reset(pluginIdStr);
  }

  /**
   * Build the per-connection automation deps for the MCP client factory.
   * Returns `undefined` when automation infrastructure is not configured
   * (e.g. in tests that don't exercise the automation path).
   */
  private buildAutomationDeps(entry: RuntimeEntry): import("../ports/mcp-client.port.js").AutomationClientDeps | undefined {
    if (!this.deps.automationEmitRegistry || !this.deps.automationRateLimiter) {
      return undefined;
    }
    return {
      pluginId: PluginId.toString(entry.pluginId),
      eventDispatcher: this.deps.eventDispatcher,
      emitRegistry: this.deps.automationEmitRegistry,
      rateLimiter: this.deps.automationRateLimiter,
    };
  }

  /**
   * Transition the entry to "running" and publish the `PluginStartedEvent`.
   * Must be called after `connectTransport` and after the exit watcher is
   * registered (so there is no race window where an external kill is missed).
   */
  async transitionToRunning(entry: RuntimeEntry): Promise<void> {
    const transition = entry.runtime.transitionTo(
      "running",
      this.deps.clock.now(),
    );
    if (!transition.ok) {
      throw this.mapDomainError(transition.error, entry.pluginId);
    }
    entry.runtime = transition.value;
    await this.publishPulled(entry.runtime);

    const startedEvent = PluginStartedEvent.create(
      entry.pluginId,
      this.deps.clock.now(),
      entry.mcpClient?.pid ?? entry.process?.pid ?? null,
    );
    await this.deps.eventDispatcher.publish(startedEvent);
  }

  requireRunningClient(entry: RuntimeEntry): McpClientPort {
    if (!entry.mcpClient || entry.runtime.state !== "running") {
      throw new ApplicationError(
        "PLUGIN_NOT_RUNNING",
        `Plugin ${PluginId.toString(entry.pluginId)} is not running`,
        { pluginId: PluginId.toString(entry.pluginId) },
      );
    }
    return entry.mcpClient;
  }

  async syncWorkspaceLocked(entry: RuntimeEntry, workspace: string): Promise<WorkspaceSyncResult> {
    const previous = entry.workspace;
    entry.workspace = workspace;
    if (entry.runtime.state !== "running" || !entry.mcpClient) {
      return { mode: "idle", respawned: false };
    }
    const client = entry.mcpClient;
    if (client.rootsRequested?.()) {
      await this.applyRoots(entry, workspace);
      if (entry.lastRootsWorkspace !== workspace) {
        entry.lastRootsWorkspace = workspace;
        try {
          await client.notifyRootsChanged?.();
        } catch (error) {
          this.deps.logger?.warn(`roots/list_changed notify failed for ${PluginId.toString(entry.pluginId)}: ${error instanceof Error ? error.message : String(error)}`);
        }
      }
      return { mode: "roots", respawned: false };
    }
    void previous;
    return { mode: "static", respawned: false };
  }

  async applyRoots(entry: RuntimeEntry, workspace: string): Promise<void> {
    const client = entry.mcpClient;
    if (!client?.setRoots) return;
    const roots: RootDescriptor[] = [{ uri: pathToFileURL(resolve(workspace)).href, name: "workspace" }];
    client.setRoots(roots);
  }

  async publishPulled(runtime: PluginRuntime): Promise<void> {
    const events = runtime.pullEvents() as readonly DomainEvent[];
    await this.deps.eventDispatcher.publishAll(events);
  }

  private mapDomainError(error: import("@nusashell/domain").DomainError, pluginId: PluginId): ApplicationError {
    const id = PluginId.toString(pluginId);
    switch (error.code) {
      case "PLUGIN_NOT_FOUND":
        return new ApplicationError("PLUGIN_NOT_FOUND", error.message, { pluginId: id });
      case "PLUGIN_DISABLED":
        return new ApplicationError("PLUGIN_DISABLED", error.message, { pluginId: id });
      case "INVALID_RUNTIME_TRANSITION":
        return new ApplicationError("INVALID_RUNTIME_TRANSITION", error.message, error.details);
      case "TOOL_NOT_FOUND":
        return new ApplicationError("TOOL_NOT_FOUND", error.message, error.details);
      case "TOOL_CALL_TIMEOUT":
        return new ApplicationError("TOOL_CALL_TIMEOUT", error.message, error.details);
      case "VALIDATION_ERROR":
        return new ApplicationError("INTERNAL_ERROR", error.message, error.details);
      default:
        return new ApplicationError("INTERNAL_ERROR", error.message, { pluginId: id });
    }
  }
}
