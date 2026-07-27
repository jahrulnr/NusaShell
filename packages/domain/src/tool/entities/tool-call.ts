import type { PluginId } from "../../plugin/value-objects/plugin-id.js";
import type { RequestId } from "../value-objects/request-id.js";
import type { ToolName } from "../value-objects/tool-name.js";

export type ToolCallStatus =
  | "pending"
  | "completed"
  | "failed"
  | "cancelled"
  | "timed_out";

export interface CreateToolCallInput {
  readonly requestId: RequestId;
  readonly pluginId: PluginId;
  readonly toolName: ToolName;
  readonly args: Readonly<Record<string, unknown>>;
}

export class ToolCall {
  private constructor(
    readonly requestId: RequestId,
    readonly pluginId: PluginId,
    readonly toolName: ToolName,
    readonly status: ToolCallStatus,
    readonly args: Readonly<Record<string, unknown>>,
    readonly result?: unknown,
  ) {}

  static createPending(input: CreateToolCallInput): ToolCall {
    return new ToolCall(
      input.requestId,
      input.pluginId,
      input.toolName,
      "pending",
      input.args,
    );
  }

  withStatus(
    status: ToolCallStatus,
    result?: unknown,
  ): ToolCall {
    return new ToolCall(
      this.requestId,
      this.pluginId,
      this.toolName,
      status,
      this.args,
      result,
    );
  }
}
