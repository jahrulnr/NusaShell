import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginInstallerPort } from "../../../plugin/ports/plugin-installer.port.js";
import type { EventDispatcher } from "../../../events/event-dispatcher.js";
import type { ClockPort } from "../../../plugin/ports/clock.port.js";
import type { InstallPluginCommand } from "./install-plugin.command.js";
import type { InstallPluginResult } from "./install-plugin.result.js";
import { PluginId, PluginVersion, PluginInstalledEvent } from "@nusashell/domain";

export class InstallPluginHandler
  implements CommandHandler<InstallPluginCommand, InstallPluginResult>
{
  constructor(
    private readonly installer: PluginInstallerPort,
    private readonly eventDispatcher: EventDispatcher,
    private readonly clock: ClockPort,
  ) {}

  async handle(command: InstallPluginCommand): Promise<InstallPluginResult> {
    const result =
      command.source === "url"
        ? await this.installer.installFromUrl(command.path)
        : await this.installer.installFromPath(command.path);

    const idResult = PluginId.create(result.pluginId);
    if (idResult.ok) {
      const versionResult = PluginVersion.create(result.version);
      if (versionResult.ok) {
        await this.eventDispatcher.publish(
          PluginInstalledEvent.create(idResult.value, versionResult.value, this.clock.now()),
        );
      }
    }

    return { pluginId: result.pluginId, installPath: result.installPath, version: result.version };
  }
}
