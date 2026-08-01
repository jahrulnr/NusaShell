import {
  Plugin,
  PluginId,
  PluginRuntime,
  type PluginRuntimeState,
} from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { ClockPort } from "../ports/clock.port.js";
import type { LoggerPort } from "../ports/logger.port.js";
import type {
  McpClientFactoryPort,
  CompletionReference,
  CompletionResult,
  PromptDescriptor,
  PromptResult,
  ResourceDescriptor,
  ResourceReadResult,
  ResourceTemplateDescriptor,
  ToolDescriptor,
} from "../ports/mcp-client.port.js";
import type { PluginProcessPort } from "../ports/plugin-process.port.js";
import type { PluginRepositoryPort } from "../ports/plugin-repository.port.js";
import { EventDispatcher } from "../../events/event-dispatcher.js";
import { PluginOperationQueue } from "./plugin-operation-queue.js";
import { resolveIcon } from "./icon-resolver.js";
import { ToolCallTracker } from "./tool-call-tracker.js";
import { McpSessionManager } from "./mcp-session-manager.js";
import { PluginLifecycleCoordinator } from "./plugin-lifecycle-coordinator.js";
import type {
  RuntimeEntry,
  StartPluginOptions,
  WorkspaceSyncResult,
  PluginLaunchSpec,
  CallToolOptions,
  PluginView,
} from "./plugin-runtime-types.js";

export interface PluginRuntimeManagerDeps {
  readonly pluginRepository: PluginRepositoryPort;
  readonly processAdapter: PluginProcessPort;
  readonly mcpClientFactory: McpClientFactoryPort;
  readonly eventDispatcher: EventDispatcher;
  readonly clock: ClockPort;
  readonly logger?: LoggerPort;
  readonly resolveRuntimeEnvironment?: (
    pluginId: string,
  ) => Promise<Readonly<Record<string, string>>> | Readonly<Record<string, string>>;
  readonly startTimeoutMs?: number;
  readonly stopTimeoutMs?: number;
  readonly toolCallTimeoutMs?: number;
}

export type {
  StartPluginOptions,
  WorkspaceSyncResult,
  PluginLaunchSpec,
  CallToolOptions,
  PluginView,
} from "./plugin-runtime-types.js";

/**
 * Facade for plugin runtime management. Holds the single `runtimes` map
 * (SoT) and delegates to three focused sub-modules:
 *
 * - `PluginLifecycleCoordinator` — start/stop/restart/crash/exit-watcher
 * - `McpSessionManager` — MCP transport creation, workspace roots, discovery
 * - `ToolCallTracker` — in-flight tool calls, timeouts, completion events
 *
 * The facade keeps the public API stable; callers (IPC, WS gateway, agent
 * turn runner) see no change.
 */
export class PluginRuntimeManager {
  private readonly runtimes = new Map<string, RuntimeEntry>();
  private readonly deps: PluginRuntimeManagerDeps;
  private readonly tracker: ToolCallTracker;
  private readonly sessions: McpSessionManager;
  private readonly lifecycle: PluginLifecycleCoordinator;

  constructor(deps: PluginRuntimeManagerDeps) {
    this.deps = deps;
    this.tracker = new ToolCallTracker(deps);
    this.sessions = new McpSessionManager(deps);
    this.lifecycle = new PluginLifecycleCoordinator(deps, this.tracker, this.sessions);
  }

  async listPlugins(): Promise<readonly PluginView[]> {
    const plugins = await this.deps.pluginRepository.list();
    return plugins.map((plugin) => {
      const entry = this.runtimes.get(PluginId.toString(plugin.id));
      return {
        pluginId: PluginId.toString(plugin.id),
        name: plugin.manifest.name,
        version: plugin.manifest.version.toString(),
        icon: resolveIcon(plugin.manifest.icon, plugin.installPath),
        installPath: plugin.installPath,
        state: entry?.runtime.state ?? "idle",
        enabled: plugin.enabled,
        autostart: plugin.manifest.mcp.autostart,
        ui: plugin.manifest.ui,
        keepAliveOnClose: plugin.manifest.mcp.keepAliveOnClose,
      };
    });
  }

  async getPluginState(pluginId: PluginId): Promise<PluginRuntimeState> {
    const entry = this.runtimes.get(PluginId.toString(pluginId));
    return entry?.runtime.state ?? "idle";
  }

  async startPlugin(pluginId: PluginId, options?: StartPluginOptions): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    if (options) {
      entry.launchArgs = options.args;
      entry.launchEnv = options.env;
      if (options.workspace !== undefined) entry.workspace = options.workspace;
    }
    return entry.queue.enqueue(async () => this.lifecycle.startLocked(entry));
  }

  async stopPlugin(pluginId: PluginId): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.lifecycle.stopLocked(entry));
  }

  async callTool(
    pluginId: PluginId,
    options: CallToolOptions,
  ): Promise<unknown> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.tracker.callToolLocked(entry, options));
  }

  async cancelTool(pluginId: PluginId, requestId: string): Promise<void> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => {
      this.tracker.cancelPendingCall(entry, requestId, "TOOL_CALL_CANCELLED", "Cancelled by client");
    });
  }

  async listTools(pluginId: PluginId): Promise<readonly ToolDescriptor[]> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.requireRunningClient(entry).listTools());
  }

  async listPrompts(pluginId: PluginId): Promise<readonly PromptDescriptor[]> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.requireRunningClient(entry).listPrompts());
  }

  async getPrompt(
    pluginId: PluginId,
    name: string,
    args: Readonly<Record<string, string>>,
  ): Promise<PromptResult> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.requireRunningClient(entry).getPrompt(name, args));
  }

  async listResources(pluginId: PluginId): Promise<readonly ResourceDescriptor[]> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.requireRunningClient(entry).listResources());
  }

  async listResourceTemplates(pluginId: PluginId): Promise<readonly ResourceTemplateDescriptor[]> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.requireRunningClient(entry).listResourceTemplates());
  }

  async readResource(pluginId: PluginId, uri: string): Promise<ResourceReadResult> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.requireRunningClient(entry).readResource(uri));
  }

  async complete(
    pluginId: PluginId,
    reference: CompletionReference,
    argument: { readonly name: string; readonly value: string },
    context?: { readonly arguments?: Readonly<Record<string, string>> },
  ): Promise<CompletionResult> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () =>
      this.sessions.requireRunningClient(entry).complete(reference, argument, context));
  }

  async restartPlugin(pluginId: PluginId): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => {
      if (entry.runtime.state === "running" || entry.runtime.state === "starting") {
        await this.lifecycle.stopLocked(entry);
      }
      return this.lifecycle.startLocked(entry);
    });
  }

  async syncWorkspace(pluginId: PluginId, workspace: string): Promise<WorkspaceSyncResult> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.sessions.syncWorkspaceLocked(entry, workspace));
  }

  async getLaunchSpec(pluginId: PluginId): Promise<PluginLaunchSpec | null> {
    const key = PluginId.toString(pluginId);
    const entry = this.runtimes.get(key);
    if (!entry) return null;
    const plugin = await this.loadPlugin(pluginId);
    const manifest = plugin.manifest;
    const envKeys = Object.keys({ ...manifest.mcp.env, ...(entry.launchEnv ?? {}) });
    return {
      pluginId: key,
      transport: manifest.mcp.transport,
      ...(manifest.mcp.command !== undefined ? { command: manifest.mcp.command } : {}),
      args: entry.launchArgs ?? manifest.mcp.args,
      envKeys,
      ...(entry.workspace ? { workspace: entry.workspace } : {}),
      rootsCapable: entry.mcpClient?.rootsRequested?.() ?? false,
    };
  }

  async getPlugin(pluginId: PluginId): Promise<PluginView | null> {
    const key = PluginId.toString(pluginId);
    const entry = this.runtimes.get(key);
    if (entry) {
      if (!entry.name) {
        const plugin = await this.loadPlugin(pluginId);
        entry.name = plugin.manifest.name;
        entry.version = plugin.manifest.version.toString();
        entry.icon = resolveIcon(plugin.manifest.icon, plugin.installPath);
        entry.installPath = plugin.installPath;
        entry.enabled = plugin.enabled;
        entry.autostart = plugin.manifest.mcp.autostart;
        entry.ui = plugin.manifest.ui;
        entry.keepAliveOnClose = plugin.manifest.mcp.keepAliveOnClose;
      }
      return this.lifecycle.view(entry);
    }
    const plugin = await this.deps.pluginRepository.findById(pluginId);
    if (!plugin) return null;
    return {
      pluginId: key,
      name: plugin.manifest.name,
      version: plugin.manifest.version.toString(),
      icon: resolveIcon(plugin.manifest.icon, plugin.installPath),
      installPath: plugin.installPath,
      state: "idle",
      enabled: plugin.enabled,
      autostart: plugin.manifest.mcp.autostart,
      ui: plugin.manifest.ui,
      keepAliveOnClose: plugin.manifest.mcp.keepAliveOnClose,
    };
  }

  async stopAll(): Promise<void> {
    const entries = [...this.runtimes.values()];
    await Promise.allSettled(
      entries.map((entry) =>
        entry.queue.enqueue(async () => this.lifecycle.stopLocked(entry)),
      ),
    );
  }

  async removePlugin(pluginId: PluginId): Promise<void> {
    const key = PluginId.toString(pluginId);
    const entry = this.runtimes.get(key);
    if (entry) {
      await entry.queue.enqueue(async () => this.lifecycle.stopLocked(entry));
      this.runtimes.delete(key);
    }
  }

  async setAutostart(pluginId: PluginId, autostart: boolean): Promise<PluginView> {
    const plugin = await this.loadPlugin(pluginId);
    await this.deps.pluginRepository.save(plugin.withMcpAutostart(autostart));
    const entry = this.runtimes.get(PluginId.toString(pluginId));
    if (entry) entry.autostart = autostart;
    return (await this.getPlugin(pluginId))!;
  }

  async startAutostartPlugins(): Promise<void> {
    const plugins = await this.deps.pluginRepository.list();
    await Promise.allSettled(plugins.filter((plugin) => plugin.enabled && plugin.manifest.mcp.autostart).map((plugin) => this.startPlugin(plugin.id)));
  }

  private async ensureEntry(pluginId: PluginId): Promise<RuntimeEntry> {
    const key = PluginId.toString(pluginId);
    const existing = this.runtimes.get(key);
    if (existing) {
      return existing;
    }
    const entry: RuntimeEntry = {
      pluginId,
      name: "",
      version: "",
      icon: "",
      installPath: "",
      enabled: true,
      autostart: false,
      keepAliveOnClose: false,
      runtime: PluginRuntime.createIdle(pluginId),
      startPromise: null,
      queue: new PluginOperationQueue(),
      process: null,
      mcpClient: null,
      pendingCalls: new Map(),
      restartCount: 0,
      workspace: undefined,
      lastRootsWorkspace: undefined,
      launchArgs: undefined,
      launchEnv: undefined,
      runningArgs: undefined,
      runningEnv: undefined,
    };
    this.runtimes.set(key, entry);
    return entry;
  }

  private async loadPlugin(pluginId: PluginId): Promise<Plugin> {
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
}
