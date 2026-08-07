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
import { AutomationEmitRegistry } from "./automation-emit-registry.js";
import type {
  RuntimeEntry,
  StartPluginOptions,
  WorkspaceSyncResult,
  PluginLaunchSpec,
  CallToolOptions,
  PluginView,
} from "./plugin-runtime-types.js";
import { hydrateEntryFromPlugin } from "./plugin-runtime-types.js";
import type { AutomationRateLimiterPort } from "../ports/mcp-client.port.js";

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
  readonly automationEmitRegistry?: AutomationEmitRegistry;
  readonly automationRateLimiter?: AutomationRateLimiterPort;
  /**
   * Auto-restart of a plugin that crashed unexpectedly (process exit / close
   * outside the intentional stop path), with exponential backoff and a
   * circuit breaker (max restarts within a window). Disable per-test or per
   * product config by passing `{ enabled: false }`.
   */
  readonly autoRestart?: {
    readonly enabled?: boolean;
    readonly maxRestarts?: number;
    readonly windowMs?: number;
    readonly baseDelayMs?: number;
    readonly maxDelayMs?: number;
  };
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
        source: plugin.manifest.source,
        transport: plugin.manifest.mcp.transport,
        ...(plugin.manifest.category !== undefined ? { category: plugin.manifest.category } : {}),
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
      // Empty args must not override the manifest (hangs `node` with no script).
      if (options.args !== undefined) {
        entry.launchArgs = options.args.length > 0 ? options.args : undefined;
      }
      if (options.env !== undefined) {
        entry.launchEnv = Object.keys(options.env).length > 0 ? options.env : undefined;
      }
      if (options.workspace !== undefined) entry.workspace = options.workspace;
    }
    return entry.queue.enqueue(async () => this.lifecycle.startLocked(entry));
  }

  async stopPlugin(pluginId: PluginId): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    // Abort a hung connect immediately so stop is not stuck behind the
    // connect timeout on the serial queue.
    if (entry.runtime.state === "starting") {
      entry.startAborted = true;
      if (entry.mcpClient) {
        void entry.mcpClient.close().catch(() => {});
      }
    }
    return entry.queue.enqueue(async () => this.lifecycle.stopLocked(entry));
  }

  async callTool(
    pluginId: PluginId,
    options: CallToolOptions,
    signal?: AbortSignal,
  ): Promise<unknown> {
    const entry = await this.ensureEntry(pluginId);
    const mergedOptions = signal ? { ...options, signal } : options;
    // Tool calls are read-heavy and independent per requestId: run them
    // concurrently so one slow call does not block another call to the same
    // plugin (ticket #1). Lifecycle ops stay exclusive — runtime state + the
    // tracker's cancel-on-stop keep them mutually exclusive.
    return entry.queue.enqueueConcurrent(async () => this.tracker.callToolLocked(entry, mergedOptions));
  }

  async cancelTool(pluginId: PluginId, requestId: string): Promise<void> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueueConcurrent(async () => {
      this.tracker.cancelPendingCall(entry, requestId, "TOOL_CALL_CANCELLED", "Cancelled by client");
    });
  }

  async listTools(pluginId: PluginId): Promise<readonly ToolDescriptor[]> {
    const entry = await this.ensureEntry(pluginId);
    // Serve the cached catalog when available (round-trip JSON-RPC avoided).
    // The cache is set lazily on first fetch and invalidated on restart /
    // stop / crash / `notifications/tools/list_changed`.
    if (entry.cachedTools !== null) {
      return entry.cachedTools;
    }
    const tools = await entry.queue.enqueue(async () =>
      this.sessions.requireRunningClient(entry).listTools());
    entry.cachedTools = tools;
    return tools;
  }

  /**
   * Invalidate the cached tool catalog for a plugin so the next `listTools`
   * re-fetches from the MCP server. Called on lifecycle transitions and on
   * MCP `notifications/tools/list_changed`.
   */
  invalidateToolsCache(pluginId: PluginId): void {
    const entry = this.runtimes.get(PluginId.toString(pluginId));
    if (entry) entry.cachedTools = null;
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
      source: manifest.source,
      transport: manifest.mcp.transport,
      ...(manifest.mcp.command !== undefined ? { command: manifest.mcp.command } : {}),
      ...(manifest.mcp.url !== undefined ? { url: manifest.mcp.url } : {}),
      headerKeys: Object.keys(manifest.mcp.headers),
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
      const plugin = await this.deps.pluginRepository.findById(pluginId);
      if (plugin) {
        hydrateEntryFromPlugin(entry, plugin, resolveIcon);
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
      source: plugin.manifest.source,
      transport: plugin.manifest.mcp.transport,
      keepAliveOnClose: plugin.manifest.mcp.keepAliveOnClose,
      ...(plugin.manifest.category !== undefined ? { category: plugin.manifest.category } : {}),
      ...(plugin.manifest.mcp.command !== undefined ? { command: plugin.manifest.mcp.command } : {}),
      ...(plugin.manifest.mcp.args.length > 0 ? { args: plugin.manifest.mcp.args } : {}),
      ...(plugin.manifest.mcp.url !== undefined ? { url: plugin.manifest.mcp.url } : {}),
      ...(Object.keys(plugin.manifest.mcp.env).length > 0 ? { env: plugin.manifest.mcp.env } : {}),
      ...(Object.keys(plugin.manifest.mcp.headers).length > 0 ? { headers: plugin.manifest.mcp.headers } : {}),
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
      source: "package",
      transport: "stdio",
      command: undefined,
      args: [],
      url: undefined,
      env: {},
      headers: {},
      enabled: true,
      autostart: false,
      keepAliveOnClose: false,
      runtime: PluginRuntime.createIdle(pluginId),
      startPromise: null,
      startAborted: false,
      queue: new PluginOperationQueue(),
      process: null,
      mcpClient: null,
      pendingCalls: new Map(),
      restartCount: 0,
      restarting: false,
      restartWindowStartAt: 0,
      restartTimer: null,
      lastCrashReason: undefined,
      cachedTools: null,
      workspace: undefined,
      lastRootsWorkspace: undefined,
      launchArgs: undefined,
      launchEnv: undefined,
      runningArgs: undefined,
      runningEnv: undefined,
      automation: undefined,
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
