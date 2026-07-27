import { PluginId } from "@nusashell/domain";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { StartPluginCommand } from "./start-plugin.command.js";
import type { StartPluginResult } from "./start-plugin.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class StartPluginHandler
  implements CommandHandler<StartPluginCommand, StartPluginResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(command: StartPluginCommand): Promise<StartPluginResult> {
    const idResult = PluginId.create(command.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    const view = await this.runtimeManager.startPlugin(idResult.value);
    return { pluginId: view.pluginId, state: view.state };
  }
}
