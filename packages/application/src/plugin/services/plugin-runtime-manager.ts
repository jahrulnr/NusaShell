import {
  DomainError,
  type DomainEvent,
  Plugin,
  PluginCrashedEvent,
  PluginId,
  PluginLifecyclePolicy,
  PluginRuntime,
  type PluginRuntimeState,
  PluginStartedEvent,
  PluginStoppedEvent,
  RequestId,
  RuntimeTransitionPolicy,
  ToolCall,
  ToolCallCompletedEvent,
  ToolName,
} from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { ClockPort } from "../ports/clock.port.js";
import type { LoggerPort } from "../ports/logger.port.js";
import type { McpClientFactoryPort, McpClientPort, ToolDescriptor } from "../ports/mcp-client.port.js";
import type { PluginProcessPort, ProcessHandle } from "../ports/plugin-process.port.js";
import type { PluginRepositoryPort } from "../ports/plugin-repository.port.js";
import { EventDispatcher } from "../../events/event-dispatcher.js";
import { PluginOperationQueue } from "./plugin-operation-queue.js";
import { resolveIcon } from "./icon-resolver.js";

interface PendingToolCall {
  readonly toolCall: ToolCall;
  readonly resolve: (value: unknown) => void;
  readonly reject: (error: unknown) => void;
  readonly timer: ReturnType<typeof setTimeout> | undefined;
}

interface RuntimeEntry {
  readonly pluginId: PluginId;
  name: string;
  version: string;
  icon: string;
  installPath: string;
  enabled: boolean;
  runtime: PluginRuntime;
  startPromise: Promise<void> | null;
  readonly queue: PluginOperationQueue;
  process: ProcessHandle | null;
  mcpClient: McpClientPort | null;
  readonly pendingCalls: Map<string, PendingToolCall>;
  restartCount: number;
}

export interface PluginRuntimeManagerDeps {
  readonly pluginRepository: PluginRepositoryPort;
  readonly processAdapter: PluginProcessPort;
  readonly mcpClientFactory: McpClientFactoryPort;
  readonly eventDispatcher: EventDispatcher;
  readonly clock: ClockPort;
  readonly logger?: LoggerPort;
  readonly startTimeoutMs?: number;
  readonly stopTimeoutMs?: number;
  readonly toolCallTimeoutMs?: number;
}

export interface StartPluginOptions {
  readonly args?: Readonly<Record<string, unknown>>;
}

export interface CallToolOptions {
  readonly requestId: string;
  readonly toolName: string;
  readonly args: Readonly<Record<string, unknown>>;
  readonly timeoutMs?: number;
}

export interface PluginView {
  readonly pluginId: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly installPath: string;
  readonly state: PluginRuntimeState;
  readonly enabled: boolean;
}

export class PluginRuntimeManager {
  private readonly runtimes = new Map<string, RuntimeEntry>();
  private readonly deps: PluginRuntimeManagerDeps;

  constructor(deps: PluginRuntimeManagerDeps) {
    this.deps = deps;
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
      };
    });
  }

  async getPluginState(pluginId: PluginId): Promise<PluginRuntimeState> {
    const entry = this.runtimes.get(PluginId.toString(pluginId));
    return entry?.runtime.state ?? "idle";
  }

  async startPlugin(pluginId: PluginId): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.startLocked(entry));
  }

  async stopPlugin(pluginId: PluginId): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.stopLocked(entry));
  }

  async callTool(
    pluginId: PluginId,
    options: CallToolOptions,
  ): Promise<unknown> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => this.callToolLocked(entry, options));
  }

  async cancelTool(pluginId: PluginId, requestId: string): Promise<void> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => {
      this.cancelPendingCall(entry, requestId, "TOOL_CALL_CANCELLED", "Cancelled by client");
    });
  }

  async listTools(pluginId: PluginId): Promise<readonly ToolDescriptor[]> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => {
      if (!entry.mcpClient || entry.runtime.state !== "running") {
        throw new ApplicationError(
          "PLUGIN_NOT_RUNNING",
          `Plugin ${PluginId.toString(pluginId)} is not running`,
          { pluginId: PluginId.toString(pluginId) },
        );
      }
      return entry.mcpClient.listTools();
    });
  }

  async restartPlugin(pluginId: PluginId): Promise<PluginView> {
    const entry = await this.ensureEntry(pluginId);
    return entry.queue.enqueue(async () => {
      if (entry.runtime.state === "running" || entry.runtime.state === "starting") {
        await this.stopLocked(entry);
      }
      return this.startLocked(entry);
    });
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
      }
      return this.view(entry);
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
    };
  }

  async stopAll(): Promise<void> {
    const entries = [...this.runtimes.values()];
    await Promise.allSettled(
      entries.map((entry) =>
        entry.queue.enqueue(async () => this.stopLocked(entry)),
      ),
    );
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
      runtime: PluginRuntime.createIdle(pluginId),
      startPromise: null,
      queue: new PluginOperationQueue(),
      process: null,
      mcpClient: null,
      pendingCalls: new Map(),
      restartCount: 0,
    };
    this.runtimes.set(key, entry);
    return entry;
  }

  private async startLocked(entry: RuntimeEntry): Promise<PluginView> {
    if (entry.runtime.state === "running" || entry.runtime.state === "starting") {
      if (entry.startPromise) {
        await entry.startPromise;
      }
      return this.view(entry);
    }

    const plugin = await this.loadPlugin(entry.pluginId);
    entry.name = plugin.manifest.name;
    entry.version = plugin.manifest.version.toString();
    entry.icon = resolveIcon(plugin.manifest.icon, plugin.installPath);
    entry.installPath = plugin.installPath;
    entry.enabled = plugin.enabled;
    const canStart = PluginLifecyclePolicy.canStart(plugin, entry.runtime);
    if (!canStart.ok) {
      throw this.mapDomainError(canStart.error, entry.pluginId);
    }

    const transition = entry.runtime.transitionTo("starting", this.deps.clock.now());
    if (!transition.ok) {
      throw this.mapDomainError(transition.error, entry.pluginId);
    }
    entry.runtime = transition.value;
    await this.publishPulled(entry.runtime);

    entry.startPromise = this.doStart(entry, plugin);
    try {
      await entry.startPromise;
    } finally {
      entry.startPromise = null;
    }
    return this.view(entry);
  }

  private async doStart(entry: RuntimeEntry, plugin: Plugin): Promise<void> {
    try {
      const manifest = plugin.manifest;
      if (manifest.mcp.transport === "stdio") {
        const command = manifest.mcp.command;
        if (!command) {
          throw new ApplicationError(
            "PLUGIN_START_FAILED",
            `Plugin ${PluginId.toString(entry.pluginId)} stdio transport missing command`,
          );
        }

        const mcpClient = this.deps.mcpClientFactory.createForStdio(
          command,
          manifest.mcp.args,
          manifest.mcp.env,
          plugin.installPath,
        );
        await mcpClient.connect();
        entry.mcpClient = mcpClient;
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
      );
      await this.deps.eventDispatcher.publish(startedEvent);

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
          if (entry.runtime.state === "running") {
            void this.handleProcessExit(entry, -1);
          }
        });
      }
    } catch (error) {
      await this.crash(entry, this.describeError(error));
      throw this.toApplicationError(error, "PLUGIN_START_FAILED", entry.pluginId);
    }
  }

  private async stopLocked(entry: RuntimeEntry): Promise<PluginView> {
    if (
      entry.runtime.state === "idle" ||
      entry.runtime.state === "crashed" ||
      entry.runtime.state === "disabled"
    ) {
      return this.view(entry);
    }

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
    await this.publishPulled(entry.runtime);

    try {
      this.cancelPendingCalls(entry, "TOOL_CALL_CANCELLED", "Plugin stopping");
      if (entry.mcpClient) {
        await entry.mcpClient.close();
        entry.mcpClient = null;
      }
      if (entry.process) {
        await entry.process.kill();
        entry.process = null;
      }
    } catch (error) {
      await this.crash(entry, this.describeError(error));
      throw this.toApplicationError(error, "PLUGIN_STOP_FAILED", entry.pluginId);
    }

    const toIdle = entry.runtime.transitionTo("idle", this.deps.clock.now());
    if (toIdle.ok) {
      entry.runtime = toIdle.value;
      await this.publishPulled(entry.runtime);
    }

    const stoppedEvent = PluginStoppedEvent.create(
      entry.pluginId,
      this.deps.clock.now(),
    );
    await this.deps.eventDispatcher.publish(stoppedEvent);

    return this.view(entry);
  }

  private async callToolLocked(
    entry: RuntimeEntry,
    options: CallToolOptions,
  ): Promise<unknown> {
    const plugin = await this.loadPlugin(entry.pluginId);
    const canCall = PluginLifecyclePolicy.canCallTool(plugin, entry.runtime);
    if (!canCall.ok) {
      throw this.mapDomainError(canCall.error, entry.pluginId);
    }

    if (!entry.mcpClient) {
      throw new ApplicationError(
        "MCP_CONNECTION_FAILED",
        `Plugin ${PluginId.toString(entry.pluginId)} has no active MCP client`,
      );
    }

    const requestIdResult = RequestId.create(options.requestId);
    if (!requestIdResult.ok) {
      throw new ApplicationError(
        "INTERNAL_ERROR",
        `Invalid request id: ${requestIdResult.error.message}`,
      );
    }
    const toolNameResult = ToolName.create(options.toolName);
    if (!toolNameResult.ok) {
      throw new ApplicationError(
        "TOOL_NOT_FOUND",
        `Invalid tool name: ${toolNameResult.error.message}`,
      );
    }

    const toolCall = ToolCall.createPending({
      requestId: requestIdResult.value,
      pluginId: entry.pluginId,
      toolName: toolNameResult.value,
      args: options.args,
    });

    const timeoutMs = options.timeoutMs ?? this.deps.toolCallTimeoutMs ?? 30_000;

    return new Promise<unknown>((resolve, reject) => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      if (timeoutMs > 0) {
        timer = setTimeout(() => {
          this.cancelPendingCall(
            entry,
            RequestId.toString(toolCall.requestId),
            "TOOL_CALL_TIMEOUT",
            `Tool call timed out after ${timeoutMs}ms`,
          );
        }, timeoutMs);
      }

      const pending: PendingToolCall = {
        toolCall,
        resolve: (value) => {
          if (timer) clearTimeout(timer);
          resolve(value);
        },
        reject: (error) => {
          if (timer) clearTimeout(timer);
          reject(error);
        },
        timer,
      };
      entry.pendingCalls.set(RequestId.toString(toolCall.requestId), pending);

      entry.mcpClient!
        .callTool(options.toolName, options.args)
        .then((result) => {
          if (entry.pendingCalls.delete(RequestId.toString(toolCall.requestId))) {
            const completed = toolCall.withStatus("completed", result);
            void this.publishToolCompleted(entry, completed);
            pending.resolve(result);
          }
        })
        .catch((error) => {
          if (entry.pendingCalls.delete(RequestId.toString(toolCall.requestId))) {
            const failed = toolCall.withStatus("failed");
            void this.publishToolCompleted(entry, failed);
            pending.reject(error);
          }
        });
    });
  }

  private async handleProcessExit(entry: RuntimeEntry, code: number): Promise<void> {
    if (
      entry.runtime.state === "stopping" ||
      entry.runtime.state === "idle"
    ) {
      return;
    }
    await this.crash(entry, `Process exited with code ${code}`);
  }

  private async crash(entry: RuntimeEntry, reason: string): Promise<void> {
    this.cancelPendingCalls(entry, "PLUGIN_CRASHED", reason);
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
      await this.publishPulled(entry.runtime);
    }

    entry.restartCount += 1;
    const crashedEvent = PluginCrashedEvent.create(
      entry.pluginId,
      reason,
      this.deps.clock.now(),
    );
    await this.deps.eventDispatcher.publish(crashedEvent);
  }

  private cancelPendingCalls(
    entry: RuntimeEntry,
    code: "TOOL_CALL_CANCELLED" | "PLUGIN_CRASHED",
    reason: string,
  ): void {
    for (const [id, pending] of entry.pendingCalls) {
      entry.pendingCalls.delete(id);
      if (pending.timer) clearTimeout(pending.timer);
      pending.reject(
        new ApplicationError(code, reason, {
          requestId: id,
          pluginId: PluginId.toString(entry.pluginId),
        }),
      );
    }
  }

  private cancelPendingCall(
    entry: RuntimeEntry,
    requestId: string,
    code: "TOOL_CALL_TIMEOUT" | "TOOL_CALL_CANCELLED",
    reason: string,
  ): void {
    const pending = entry.pendingCalls.get(requestId);
    if (!pending) return;
    entry.pendingCalls.delete(requestId);
    if (pending.timer) clearTimeout(pending.timer);
    pending.reject(
      new ApplicationError(code, reason, {
        requestId,
        pluginId: PluginId.toString(entry.pluginId),
      }),
    );
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

  private async publishPulled(runtime: PluginRuntime): Promise<void> {
    const events = runtime.pullEvents() as readonly DomainEvent[];
    await this.deps.eventDispatcher.publishAll(events);
  }

  private async publishToolCompleted(
    entry: RuntimeEntry,
    toolCall: ToolCall,
  ): Promise<void> {
    const event = ToolCallCompletedEvent.create(
      entry.pluginId,
      toolCall.requestId,
      toolCall.toolName,
      this.deps.clock.now(),
    );
    await this.deps.eventDispatcher.publish(event);
  }

  private view(entry: RuntimeEntry): PluginView {
    return {
      pluginId: PluginId.toString(entry.pluginId),
      name: entry.name,
      version: entry.version,
      icon: entry.icon,
      installPath: entry.installPath,
      state: entry.runtime.state,
      enabled: entry.enabled,
    };
  }

  private mapDomainError(error: DomainError, pluginId: PluginId): ApplicationError {
    const id = PluginId.toString(pluginId);
    switch (error.code) {
      case "PLUGIN_NOT_FOUND":
        return new ApplicationError("PLUGIN_NOT_FOUND", error.message, { pluginId: id });
      case "PLUGIN_DISABLED":
        return new ApplicationError("PLUGIN_DISABLED", error.message, { pluginId: id });
      case "INVALID_RUNTIME_TRANSITION":
        return new ApplicationError(
          "INVALID_RUNTIME_TRANSITION",
          error.message,
          error.details,
        );
      case "TOOL_NOT_FOUND":
        return new ApplicationError("TOOL_NOT_FOUND", error.message, error.details);
      case "TOOL_CALL_TIMEOUT":
        return new ApplicationError("TOOL_CALL_TIMEOUT", error.message, error.details);
      case "VALIDATION_ERROR":
        return new ApplicationError("INTERNAL_ERROR", error.message, error.details);
    }
  }

  private toApplicationError(
    error: unknown,
    fallbackCode: "PLUGIN_START_FAILED" | "PLUGIN_STOP_FAILED",
    pluginId: PluginId,
  ): ApplicationError {
    if (error instanceof ApplicationError) {
      return error;
    }
    const message = error instanceof Error ? error.message : String(error);
    return new ApplicationError(fallbackCode, message, {
      pluginId: PluginId.toString(pluginId),
    });
  }

  private describeError(error: unknown): string {
    if (error instanceof Error) return error.message;
    return String(error);
  }
}

