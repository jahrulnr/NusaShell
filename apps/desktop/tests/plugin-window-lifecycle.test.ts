import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WebContents } from "electron";

const electronState = vi.hoisted(() => {
  const state = {
    onLoad: null as null | ((window: { readonly webContents: unknown }) => void),
  };
  class FakeBrowserWindow {
    static fromWebContents(): FakeBrowserWindow | null {
      return null;
    }

    readonly webContents = {};
    private visible = false;

    constructor(_options: unknown) {}

    once(_event: string, _listener: () => void): void {}
    on(_event: string, _listener: () => void): void {}
    focus(): void {}
    close(): void {}

    async loadURL(_url: string): Promise<void> {
      state.onLoad?.(this);
    }

    isDestroyed(): boolean {
      return false;
    }

    isVisible(): boolean {
      return this.visible;
    }

    show(): void {
      this.visible = true;
    }
  }
  return Object.assign(state, { BrowserWindow: FakeBrowserWindow });
});

vi.mock("electron", () => ({
  app: {
    isPackaged: false,
    getPath: () => "/tmp",
  },
  BrowserWindow: electronState.BrowserWindow,
  ipcMain: { handle: vi.fn() },
  screen: {
    getPrimaryDisplay: () => ({ workAreaSize: { width: 1920, height: 1040 } }),
    getDisplayMatching: () => ({ workAreaSize: { width: 1920, height: 1040 } }),
  },
}));

vi.mock("../src/main/window-assets.js", () => ({
  resolveWindowIconPath: () => "/tmp/nusashell.png",
}));

import {
  closeAllPluginWindows,
  isPluginWindowSender,
  openPluginWindow,
} from "../src/main/window-manager.js";

describe("plugin window lifecycle", () => {
  beforeEach(() => {
    electronState.onLoad = null;
    closeAllPluginWindows();
  });

  it("authorizes the plugin sender while its first page is still loading", async () => {
    let authorizedDuringLoad = false;
    electronState.onLoad = (window) => {
      authorizedDuringLoad = isPluginWindowSender(
        window.webContents as WebContents,
        "com.nusashell.mail",
      );
    };

    await openPluginWindow(
      "com.nusashell.mail",
      "Mail",
      "✉",
      "/plugins/mail",
      { entry: "ui/index.html" },
    );

    expect(authorizedDuringLoad).toBe(true);
  });
});
