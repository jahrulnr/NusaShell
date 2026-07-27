import { PluginId } from "@nusashell/domain";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { StopPluginCommand } from "./stop-plugin.command.js";
import type { StopPluginResult } from "./stop-plugin.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class StopPluginHandler
  implements CommandHandler<StopPluginCommand, StopPluginResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(command: StopPluginCommand): Promise<StopPluginResult> {
    const idResult = PluginId.create(command.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    const view = await this.runtimeManager.stopPlugin(idResult.value);
    return { pluginId: view.pluginId, state: view.state };
  }
}
