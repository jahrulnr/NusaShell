import type { DomainEvent } from "../../shared/domain-event.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import { PluginId as PluginIdVO } from "../value-objects/plugin-id.js";
import type { PluginRuntimeState } from "../value-objects/runtime-state.js";

export class PluginStateChangedEvent implements DomainEvent {
  readonly type = "plugin.state_changed" as const;

  constructor(
    readonly aggregateId: string,
    readonly from: PluginRuntimeState,
    readonly to: PluginRuntimeState,
    readonly occurredAt: Date,
  ) {}

  static create(
    pluginId: PluginId,
    from: PluginRuntimeState,
    to: PluginRuntimeState,
    occurredAt: Date = new Date(),
  ): PluginStateChangedEvent {
    return new PluginStateChangedEvent(
      PluginIdVO.toString(pluginId),
      from,
      to,
      occurredAt,
    );
  }
}
