import { app, BrowserWindow, ipcMain, Menu } from "electron";
import { resolve } from "node:path";
import { randomUUID } from "node:crypto";
import { bootstrap, type BootstrapResult } from "@nusashell/backend";
import { LogTail, type ShellLogLevel, type ShellLogSource } from "./log-tail.js";
import {
  createLauncherWindow,
  closeAllPluginWindows,
  registerWindowIpc,
} from "./window-manager.js";
import { AppUpdater } from "./updater.js";
import type { CallToolCommand, ListToolsQuery } from "@nusashell/application";

let backend: BootstrapResult | null = null;
let updater: AppUpdater | null = null;
const isDev = process.argv.includes("--dev");
const logTail = new LogTail(1000);
const shellLogLevels = new Set<ShellLogLevel>(["debug", "info", "warn", "error"]);

function redactLogMessage(message: string): string {
  return message
    .replace(/([?&](?:token|password|secret|api[_-]?key|authorization)=)[^&\s]+/gi, "$1[REDACTED]")
    .replace(/((?:token|password|secret|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)[^,\s}"']+/gi, "$1[REDACTED]");
}

function formatLogArguments(args: readonly unknown[]): string {
  const message = args.map((arg) => {
    if (arg instanceof Error) return arg.stack ?? arg.message;
    if (typeof arg === "string") return arg;
    try {
      return JSON.stringify(arg);
    } catch {
      return String(arg);
    }
  }).join(" ");
  return redactLogMessage(message);
}

function toShellLogLevel(level: string): ShellLogLevel {
  if (level === "error" || level === "fatal") return "error";
  if (level === "warn") return "warn";
  if (level === "debug" || level === "trace") return "debug";
  return "info";
}

function captureMainConsole(): void {
  for (const level of ["debug", "info", "warn", "error"] as const) {
    const original = console[level].bind(console);
    console[level] = (...args: unknown[]) => {
      logTail.add("main", level, formatLogArguments(args));
      original(...args);
    };
  }
}

captureMainConsole();

if (isDev) {
  app.commandLine.appendSwitch("no-sandbox");
}

async function startBackend(): Promise<BootstrapResult> {
  const pluginsRoot = app.isPackaged
    ? resolve(process.resourcesPath, "plugins", "examples")
    : resolve(__dirname, "..", "..", "..", "..", "plugins", "examples");

  // SQLite requires better-sqlite3 native module rebuilt for Electron's ABI.
  // Until that's set up, default to filesystem registry. Set NUSASHELL_DB_PATH to opt in.
  const dbPath = process.env.NUSASHELL_DB_PATH || undefined;
  return bootstrap({
    config: { port: 9130, host: "127.0.0.1", pluginsRoot, dbPath, logLevel: isDev ? "debug" : "info" },
    loggerObserver: ({ level, args }) => {
      const message = formatLogArguments(args);
      const source: ShellLogSource = /\bmcp\b|stdio/i.test(message) ? "mcp" : "backend";
      logTail.add(source, toShellLogLevel(level), message);
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
  Menu.setApplicationMenu(null);
  registerWindowIpc((level, message) => logTail.add("ipc", level, message));

  logTail.subscribe((entry) => {
    for (const window of BrowserWindow.getAllWindows()) {
      window.webContents.send("logs:entry", entry);
    }
  });

  ipcMain.handle("logs:list", () => logTail.list());
  ipcMain.on("logs:write", (_event, level: ShellLogLevel, message: string) => {
    if (!shellLogLevels.has(level) || typeof message !== "string") return;
    logTail.add("renderer", level, redactLogMessage(message.slice(0, 4000)));
  });

  try {
    backend = await startBackend();
    await waitForBackend(backend.config.port);
  } catch (err) {
    console.error("[main] startBackend failed:", err);
  }

  // IPC handlers for plugin tool calls (in-process, no WS roundtrip)
  ipcMain.handle("tool:call", async (_event, pluginId: string, toolName: string, args: Record<string, unknown>) => {
    if (!backend) throw new Error("Backend not ready");
    const command: CallToolCommand = {
      kind: "call-tool",
      pluginId,
      requestId: randomUUID(),
      toolName,
      args: args ?? {},
    };
    logTail.add("ipc", "info", `tool.call ${pluginId}.${toolName} (${command.requestId})`);
    try {
      const result = await backend.container.commandBus.execute(command);
      logTail.add("ipc", "info", `tool.call completed ${pluginId}.${toolName} (${command.requestId})`);
      return result;
    } catch (error) {
      logTail.add("ipc", "error", `tool.call failed ${pluginId}.${toolName}: ${String(error)}`);
      throw error;
    }
  });

  ipcMain.handle("tool:list", async (_event, pluginId: string) => {
    if (!backend) throw new Error("Backend not ready");
    const query: ListToolsQuery = {
      kind: "list-tools",
      pluginId,
    };
    logTail.add("ipc", "debug", `tool.list ${pluginId}`);
    return backend.container.queryBus.execute(query);
  });

  createLauncherWindow();

  if (app.isPackaged) {
    updater = new AppUpdater();
    void updater.checkForUpdates();
  }

  // Register updater IPC handlers always (no-op in dev) to prevent renderer errors
  ipcMain.handle("updater:check", async () => updater?.checkForUpdates() ?? null);
  ipcMain.handle("updater:quit-install", () => updater?.quitAndInstall());
  ipcMain.handle("updater:status", () => updater?.getStatus() ?? null);

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
