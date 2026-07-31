import { Menu, Tray, nativeImage, type NativeImage } from "electron";
import { resolveWindowIconPath } from "./window-assets.js";

export interface TrayManagerOptions {
  readonly isPackaged: boolean;
  readonly moduleDir: string;
  readonly resourcesPath: string;
  readonly getStatusLabel?: () => string;
  readonly onOpen: () => void;
  readonly onQuit: () => void;
  readonly onToggle: () => void;
}

export function buildTrayMenuTemplate(input: {
  readonly statusLabel: string;
  readonly onOpen: () => void;
  readonly onQuit: () => void;
}): Electron.MenuItemConstructorOptions[] {
  return [
    {
      label: input.statusLabel,
      enabled: false,
    },
    {
      label: "Open NusaShell",
      click: () => input.onOpen(),
    },
    { type: "separator" },
    {
      label: "Quit",
      click: () => input.onQuit(),
    },
  ];
}

export class TrayManager {
  private tray: Tray | null = null;

  constructor(private readonly options: TrayManagerOptions) {}

  create(): Tray {
    if (this.tray) return this.tray;
    const icon = loadTrayIcon({
      isPackaged: this.options.isPackaged,
      moduleDir: this.options.moduleDir,
      resourcesPath: this.options.resourcesPath,
    });
    this.tray = new Tray(icon);
    this.tray.setToolTip("NusaShell");
    this.refreshMenu();
    this.tray.on("click", () => this.options.onToggle());
    return this.tray;
  }

  refreshMenu(): void {
    if (!this.tray) return;
    const statusLabel = this.options.getStatusLabel?.() ?? "NusaShell — running";
    const menu = Menu.buildFromTemplate(buildTrayMenuTemplate({
      statusLabel,
      onOpen: this.options.onOpen,
      onQuit: this.options.onQuit,
    }));
    this.tray.setContextMenu(menu);
  }

  destroy(): void {
    this.tray?.destroy();
    this.tray = null;
  }
}

export function loadTrayIcon(input: {
  readonly isPackaged: boolean;
  readonly moduleDir: string;
  readonly resourcesPath: string;
}): NativeImage {
  const path = resolveWindowIconPath(input);
  const image = nativeImage.createFromPath(path);
  if (image.isEmpty()) return image;
  const size = image.getSize();
  if (size.width <= 32 && size.height <= 32) return image;
  return image.resize({ width: 22, height: 22, quality: "best" });
}
