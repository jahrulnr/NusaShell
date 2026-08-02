import { join } from "node:path";

/**
 * Desktop runtime mode resolver.
 *
 * Pure helpers that decide dev vs prod behavior for the Electron shell so the
 * main process, preload, and window-manager all derive the same port and data
 * root from the same inputs.
 *
 * Production is defined by `app.isPackaged` (not `NODE_ENV`). Unpackaged
 * builds are only treated as dev when `--dev` is present, so a packaged binary
 * can never leak dev behavior even if `--dev` is somehow appended.
 */

export const PROD_WS_PORT = 9130;
export const DEV_WS_PORT = 9131;
export const DEV_DATA_DIRNAME = ".nusashell";
export const PROD_DATA_DIRNAME = "nusashell";

export interface IsDevOptions {
  readonly isPackaged: boolean;
  readonly argv: readonly string[];
}

export interface WsPortOptions {
  readonly isDev: boolean;
  readonly envPort?: string | undefined;
}

export interface DataRootOptions {
  readonly isDev: boolean;
  readonly repositoryRoot: string;
  readonly appDataPath: string;
}

export function resolveIsDev({ isPackaged, argv }: IsDevOptions): boolean {
  return !isPackaged && argv.includes("--dev");
}

export function resolveWsPort({ isDev, envPort }: WsPortOptions): number {
  const parsed = envPort !== undefined ? Number.parseInt(envPort, 10) : NaN;
  if (Number.isInteger(parsed) && parsed > 0 && parsed < 65536) return parsed;
  return isDev ? DEV_WS_PORT : PROD_WS_PORT;
}

export function resolveDataRoot({ isDev, repositoryRoot, appDataPath }: DataRootOptions): string {
  return isDev
    ? join(repositoryRoot, DEV_DATA_DIRNAME)
    : join(appDataPath, PROD_DATA_DIRNAME);
}
