import { ipcMain, shell, dialog, type OpenDialogOptions } from "electron";
import type { IpcContext, AppBehaviorPatch } from "./ipc-context.js";
import type { ShellLogLevel } from "../log-tail.js";
import { loadPluginPngDataUrl } from "../plugin-icon.js";
import { getLauncherWindow } from "../window-manager.js";

const DOCS_URL = "https://github.com/jahrulnr/NusaShell/tree/master/docs";

/** Register shell, logs, app-behavior, and updater IPC handlers. */
export function registerShellIpc(ctx: IpcContext): void {
  ipcMain.handle("shell:open-docs", async () => {
    await shell.openExternal(DOCS_URL);
  });

  ipcMain.handle("plugin-icons:read", (event, source: string, installPath: string) => {
    if (event.sender !== getLauncherWindow()?.webContents) {
      throw new Error("Plugin icons are only available to the launcher");
    }
    return loadPluginPngDataUrl(source, installPath);
  });

  ipcMain.handle("shell:pick-plugin-source", async (event, kind: "directory" | "archive") => {
    if (kind !== "directory" && kind !== "archive") return null;
    const owner = ctx.BrowserWindow.fromWebContents(event.sender);
    const options: OpenDialogOptions = kind === "directory"
      ? {
          title: "Choose a NusaShell plugin folder",
          buttonLabel: "Choose folder",
          properties: ["openDirectory"],
        }
      : {
          title: "Choose a NusaShell plugin archive",
          buttonLabel: "Choose archive",
          properties: ["openFile"],
          filters: [
            { name: "Plugin archives", extensions: ["zip", "tgz", "gz"] },
            { name: "All files", extensions: ["*"] },
          ],
        };
    const result = owner
      ? await dialog.showOpenDialog(owner, options)
      : await dialog.showOpenDialog(options);
    return result.canceled ? null : (result.filePaths[0] ?? null);
  });

  // Logs
  ipcMain.handle("logs:list", () => ctx.logTail.list());
  ipcMain.on("logs:write", (_event, level: string, message: string) => {
    if (!ctx.shellLogLevels.has(level as never) || typeof message !== "string") return;
    ctx.logTail.add("renderer", level as ShellLogLevel, message.slice(0, 4000));
  });

  // App behavior
  ipcMain.handle("app-behavior:get", async () => {
    const settings = await ctx.getAppBehaviorStore().load();
    return { ...settings, canSetLoginAutostart: ctx.app.isPackaged };
  });
  ipcMain.handle("app-behavior:set", async (_event, patch: AppBehaviorPatch) => {
    const store = ctx.getAppBehaviorStore();
    const previous = await store.load();
    const next = await store.set(patch);
    ctx.setAppBehavior(next);
    const loginSettingsChanged =
      next.launchAtLogin !== previous.launchAtLogin
      || (next.launchAtLogin && next.startHidden !== previous.startHidden);
    if (loginSettingsChanged) {
      try {
        await ctx.getLoginAutostart().set(next.launchAtLogin, { hidden: next.startHidden });
      } catch (error) {
        ctx.setAppBehavior(await store.set({
          launchAtLogin: previous.launchAtLogin,
          startHidden: previous.startHidden,
        }));
        throw error;
      }
    }
    return { ...next, canSetLoginAutostart: ctx.app.isPackaged };
  });

  // Updater (no-op in dev)
  ipcMain.handle("updater:check", async () => ctx.getUpdater()?.checkForUpdates() ?? null);
  ipcMain.handle("updater:quit-install", () => ctx.getUpdater()?.quitAndInstall());
  ipcMain.handle("updater:status", () => ctx.getUpdater()?.getStatus() ?? null);
}
