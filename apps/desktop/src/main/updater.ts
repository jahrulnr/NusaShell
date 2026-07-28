import { autoUpdater, type UpdateInfo } from "electron-updater";

export interface UpdateStatus {
  available: boolean;
  info?: UpdateInfo;
  downloading: boolean;
  downloaded: boolean;
  error?: string;
}

export class AppUpdater {
  private status: UpdateStatus = {
    available: false,
    downloading: false,
    downloaded: false,
  };

  constructor() {
    autoUpdater.autoDownload = true;
    autoUpdater.autoInstallOnAppQuit = true;

    autoUpdater.on("update-available", (info) => {
      this.status = { ...this.status, available: true, info, downloading: true };
      this.notifyRenderer("update-available", info);
    });

    autoUpdater.on("update-not-available", (info) => {
      this.status = { ...this.status, available: false, info };
      this.notifyRenderer("update-not-available", info);
    });

    autoUpdater.on("download-progress", (progress) => {
      this.notifyRenderer("download-progress", progress);
    });

    autoUpdater.on("update-downloaded", (info) => {
      this.status = { ...this.status, downloading: false, downloaded: true, info };
      this.notifyRenderer("update-downloaded", info);
    });

    autoUpdater.on("error", (err) => {
      this.status = { ...this.status, error: err?.message ?? String(err) };
      this.notifyRenderer("update-error", { message: this.status.error });
    });
  }

  async checkForUpdates(): Promise<UpdateStatus> {
    try {
      const result = await autoUpdater.checkForUpdates();
      if (result) {
        this.status = {
          ...this.status,
          available: true,
          info: result.updateInfo,
        };
      }
    } catch (err) {
      this.status = {
        ...this.status,
        error: err instanceof Error ? err.message : String(err),
      };
    }
    return this.status;
  }

  quitAndInstall(): void {
    autoUpdater.quitAndInstall();
  }

  getStatus(): UpdateStatus {
    return this.status;
  }

  private notifyRenderer(channel: string, data: unknown): void {
    const { BrowserWindow } = require("electron") as typeof import("electron");
    for (const win of BrowserWindow.getAllWindows()) {
      win.webContents.send(`updater:${channel}`, data);
    }
  }
}
