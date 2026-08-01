import {
  PluginId,
  RequestId,
  ToolCall,
  ToolCallCompletedEvent,
  ToolName,
} from "@nusashell/domain";
import { ApplicationError } from "../../errors/application-error.js";
import type { ClockPort } from "../ports/clock.port.js";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import type { RuntimeEntry, PendingToolCall, CallToolOptions } from "./plugin-runtime-types.js";
import type { PluginRuntimeManagerDeps } from "./plugin-runtime-manager.js";

/**
 * Tracks in-flight tool calls per plugin: dispatches calls to the MCP client,
 * manages timeouts, cancels pending calls on stop/crash, and publishes
 * `ToolCallCompletedEvent` when a call settles.
 *
 * Stateless — receives `entry` per call. The `runtimes` map stays in the
 * `PluginRuntimeManager` facade (single SoT).
 */
export class ToolCallTracker {
  constructor(
    private readonly deps: PluginRuntimeManagerDeps,
  ) {}

  async callToolLocked(entry: RuntimeEntry, options: CallToolOptions): Promise<unknown> {
    const plugin = await this.loadPlugin(entry.pluginId);
    const { PluginLifecyclePolicy } = await import("@nusashell/domain");
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

  cancelPendingCalls(
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

  cancelPendingCall(
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

  async publishToolCompleted(
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
}
