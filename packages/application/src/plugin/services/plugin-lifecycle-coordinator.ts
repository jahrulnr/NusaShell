import {
  PluginId,
  PluginLifecyclePolicy,
  PluginCrashedEvent,
  PluginStoppedEvent,
  RuntimeTransitionPolicy,
} from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { PluginRuntimeManagerDeps } from "./plugin-runtime-manager.js";
import type { RuntimeEntry, PluginView } from "./plugin-runtime-types.js";
import { arrayEquals, hydrateEntryFromPlugin, recordEquals } from "./plugin-runtime-types.js";
import type { ToolCallTracker } from "./tool-call-tracker.js";
import type { McpSessionManager } from "./mcp-session-manager.js";
import { resolveIcon } from "./icon-resolver.js";

/**
 * Coordinates plugin lifecycle transitions: start, stop, restart, crash
 * recovery, and process-exit watching. Delegates tool call cleanup to
 * `ToolCallTracker` and MCP session creation/teardown to `McpSessionManager`.
 *
 * Stateless — receives `entry` per call. The `runtimes` map stays in the
 * `PluginRuntimeManager` facade (single SoT).
 */
export class PluginLifecycleCoordinator {
  constructor(
    private readonly deps: PluginRuntimeManagerDeps,
    private readonly tracker: ToolCallTracker,
    private readonly sessions: McpSessionManager,
  ) {}

  async startLocked(entry: RuntimeEntry): Promise<PluginView> {
    if (entry.runtime.state === "running" || entry.runtime.state === "starting") {
      if (this.launchSpecChanged(entry)) {
        this.deps.logger?.info("Respawning plugin %s for launchSpec override", PluginId.toString(entry.pluginId));
        await this.stopLocked(entry);
      } else {
        if (entry.startPromise) {
          await entry.startPromise;
        }
        return this.view(entry);
      }
    }

    const plugin = await this.loadPlugin(entry.pluginId);
    hydrateEntryFromPlugin(entry, plugin, resolveIcon);
    const canStart = PluginLifecyclePolicy.canStart(plugin, entry.runtime);
    if (!canStart.ok) {
      throw this.mapDomainError(canStart.error, entry.pluginId);
    }

    entry.startAborted = false;
    const transition = entry.runtime.transitionTo("starting", this.deps.clock.now());
    if (!transition.ok) {
      throw this.mapDomainError(transition.error, entry.pluginId);
    }
    entry.runtime = transition.value;
    await this.sessions.publishPulled(entry.runtime);

    entry.startPromise = this.doStart(entry, plugin);
    try {
      await entry.startPromise;
    } finally {
      entry.startPromise = null;
    }
    return this.view(entry);
  }

  async stopLocked(entry: RuntimeEntry): Promise<PluginView> {
    if (entry.runtime.state === "idle" || entry.runtime.state === "disabled") {
      return this.view(entry);
    }

    if (entry.runtime.state === "crashed") {
      const toIdle = entry.runtime.transitionTo("idle", this.deps.clock.now());
      if (!toIdle.ok) {
        throw this.mapDomainError(toIdle.error, entry.pluginId);
      }
      entry.runtime = toIdle.value;
      entry.startAborted = false;
      await this.sessions.publishPulled(entry.runtime);
      return this.view(entry);
    }

    entry.startAborted = entry.runtime.state === "starting" || entry.startAborted;

    const canStop = RuntimeTransitionPolicy.assertTransition(
      entry.runtime.state,
      "stopping",
    );
    if (!canStop.ok) {
      throw this.mapDomainError(canStop.error, entry.pluginId);
    }

    const transition = entry.runtime.transitionTo("stopping", this.deps.clock.now());
    if (!transition.ok) {
      throw this.mapDomainError(transition.error, entry.pluginId);
    }
    entry.runtime = transition.value;
    await this.sessions.publishPulled(entry.runtime);

    try {
      this.tracker.cancelPendingCalls(entry, "TOOL_CALL_CANCELLED", "Plugin stopping");
      if (entry.mcpClient) {
        await entry.mcpClient.close();
        entry.mcpClient = null;
      }
      if (entry.process) {
        await entry.process.kill();
        entry.process = null;
      }
      this.sessions.unregisterAutomationEmits(entry);
    } catch (error) {
      await this.crash(entry, this.describeError(error));
      throw this.toApplicationError(error, "PLUGIN_STOP_FAILED", entry.pluginId);
    }

    const toIdle = entry.runtime.transitionTo("idle", this.deps.clock.now());
    if (toIdle.ok) {
      entry.runtime = toIdle.value;
      await this.sessions.publishPulled(entry.runtime);
    }
    entry.startAborted = false;

    const stoppedEvent = PluginStoppedEvent.create(
      entry.pluginId,
      this.deps.clock.now(),
    );
    await this.deps.eventDispatcher.publish(stoppedEvent);

    return this.view(entry);
  }

  launchSpecChanged(entry: RuntimeEntry): boolean {
    if (entry.launchArgs !== undefined && !arrayEquals(entry.launchArgs, entry.runningArgs ?? [])) return true;
    if (entry.launchEnv !== undefined && !recordEquals(entry.launchEnv, entry.runningEnv ?? {})) return true;
    return false;
  }

  /**
   * Registers the process/MCP close watcher that flips a "running" plugin to
   * "crashed" when the underlying process dies outside NusaShell's stop path
   * (finding 2). Registered before the "running" transition in `doStart` so
   * there is no race window where an external kill is missed.
   */
  registerExitWatcher(entry: RuntimeEntry): void {
    if (entry.process) {
      entry.process.exited
        .then((code) => {
          void this.handleProcessExit(entry, code);
        })
        .catch(() => {
          void this.handleProcessExit(entry, -1);
        });
    } else if (entry.mcpClient && entry.mcpClient.onClose) {
      entry.mcpClient.onClose(() => {
        void this.handleProcessExit(entry, -1);
      });
    }
  }

  async handleProcessExit(entry: RuntimeEntry, code: number): Promise<void> {
    if (
      entry.runtime.state === "stopping" ||
      entry.runtime.state === "idle"
    ) {
      return;
    }
    this.deps.logger?.warn("Plugin process exited unexpectedly plugin=%s code=%d state=%s", PluginId.toString(entry.pluginId), code, entry.runtime.state);
    await this.crash(entry, `Process exited with code ${code}`);
  }

  async crash(entry: RuntimeEntry, reason: string): Promise<void> {
    this.tracker.cancelPendingCalls(entry, "PLUGIN_CRASHED", reason);
    entry.process = null;
    if (entry.mcpClient) {
      try {
        await entry.mcpClient.close();
      } catch (err) {
        this.deps.logger?.warn("Failed to close MCP client on crash: %s", err);
      }
      entry.mcpClient = null;
    }

    const transition = entry.runtime.transitionTo("crashed", this.deps.clock.now());
    if (transition.ok) {
      entry.runtime = transition.value;
      await this.sessions.publishPulled(entry.runtime);
    }

    entry.restartCount += 1;
    const crashedEvent = PluginCrashedEvent.create(
      entry.pluginId,
      reason,
      this.deps.clock.now(),
    );
    await this.deps.eventDispatcher.publish(crashedEvent);
  }

  view(entry: RuntimeEntry): PluginView {
    return {
      pluginId: PluginId.toString(entry.pluginId),
      name: entry.name,
      version: entry.version,
      icon: entry.icon,
      installPath: entry.installPath,
      source: entry.source ?? "package",
      transport: entry.transport ?? "stdio",
      ...(entry.category !== undefined ? { category: entry.category } : {}),
      state: entry.runtime.state,
      enabled: entry.enabled,
      autostart: entry.autostart,
      ui: entry.ui,
      keepAliveOnClose: entry.keepAliveOnClose,
      ...(entry.command !== undefined ? { command: entry.command } : {}),
      ...(entry.args.length > 0 ? { args: entry.args } : {}),
      ...(entry.url !== undefined ? { url: entry.url } : {}),
      ...(Object.keys(entry.env).length > 0 ? { env: entry.env } : {}),
      ...(Object.keys(entry.headers).length > 0 ? { headers: entry.headers } : {}),
      ...(entry.automation !== undefined ? { automation: entry.automation } : {}),
    };
  }

  private async doStart(entry: RuntimeEntry, plugin: import("@nusashell/domain").Plugin): Promise<void> {
    try {
      if (entry.startAborted || entry.runtime.state === "stopping" || entry.runtime.state === "idle") {
        return;
      }
      // 1. Connect the MCP transport (stdio/http/sse)
      await this.sessions.connectTransport(entry, plugin);
      if (entry.startAborted || entry.runtime.state === "stopping" || entry.runtime.state === "idle") {
        if (entry.mcpClient) {
          await entry.mcpClient.close().catch(() => {});
          entry.mcpClient = null;
        }
        return;
      }
      // 2. Register the close/exit watcher BEFORE transitioning to "running"
      //    so an external kill between connect and the running transition
      //    cannot leave the SoT stuck on "running" with no observer (finding 2).
      this.registerExitWatcher(entry);
      // 3. Transition to "running" and publish PluginStartedEvent
      await this.sessions.transitionToRunning(entry);
    } catch (error) {
      if (entry.startAborted || entry.runtime.state === "stopping" || entry.runtime.state === "idle") {
        entry.mcpClient = null;
        entry.process = null;
        return;
      }
      this.deps.logger?.error("doStart failed for plugin %s: %s", PluginId.toString(entry.pluginId), String(error));
      await this.crash(entry, this.describeError(error));
      throw this.toApplicationError(error, "PLUGIN_START_FAILED", entry.pluginId);
    }
  }

  private async loadPlugin(pluginId: PluginId): Promise<import("@nusashell/domain").Plugin> {
    const plugin = await this.deps.pluginRepository.findById(pluginId);
    if (!plugin) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Plugin ${PluginId.toString(pluginId)} was not found`,
        { pluginId: PluginId.toString(pluginId) },
      );
    }
    return plugin;
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

  private toApplicationError(
    error: unknown,
    fallbackCode: "PLUGIN_START_FAILED" | "PLUGIN_STOP_FAILED",
    pluginId: PluginId,
  ): ApplicationError {
    if (error instanceof ApplicationError) return error;
    if (error instanceof Error) {
      return new ApplicationError(fallbackCode, error.message, {
        pluginId: PluginId.toString(pluginId),
      });
    }
    return new ApplicationError(fallbackCode, String(error), {
      pluginId: PluginId.toString(pluginId),
    });
  }

  private describeError(error: unknown): string {
    if (error instanceof Error) return error.message;
    return String(error);
  }
}
