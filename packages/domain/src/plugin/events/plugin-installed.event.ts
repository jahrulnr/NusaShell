import type { DomainEvent } from "../../shared/domain-event.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import { PluginId as PluginIdVO } from "../value-objects/plugin-id.js";
import type { PluginVersion } from "../value-objects/plugin-version.js";
import { PluginVersion as PluginVersionVO } from "../value-objects/plugin-version.js";

export class PluginInstalledEvent implements DomainEvent {
  readonly type = "plugin.installed" as const;

  constructor(
    readonly aggregateId: string,
    readonly version: string,
    readonly occurredAt: Date,
  ) {}

  static create(
    pluginId: PluginId,
    version: PluginVersion,
    occurredAt: Date = new Date(),
  ): PluginInstalledEvent {
    return new PluginInstalledEvent(
      PluginIdVO.toString(pluginId),
      PluginVersionVO.toString(version),
      occurredAt,
    );
  }
}
