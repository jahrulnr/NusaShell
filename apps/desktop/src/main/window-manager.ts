import { app, BrowserWindow, ipcMain } from "electron";
import { join, resolve } from "node:path";
import { resolveWindowIconPath } from "./window-assets.js";

const isDev = process.argv.includes("--dev");

const RENDERER_DIST = join(__dirname, "..", "renderer");
const PRELOAD_PATH = join(__dirname, "preload.cjs");
const WINDOW_ICON_PATH = resolveWindowIconPath({
  isPackaged: app.isPackaged,
  moduleDir: __dirname,
  resourcesPath: process.resourcesPath,
});

type WindowLog = (level: "debug" | "info", message: string) => void;

let launcherWindow: BrowserWindow | null = null;
const pluginWindows = new Map<string, BrowserWindow>();

export function createLauncherWindow(): BrowserWindow {
  launcherWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    minWidth: 900,
    minHeight: 600,
    show: false,
    title: "NusaShell",
    icon: WINDOW_ICON_PATH,
    frame: false,
    webPreferences: {
      preload: PRELOAD_PATH,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  const devServerUrl = MAIN_WINDOW_VITE_DEV_SERVER_URL;
  if (isDev && devServerUrl) {
    void launcherWindow.loadURL(devServerUrl);
  } else {
    void launcherWindow.loadFile(join(RENDERER_DIST, "index.html"));
  }
  launcherWindow.once("ready-to-show", () => launcherWindow?.show());

  launcherWindow.on("closed", () => {
    launcherWindow = null;
  });

  return launcherWindow;
}

export function getLauncherWindow(): BrowserWindow | null {
  return launcherWindow;
}

export async function openPluginWindow(
  pluginId: string,
  name: string,
  icon: string,
  installPath: string,
  windowMode?: string,
): Promise<void> {
  const existing = pluginWindows.get(pluginId);
  if (existing && !existing.isDestroyed()) {
    existing.focus();
    return;
  }
  pluginWindows.delete(pluginId);

  const width = windowMode === "fullscreen" ? 1200 : 720;
  const height = windowMode === "fullscreen" ? 800 : 480;

  const win = new BrowserWindow({
    width,
    height,
    minWidth: 400,
    minHeight: 300,
    title: `${icon} ${name}`,
    show: false,
    icon: WINDOW_ICON_PATH,
    ...(launcherWindow ? { parent: launcherWindow } : {}),
    webPreferences: {
      preload: PRELOAD_PATH,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  win.once("ready-to-show", () => {
    if (!win.isDestroyed()) win.show();
  });

  const uiPath = resolve(installPath, "ui", "index.html");
  try {
    await win.loadURL(`file://${uiPath}?pluginId=${encodeURIComponent(pluginId)}`);
  } catch (err) {
    console.error("[openPluginWindow] loadURL failed:", err);
  }
  if (!win.isDestroyed() && !win.isVisible()) {
    win.show();
  }

  win.on("closed", () => {
    pluginWindows.delete(pluginId);
    // Stop the plugin's MCP server when its window closes (keepAliveOnClose: false)
    const ws = new (require("ws"))(`ws://127.0.0.1:${process.env.NUSASHELL_PORT ?? "9130"}`);
    ws.on("open", () => {
      ws.send(JSON.stringify({
        kind: "request",
        id: `cleanup_${pluginId}`,
        method: "plugin.stop",
        protocolVersion: "1.0.0",
        payload: { pluginId },
      }));
      ws.close();
    });
    ws.on("error", () => { /* best-effort */ });
  });

  pluginWindows.set(pluginId, win);
}

export function closePluginWindow(pluginId: string): void {
  const win = pluginWindows.get(pluginId);
  if (win && !win.isDestroyed()) {
    win.close();
  }
  pluginWindows.delete(pluginId);
}

export function closeAllPluginWindows(): void {
  for (const win of pluginWindows.values()) {
    win.close();
  }
  pluginWindows.clear();
}

export function registerWindowIpc(log?: WindowLog): void {
  ipcMain.handle("window:minimize", (event) => {
    log?.("debug", "window.minimize");
    BrowserWindow.fromWebContents(event.sender)?.minimize();
  });

  ipcMain.handle("window:toggle-maximize", (event) => {
    log?.("debug", "window.toggle-maximize");
    const window = BrowserWindow.fromWebContents(event.sender);
    if (!window) return false;

    if (window.isMaximized()) {
      window.unmaximize();
      return false;
    }

    window.maximize();
    return true;
  });

  ipcMain.handle("window:toggle-always-on-top", (event) => {
    const window = BrowserWindow.fromWebContents(event.sender);
    if (!window) return false;

    const alwaysOnTop = !window.isAlwaysOnTop();
    window.setAlwaysOnTop(alwaysOnTop);
    log?.("debug", `window.always-on-top ${alwaysOnTop ? "enabled" : "disabled"}`);
    return alwaysOnTop;
  });

  ipcMain.handle("window:close", (event) => {
    log?.("debug", "window.close");
    BrowserWindow.fromWebContents(event.sender)?.close();
  });

  ipcMain.handle("window:open-plugin", async (_event, pluginId: string, name: string, icon: string, installPath: string, windowMode?: string) => {
    log?.("info", `window.open-plugin ${pluginId}`);
    await openPluginWindow(pluginId, name, icon, installPath, windowMode);
  });

  ipcMain.handle("window:close-plugin", (_event, pluginId: string) => {
    log?.("info", `window.close-plugin ${pluginId}`);
    closePluginWindow(pluginId);
  });
}
