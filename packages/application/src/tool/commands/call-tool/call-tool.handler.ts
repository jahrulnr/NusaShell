import { PluginId } from "@nusashell/domain";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginRuntimeManager } from "../../../plugin/services/plugin-runtime-manager.js";
import type { CallToolCommand } from "./call-tool.command.js";
import type { CallToolResult } from "./call-tool.result.js";
import { ApplicationError } from "../../../errors/application-error.js";

export class CallToolHandler
  implements CommandHandler<CallToolCommand, CallToolResult>
{
  constructor(private readonly runtimeManager: PluginRuntimeManager) {}

  async handle(command: CallToolCommand): Promise<CallToolResult> {
    const idResult = PluginId.create(command.pluginId);
    if (!idResult.ok) {
      throw new ApplicationError(
        "PLUGIN_NOT_FOUND",
        `Invalid plugin id: ${idResult.error.message}`,
      );
    }
    const result = await this.runtimeManager.callTool(idResult.value, {
      requestId: command.requestId,
      toolName: command.toolName,
      args: command.args,
      ...(command.timeoutMs !== undefined ? { timeoutMs: command.timeoutMs } : {}),
    });
    return { requestId: command.requestId, result };
  }
}
