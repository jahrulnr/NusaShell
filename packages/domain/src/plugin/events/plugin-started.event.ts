import type { DomainEvent } from "../../shared/domain-event.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import { PluginId as PluginIdVO } from "../value-objects/plugin-id.js";

export class PluginStartedEvent implements DomainEvent {
  readonly type = "plugin.started" as const;

  constructor(
    readonly aggregateId: string,
    readonly occurredAt: Date,
  ) {}

  static create(
    pluginId: PluginId,
    occurredAt: Date = new Date(),
  ): PluginStartedEvent {
    return new PluginStartedEvent(PluginIdVO.toString(pluginId), occurredAt);
  }
}
