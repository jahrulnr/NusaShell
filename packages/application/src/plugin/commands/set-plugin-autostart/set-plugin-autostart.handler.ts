import { PluginId } from "@nusashell/domain";
import { ApplicationError } from "../../../errors/application-error.js";
import type { CommandHandler } from "../../../messaging/command-handler.js";
import type { PluginRuntimeManager } from "../../services/plugin-runtime-manager.js";
import type { SetPluginAutostartCommand } from "./set-plugin-autostart.command.js";
export class SetPluginAutostartHandler implements CommandHandler<SetPluginAutostartCommand, { pluginId: string; autostart: boolean }> { constructor(private readonly runtimeManager: PluginRuntimeManager) {} async handle(command: SetPluginAutostartCommand) { const id = PluginId.create(command.pluginId); if (!id.ok) throw new ApplicationError("PLUGIN_NOT_FOUND", `Invalid plugin id: ${id.error.message}`); const view = await this.runtimeManager.setAutostart(id.value, command.autostart); return { pluginId: view.pluginId, autostart: view.autostart }; } }
