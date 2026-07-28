import { BrowserWindow, ipcMain } from "electron";
import { join, resolve } from "node:path";
import { existsSync } from "node:fs";

const isDev = process.argv.includes("--dev");

const RENDERER_DIST = join(__dirname, "..", "renderer");
// Vite may output preload as preload.cjs or index.js depending on config/plugin behavior
const PRELOAD_PATH = existsSync(join(__dirname, "preload.cjs"))
  ? join(__dirname, "preload.cjs")
  : join(__dirname, "index.js");

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
  if (existing) {
    existing.focus();
    return;
  }

  const width = windowMode === "fullscreen" ? 1200 : 720;
  const height = windowMode === "fullscreen" ? 800 : 480;

  const win = new BrowserWindow({
    width,
    height,
    minWidth: 400,
    minHeight: 300,
    title: `${icon} ${name}`,
    show: false,
    ...(launcherWindow ? { parent: launcherWindow } : {}),
    webPreferences: {
      preload: PRELOAD_PATH,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  const uiPath = resolve(installPath, "ui", "index.html");
  console.log("[openPluginWindow] uiPath:", uiPath, "exists:", existsSync(uiPath));
  try {
    await win.loadURL(`file://${uiPath}`);
    console.log("[openPluginWindow] loadURL succeeded");
  } catch (err) {
    console.error("[openPluginWindow] loadURL failed:", err);
  }
  win.once("ready-to-show", () => {
    console.log("[openPluginWindow] ready-to-show, showing window");
    win.show();
  });

  win.on("closed", () => {
    pluginWindows.delete(pluginId);
    // Stop the plugin's MCP server when its window closes (keepAliveOnClose: false)
    const ws = new (require("ws"))("ws://127.0.0.1:9130");
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
  if (win) {
    win.close();
    pluginWindows.delete(pluginId);
  }
}

export function closeAllPluginWindows(): void {
  for (const win of pluginWindows.values()) {
    win.close();
  }
  pluginWindows.clear();
}

export function registerWindowIpc(): void {
  ipcMain.handle("window:open-plugin", async (_event, pluginId: string, name: string, icon: string, installPath: string, windowMode?: string) => {
    console.log("[IPC] window:open-plugin", pluginId, "installPath:", installPath);
    await openPluginWindow(pluginId, name, icon, installPath, windowMode);
  });

  ipcMain.handle("window:close-plugin", (_event, pluginId: string) => {
    closePluginWindow(pluginId);
  });
}
