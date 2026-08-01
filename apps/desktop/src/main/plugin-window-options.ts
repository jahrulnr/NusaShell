import { isAbsolute, relative, resolve } from "node:path";

export interface PluginWindowOptionsInput {
  readonly entry?: string;
  readonly window?: {
    readonly mode?: "panel" | "fullscreen" | "widget";
    readonly defaultSize?: {
      readonly width: number;
      readonly height: number;
    };
    readonly resizable?: boolean;
  };
  readonly keepAliveOnClose?: boolean;
}

export interface NormalizedPluginWindowOptions {
  readonly entry: string;
  readonly width: number;
  readonly height: number;
  readonly resizable: boolean;
  readonly keepAliveOnClose: boolean;
}

interface WindowSize {
  readonly width: number;
  readonly height: number;
}

export function normalizePluginWindowOptions(
  input: PluginWindowOptionsInput = {},
): NormalizedPluginWindowOptions {
  const mode = input.window?.mode ?? "panel";
  const defaults = mode === "fullscreen"
    ? { width: 1200, height: 800 }
    : mode === "widget"
      ? { width: 420, height: 360 }
      : { width: 720, height: 480 };
  const entry = input.entry?.trim() ?? "";
  if (!entry) {
    throw new Error("Plugin has no UI entry (headless plugin); cannot open a window");
  }
  return {
    entry,
    width: clampDimension(input.window?.defaultSize?.width, 400, 1920, defaults.width),
    height: clampDimension(input.window?.defaultSize?.height, 300, 1200, defaults.height),
    resizable: input.window?.resizable !== false,
    keepAliveOnClose: input.keepAliveOnClose === true,
  };
}

export function resolvePluginUiPath(installPath: string, entry: string): string {
  if (isAbsolute(entry)) throw new Error("Plugin UI entry points outside its install directory");
  const root = resolve(installPath);
  const target = resolve(root, entry);
  const pathFromRoot = relative(root, target);
  if (!pathFromRoot || pathFromRoot.startsWith("..") || isAbsolute(pathFromRoot)) {
    throw new Error("Plugin UI entry points outside its install directory");
  }
  return target;
}

export function pluginWindowTitle(name: string, icon: string): string {
  return /^(?:https?:\/\/|file:\/\/)/i.test(icon)
    ? name
    : `${icon} ${name}`;
}

export function fitPluginWindowToWorkArea(
  requested: WindowSize,
  workArea: WindowSize,
): WindowSize {
  return {
    width: Math.min(requested.width, workArea.width),
    height: Math.min(requested.height, workArea.height),
  };
}

function clampDimension(
  value: number | undefined,
  minimum: number,
  maximum: number,
  fallback: number,
): number {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(maximum, Math.max(minimum, Math.round(value as number)));
}
