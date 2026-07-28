import { contextBridge, ipcRenderer } from "electron";

export interface ShellApi {
  readonly wsUrl: string;
  openPlugin(pluginId: string, name: string, icon: string, installPath: string, windowMode?: string): Promise<void>;
  closePlugin(pluginId: string): Promise<void>;
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
};

contextBridge.exposeInMainWorld("shell", api);
