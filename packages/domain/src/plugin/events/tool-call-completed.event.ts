import type { DomainEvent } from "../../shared/domain-event.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import { PluginId as PluginIdVO } from "../value-objects/plugin-id.js";
import type { RequestId } from "../../tool/value-objects/request-id.js";
import { RequestId as RequestIdVO } from "../../tool/value-objects/request-id.js";
import type { ToolName } from "../../tool/value-objects/tool-name.js";
import { ToolName as ToolNameVO } from "../../tool/value-objects/tool-name.js";

export class ToolCallCompletedEvent implements DomainEvent {
  readonly type = "tool.call_completed" as const;

  constructor(
    readonly aggregateId: string,
    readonly requestId: string,
    readonly toolName: string,
    readonly occurredAt: Date,
  ) {}

  static create(
    pluginId: PluginId,
    requestId: RequestId,
    toolName: ToolName,
    occurredAt: Date = new Date(),
  ): ToolCallCompletedEvent {
    return new ToolCallCompletedEvent(
      PluginIdVO.toString(pluginId),
      RequestIdVO.toString(requestId),
      ToolNameVO.toString(toolName),
      occurredAt,
    );
  }
}
