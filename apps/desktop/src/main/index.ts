import { app, BrowserWindow } from "electron";
import { resolve } from "node:path";
import { bootstrap, type BootstrapResult } from "@nusashell/backend";
import {
  createLauncherWindow,
  closeAllPluginWindows,
  registerWindowIpc,
} from "./window-manager.js";

let backend: BootstrapResult | null = null;
const isDev = process.argv.includes("--dev");

async function startBackend(): Promise<BootstrapResult> {
  const pluginsRoot = resolve(__dirname, "..", "..", "..", "..", "plugins", "examples");
  return bootstrap({
    config: {
      port: 9130,
      host: "127.0.0.1",
      pluginsRoot,
      dbPath: undefined,
      logLevel: isDev ? "debug" : "info",
    },
  });
}

async function waitForBackend(port: number, maxRetries = 30): Promise<void> {
  for (let i = 0; i < maxRetries; i++) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}`);
      if (res.ok || res.status === 400 || res.status === 404) return;
    } catch {
      // backend not ready yet
    }
    await new Promise(r => setTimeout(r, 100));
  }
}

app.whenReady().then(async () => {
  registerWindowIpc();

  backend = await startBackend();
  await waitForBackend(backend.config.port);

  createLauncherWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createLauncherWindow();
    }
  });
});

app.on("window-all-closed", () => {
  closeAllPluginWindows();
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("before-quit", async (e) => {
  if (backend) {
    e.preventDefault();
    try {
      await backend.shutdown.shutdown();
    } catch {
      // best-effort
    }
    backend = null;
    app.quit();
  }
});
