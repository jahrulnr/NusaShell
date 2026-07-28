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
  updater: {
    checkForUpdates() {
      return ipcRenderer.invoke("updater:check");
    },
    quitAndInstall() {
      return ipcRenderer.invoke("updater:quit-install");
    },
    getStatus() {
      return ipcRenderer.invoke("updater:status");
    },
    on(channel, callback) {
      const validChannels = ["update-available", "update-not-available", "download-progress", "update-downloaded", "update-error"];
      if (validChannels.includes(channel)) {
        ipcRenderer.on(`updater:${channel}`, (_event, data) => callback(data));
      }
    },
  },
});
