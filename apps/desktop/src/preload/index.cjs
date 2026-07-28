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
  callTool(pluginId, toolName, args) {
    return ipcRenderer.invoke("tool:call", pluginId, toolName, args);
  },
  listTools(pluginId) {
    return ipcRenderer.invoke("tool:list", pluginId);
  },
  logs: {
    list() {
      return ipcRenderer.invoke("logs:list");
    },
    write(level, message) {
      ipcRenderer.send("logs:write", level, message);
    },
    onEntry(callback) {
      const listener = (_event, entry) => callback(entry);
      ipcRenderer.on("logs:entry", listener);
      return () => ipcRenderer.removeListener("logs:entry", listener);
    },
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
