// Preload script — plain JS so Electron can load it directly.
// Exposes a minimal shell API to the renderer via contextBridge.
const { contextBridge, ipcRenderer } = require("electron");

const wsUrl = `ws://127.0.0.1:${process.env.NUSASHELL_PORT ?? "9130"}`;

contextBridge.exposeInMainWorld("shell", {
  wsUrl,
  openPlugin(pluginId, name, icon, installPath, windowMode) {
    return ipcRenderer.invoke("window:open-plugin", pluginId, name, icon, installPath, windowMode);
  },
  closePlugin(pluginId) {
    return ipcRenderer.invoke("window:close-plugin", pluginId);
  },
});
