import { contextBridge, ipcRenderer } from "electron";

export interface ShellApi {
  readonly wsUrl: string;
  openPlugin(pluginId: string, name: string, icon: string, installPath: string, windowMode?: string): Promise<void>;
  closePlugin(pluginId: string): Promise<void>;
  readonly windowControls: {
    minimize(): Promise<void>;
    toggleMaximize(): Promise<boolean>;
    close(): Promise<void>;
  };
  readonly logs: {
    list(): Promise<readonly ShellLogEntry[]>;
    write(level: ShellLogLevel, message: string): void;
    onEntry(callback: (entry: ShellLogEntry) => void): () => void;
  };
}

export type ShellLogLevel = "debug" | "info" | "warn" | "error";

export interface ShellLogEntry {
  readonly id: number;
  readonly timestamp: string;
  readonly source: "backend" | "ipc" | "main" | "mcp" | "renderer";
  readonly level: ShellLogLevel;
  readonly message: string;
}

const wsUrl = `ws://127.0.0.1:${process.env.NUSASHELL_PORT ?? "9130"}`;

const api: ShellApi = {
  wsUrl,
  openPlugin(pluginId, name, icon, installPath, windowMode) {
    return ipcRenderer.invoke("window:open-plugin", pluginId, name, icon, installPath, windowMode);
  },
  closePlugin(pluginId) {
    return ipcRenderer.invoke("window:close-plugin", pluginId);
  },
  windowControls: {
    minimize() {
      return ipcRenderer.invoke("window:minimize");
    },
    toggleMaximize() {
      return ipcRenderer.invoke("window:toggle-maximize");
    },
    close() {
      return ipcRenderer.invoke("window:close");
    },
  },
  logs: {
    list() {
      return ipcRenderer.invoke("logs:list");
    },
    write(level, message) {
      ipcRenderer.send("logs:write", level, message);
    },
    onEntry(callback) {
      const listener = (_event: Electron.IpcRendererEvent, entry: ShellLogEntry) => callback(entry);
      ipcRenderer.on("logs:entry", listener);
      return () => ipcRenderer.removeListener("logs:entry", listener);
    },
  },
};

contextBridge.exposeInMainWorld("shell", api);
