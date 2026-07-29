import { join, resolve } from "node:path";

export const LINUX_DESKTOP_APP_NAME = "nusashell";

interface WindowIconPathInput {
  readonly isPackaged: boolean;
  readonly moduleDir: string;
  readonly resourcesPath: string;
}

export function resolveWindowIconPath(input: WindowIconPathInput): string {
  return input.isPackaged
    ? join(input.resourcesPath, "nusashell.png")
    : resolve(input.moduleDir, "..", "..", "assets", "nusashell.png");
}

export function resolveLinuxDevDesktopPaths(dataHome: string): {
  readonly desktopEntry: string;
  readonly icon: string;
} {
  return {
    desktopEntry: resolve(dataHome, "applications", `${LINUX_DESKTOP_APP_NAME}.desktop`),
    icon: resolve(
      dataHome,
      "icons",
      "hicolor",
      "512x512",
      "apps",
      `${LINUX_DESKTOP_APP_NAME}.png`,
    ),
  };
}
