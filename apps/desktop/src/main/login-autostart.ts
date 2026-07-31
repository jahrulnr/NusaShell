import { mkdir, readFile, rename, unlink, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import type { AppBehaviorSettings } from "./app-behavior-settings.js";

export const LOGIN_AUTOSTART_DESKTOP_NAME = "nusashell.desktop";
export const LOGIN_AUTOSTART_REQUIRES_PACKAGED =
  "Login autostart requires a packaged build";

export interface LoginAutostartDeps {
  readonly platform: NodeJS.Platform;
  readonly isPackaged: boolean;
  readonly exePath: string;
  /** Home directory used to resolve ~/.config when XDG_CONFIG_HOME is unset. */
  readonly homeDir: string;
  readonly xdgConfigHome?: string;
  readonly setLoginItemSettings?: (settings: {
    openAtLogin: boolean;
    openAsHidden?: boolean;
    path?: string;
    args?: string[];
  }) => void;
  readonly getLoginItemSettings?: () => { openAtLogin: boolean };
  readonly log?: (message: string) => void;
}

export interface LoginAutostart {
  get(): Promise<boolean>;
  set(enabled: boolean, opts: { hidden: boolean }): Promise<void>;
  reconcile(settings: AppBehaviorSettings): Promise<void>;
}

export function createLoginAutostart(deps: LoginAutostartDeps): LoginAutostart {
  return {
    async get() {
      if (deps.platform === "linux") {
        try {
          await readFile(resolveAutostartDesktopPath(deps), "utf8");
          return true;
        } catch (error) {
          if (isFileNotFound(error)) return false;
          throw error;
        }
      }
      if (deps.platform === "darwin" || deps.platform === "win32") {
        return Boolean(deps.getLoginItemSettings?.().openAtLogin);
      }
      deps.log?.(`login autostart get unsupported on ${deps.platform}`);
      return false;
    },

    async set(enabled, opts) {
      if (!deps.isPackaged) {
        throw new Error(LOGIN_AUTOSTART_REQUIRES_PACKAGED);
      }
      if (deps.platform === "linux") {
        if (enabled) {
          await writeXdgAutostart(deps, opts.hidden);
        } else {
          await removeXdgAutostart(deps);
        }
        return;
      }
      if (deps.platform === "darwin" || deps.platform === "win32") {
        applyNativeLoginItem(deps, enabled, opts.hidden);
        return;
      }
      deps.log?.(`login autostart set unsupported on ${deps.platform}`);
    },

    async reconcile(settings) {
      if (!deps.isPackaged) {
        deps.log?.("skipping login autostart reconcile in unpackaged build");
        return;
      }
      try {
        if (settings.launchAtLogin) {
          await this.set(true, { hidden: settings.startHidden });
        } else if (await this.get()) {
          await this.set(false, { hidden: settings.startHidden });
        }
      } catch (error) {
        deps.log?.(
          `login autostart reconcile failed: ${error instanceof Error ? error.message : String(error)}`,
        );
      }
    },
  };
}

export function resolveAutostartDesktopPath(deps: Pick<LoginAutostartDeps, "homeDir" | "xdgConfigHome">): string {
  const configHome = deps.xdgConfigHome?.trim()
    || join(deps.homeDir, ".config");
  return resolve(configHome, "autostart", LOGIN_AUTOSTART_DESKTOP_NAME);
}

export function buildXdgAutostartDesktop(input: {
  readonly exePath: string;
  readonly hidden: boolean;
}): string {
  const exec = input.hidden
    ? `${quoteExec(input.exePath)} --hidden`
    : quoteExec(input.exePath);
  return [
    "[Desktop Entry]",
    "Type=Application",
    "Version=1.0",
    "Name=NusaShell",
    "Comment=NusaShell — AI tool shell",
    `Exec=${exec}`,
    "Terminal=false",
    "X-GNOME-Autostart-enabled=true",
    "",
  ].join("\n");
}

async function writeXdgAutostart(deps: LoginAutostartDeps, hidden: boolean): Promise<void> {
  const path = resolveAutostartDesktopPath(deps);
  await mkdir(dirname(path), { recursive: true });
  const content = buildXdgAutostartDesktop({ exePath: deps.exePath, hidden });
  const temporaryPath = `${path}.tmp`;
  await writeFile(temporaryPath, content, { mode: 0o644 });
  await rename(temporaryPath, path);
}

async function removeXdgAutostart(deps: LoginAutostartDeps): Promise<void> {
  const path = resolveAutostartDesktopPath(deps);
  try {
    await unlink(path);
  } catch (error) {
    if (!isFileNotFound(error)) throw error;
  }
}

function applyNativeLoginItem(deps: LoginAutostartDeps, enabled: boolean, hidden: boolean): void {
  if (!deps.setLoginItemSettings) {
    deps.log?.(`login autostart setLoginItemSettings unavailable on ${deps.platform}`);
    return;
  }
  if (deps.platform === "darwin") {
    deps.setLoginItemSettings({
      openAtLogin: enabled,
      openAsHidden: hidden,
    });
    return;
  }
  deps.setLoginItemSettings({
    openAtLogin: enabled,
    path: deps.exePath,
    args: hidden ? ["--hidden"] : [],
  });
}

/** Quote an Exec line so spaces in the AppImage path survive the desktop parser. */
function quoteExec(value: string): string {
  if (!/[\s"$`\\]/.test(value)) return value;
  return `"${value.replace(/(["\\`$])/g, "\\$1")}"`;
}

function isFileNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}
