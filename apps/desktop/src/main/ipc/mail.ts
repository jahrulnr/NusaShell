import { ipcMain, type IpcMainInvokeEvent } from "electron";
import type { IpcContext } from "./ipc-context.js";
import type { SaveMailAccountInput } from "../../shared/mail-contract.js";

const MAIL_PLUGIN_ID = "nusashell.mail";

function assertMailPluginSender(ctx: IpcContext, event: IpcMainInvokeEvent): void {
  let source: URL;
  try {
    source = new URL(event.sender.getURL());
  } catch {
    throw new Error("Mail account settings are only available to the Mail plugin");
  }
  if (
    source.protocol !== "file:"
    || source.searchParams.get("pluginId") !== MAIL_PLUGIN_ID
    || !ctx.isPluginWindowSender(event.sender, MAIL_PLUGIN_ID)
  ) {
    throw new Error("Mail account settings are only available to the Mail plugin");
  }
}

async function restartMailPlugin(ctx: IpcContext): Promise<void> {
  await ctx.commandBus.execute({ kind: "restart-plugin", pluginId: MAIL_PLUGIN_ID });
}

/** Register mail account IPC handlers. */
export function registerMailIpc(ctx: IpcContext): void {
  ipcMain.handle("mail-accounts:list", (event) => {
    assertMailPluginSender(ctx, event);
    return ctx.getMailSettingsStore().getPublic();
  });
  ipcMain.handle("mail-accounts:save", async (event, input: SaveMailAccountInput) => {
    assertMailPluginSender(ctx, event);
    const result = await ctx.getMailSettingsStore().save(input);
    await restartMailPlugin(ctx);
    return result;
  });
  ipcMain.handle("mail-accounts:delete", async (event, accountId: string) => {
    assertMailPluginSender(ctx, event);
    const result = await ctx.getMailSettingsStore().delete(accountId);
    await restartMailPlugin(ctx);
    return result;
  });
}
