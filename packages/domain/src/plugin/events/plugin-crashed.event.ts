import type { DomainEvent } from "../../shared/domain-event.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import { PluginId as PluginIdVO } from "../value-objects/plugin-id.js";

export class PluginCrashedEvent implements DomainEvent {
  readonly type = "plugin.crashed" as const;

  constructor(
    readonly aggregateId: string,
    readonly reason: string,
    readonly occurredAt: Date,
  ) {}

  static create(
    pluginId: PluginId,
    reason: string,
    occurredAt: Date = new Date(),
  ): PluginCrashedEvent {
    return new PluginCrashedEvent(
      PluginIdVO.toString(pluginId),
      reason,
      occurredAt,
    );
  }
}
