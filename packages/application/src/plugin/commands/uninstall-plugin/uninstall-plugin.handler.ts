import { PluginId, PluginUninstalledEvent } from "@nusashell/domain";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginInstallerPort } from "../../../plugin/ports/plugin-installer.port.js";
import type { PluginRepositoryPort } from "../../../plugin/ports/plugin-repository.port.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { EventDispatcher } from "../../../events/event-dispatcher.js";
import type { ClockPort } from "../../../plugin/ports/clock.port.js";
import type { UninstallPluginCommand } from "./uninstall-plugin.command.js";
import type { UninstallPluginResult } from "./uninstall-plugin.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class UninstallPluginHandler
  implements CommandHandler<UninstallPluginCommand, UninstallPluginResult>
{
  constructor(
    private readonly installer: PluginInstallerPort,
    private readonly runtimeManager: PluginRuntimeManager,
    private readonly pluginRepository: PluginRepositoryPort,
    private readonly eventDispatcher: EventDispatcher,
    private readonly clock: ClockPort,
  ) {}

  async handle(command: UninstallPluginCommand): Promise<UninstallPluginResult> {
    const idResult = PluginId.create(command.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }

    await this.runtimeManager.removePlugin(idResult.value);
    await this.pluginRepository.remove(idResult.value);
    await this.installer.uninstall(command.pluginId);

    await this.eventDispatcher.publish(
      PluginUninstalledEvent.create(idResult.value, this.clock.now()),
    );

    return { pluginId: command.pluginId };
  }
}
