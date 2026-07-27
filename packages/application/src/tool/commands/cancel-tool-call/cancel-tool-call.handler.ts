import { PluginId } from "@nusashell/domain";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { CancelToolCallCommand } from "./cancel-tool-call.command.js";
import type { CancelToolCallResult } from "./cancel-tool-call.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class CancelToolCallHandler
  implements CommandHandler<CancelToolCallCommand, CancelToolCallResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(command: CancelToolCallCommand): Promise<CancelToolCallResult> {
    const idResult = PluginId.create(command.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    await this.runtimeManager.cancelTool(idResult.value, command.requestId);
    return { cancelled: true };
  }
}
