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
      const mcpClient = this.deps.mcpClientFactory.createForStdio(
        command,
        args,
        environment,
        plugin.installPath,
      );
      this.deps.logger?.debug("Starting MCP stdio process: command=%s args=%j cwd=%s workspace=%s", command, args, plugin.installPath, entry.workspace ?? "(none)");
      await mcpClient.connect();
      this.deps.logger?.info("MCP client connected (stdio) for plugin %s", PluginId.toString(entry.pluginId));
      entry.mcpClient = mcpClient;
      entry.runningArgs = args;
      entry.runningEnv = environment;
      if (entry.workspace) {
        try {
          await this.applyRoots(entry, entry.workspace);
        } catch (error) {
          this.deps.logger?.warn("Initial roots apply failed for %s: %s", PluginId.toString(entry.pluginId), error instanceof Error ? error.message : String(error));
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
      const mcpClient = this.deps.mcpClientFactory.createForHttp(url);
      await mcpClient.connect();
      entry.mcpClient = mcpClient;
    } else if (manifest.mcp.transport === "sse") {
      const url = manifest.mcp.url;
      if (!url) {
        throw new ApplicationError(
          "PLUGIN_START_FAILED",
          `Plugin ${PluginId.toString(entry.pluginId)} sse transport missing url`,
        );
      }
      const mcpClient = this.deps.mcpClientFactory.createForSse(url);
      await mcpClient.connect();
      entry.mcpClient = mcpClient;
    }
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
          this.deps.logger?.warn("roots/list_changed notify failed for %s: %s", PluginId.toString(entry.pluginId), error instanceof Error ? error.message : String(error));
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
