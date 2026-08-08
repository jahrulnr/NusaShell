import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

export interface AppBehaviorSettings {
  readonly launchAtLogin: boolean;
  readonly startHidden: boolean;
  readonly keepInBackground: boolean;
  readonly canvasEnabled: boolean;
}

export type AppBehaviorPatch = Partial<AppBehaviorSettings>;

export const DEFAULT_APP_BEHAVIOR: AppBehaviorSettings = {
  launchAtLogin: false,
  startHidden: true,
  keepInBackground: true,
  canvasEnabled: true,
};

export function normalizeAppBehavior(raw: unknown): AppBehaviorSettings {
  const record = asRecord(raw);
  return {
    launchAtLogin: booleanOr(record.launchAtLogin, DEFAULT_APP_BEHAVIOR.launchAtLogin),
    startHidden: booleanOr(record.startHidden, DEFAULT_APP_BEHAVIOR.startHidden),
    keepInBackground: booleanOr(record.keepInBackground, DEFAULT_APP_BEHAVIOR.keepInBackground),
    canvasEnabled: booleanOr(record.canvasEnabled, DEFAULT_APP_BEHAVIOR.canvasEnabled),
  };
}

/** Pure close-decision used by the launcher lifecycle. */
export function shouldHideOnClose(input: {
  readonly keepInBackground: boolean;
  readonly isQuitting: boolean;
}): boolean {
  return input.keepInBackground && !input.isQuitting;
}

/** Whether window-all-closed should quit the process. */
export function shouldQuitOnAllWindowsClosed(input: {
  readonly keepInBackground: boolean;
  readonly platform: NodeJS.Platform;
}): boolean {
  if (input.platform === "darwin") return false;
  return !input.keepInBackground;
}

export class AppBehaviorStore {
  private state: AppBehaviorSettings | null = null;
  private persisted = false;

  constructor(private readonly path: string) {}

  async load(): Promise<AppBehaviorSettings> {
    if (this.state) return this.state;
    try {
      const raw = JSON.parse(await readFile(this.path, "utf8")) as unknown;
      this.state = normalizeAppBehavior(raw);
      this.persisted = true;
    } catch (error) {
      if (isFileNotFound(error)) {
        this.state = { ...DEFAULT_APP_BEHAVIOR };
      } else {
        throw new Error("Could not load app behavior settings", { cause: error });
      }
    }
    return this.state;
  }

  async hasPersistedSettings(): Promise<boolean> {
    await this.load();
    return this.persisted;
  }

  async set(patch: AppBehaviorPatch): Promise<AppBehaviorSettings> {
    const current = await this.load();
    this.state = normalizeAppBehavior({ ...current, ...patch });
    await this.persist(this.state);
    return this.state;
  }

  private async persist(settings: AppBehaviorSettings): Promise<void> {
    await mkdir(dirname(this.path), { recursive: true });
    const temporaryPath = `${this.path}.tmp`;
    await writeFile(temporaryPath, JSON.stringify(settings, null, 2), { mode: 0o600 });
    await rename(temporaryPath, this.path);
  }
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function booleanOr(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function isFileNotFound(error: unknown): boolean {
  return typeof error === "object" && error !== null && "code" in error && error.code === "ENOENT";
}
