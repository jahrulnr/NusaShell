import { readFile, writeFile, mkdir, rm, readdir, stat, cp } from "node:fs/promises";
import { join } from "node:path";
import type { Logger } from "pino";

export const BUNDLED_SEED_STATE_FILE = ".bundled-seed.json";

export interface BundledSeedEntry {
  readonly version: string;
  readonly removed?: boolean;
}

export type BundledSeedState = Record<string, BundledSeedEntry>;

export interface BundledSeedResult {
  readonly seeded: readonly string[];
  readonly updated: readonly string[];
  readonly skipped: readonly string[];
}

export interface BundledPluginSeederDeps {
  /** Read-only source of bundled plugins (packaged resources or repo plugins). */
  readonly bundledRoot: string;
  /** Single writable user plugins root (target of seed + reconcile). */
  readonly userRoot: string;
  /** Optional logger for diagnostics. */
  readonly logger?: Logger;
  /** Override the state file name (default `.bundled-seed.json`). */
  readonly stateFile?: string;
}

/**
 * Seeds bundled plugins into the single writable user plugins root and keeps
 * the copies reconciled against the bundled source on version bumps.
 *
 * Decision #49 (user: option B — fully writable single root):
 * - Fresh install: every bundled plugin folder is copied into `userRoot`.
 * - Upgrade: if the bundled version is newer than the seeded copy, the copy is
 *   replaced (reconcile). If equal or older, the user copy is left untouched
 *   (user edits are preserved; no downgrade).
 * - User removal: a previously-seeded plugin whose folder is gone is treated as
 *   intentionally uninstalled (tombstone) and never re-seeded.
 * - A copy that is no longer shipped by the bundle stays in place (the user
 *   keeps it; the bundle stops managing it).
 * - Non-bundled user plugins are never touched.
 *
 * State is tracked in `{userRoot}/.bundled-seed.json` (pluginId → seeded
 * version) so reconcile knows the last seeded version and which plugins were
 * deliberately removed.
 */
export class BundledPluginSeeder {
  private readonly bundledRoot: string;
  private readonly userRoot: string;
  private readonly logger: Logger | undefined;
  private readonly stateFilePath: string;

  constructor(deps: BundledPluginSeederDeps) {
    this.bundledRoot = deps.bundledRoot;
    this.userRoot = deps.userRoot;
    this.logger = deps.logger;
    this.stateFilePath = join(deps.userRoot, deps.stateFile ?? BUNDLED_SEED_STATE_FILE);
  }

  async seed(): Promise<BundledSeedResult> {
    const seeded: string[] = [];
    const updated: string[] = [];
    const skipped: string[] = [];

    const state = await this.readState();

    // Tombstone pass (must run BEFORE seeding): a previously-seeded plugin
    // whose folder no longer exists was uninstalled by the user. Mark it
    // removed so the seed loop below never resurrects it.
    const bundledIds = await listPluginDirs(this.bundledRoot);
    for (const pluginId of Object.keys(state)) {
      const entry = state[pluginId];
      if (!entry || entry.removed) continue;
      const userDir = join(this.userRoot, pluginId);
      if (!(await dirExists(userDir))) {
        state[pluginId] = { ...entry, removed: true };
        skipped.push(pluginId);
      }
    }

    for (const pluginId of bundledIds) {
      const bundledDir = join(this.bundledRoot, pluginId);
      const bundledManifest = await readManifest(bundledDir);
      if (!bundledManifest) continue;

      const userDir = join(this.userRoot, pluginId);
      const userExists = await dirExists(userDir);
      const entry = state[pluginId];

      if (!userExists) {
        if (entry?.removed) {
          // User deliberately uninstalled the seeded copy — do not resurrect it.
          skipped.push(pluginId);
          continue;
        }
        await copyPluginDir(bundledDir, userDir);
        state[pluginId] = { version: bundledManifest.version };
        seeded.push(pluginId);
        this.logger?.info({ pluginId, version: bundledManifest.version }, "Seeded bundled plugin");
        continue;
      }

      const installedManifest = await readManifest(userDir);
      const installedVersion = installedManifest?.version;
      if (!installedVersion) {
        // Folder exists but has no readable manifest — treat as user-owned, skip.
        skipped.push(pluginId);
        continue;
      }

      if (compareSemver(bundledManifest.version, installedVersion) > 0) {
        await rm(userDir, { recursive: true, force: true });
        await copyPluginDir(bundledDir, userDir);
        await preserveRuntimePreferences(userDir, installedManifest);
        state[pluginId] = { version: bundledManifest.version };
        updated.push(pluginId);
        this.logger?.info(
          { pluginId, from: installedVersion, to: bundledManifest.version },
          "Reconciled bundled plugin to newer version",
        );
      } else {
        // Equal or user copy is newer — never downgrade, never rewrite user edits.
        state[pluginId] = { version: bundledManifest.version };
        skipped.push(pluginId);
      }
    }

    await this.writeState(state);
    return { seeded, updated, skipped };
  }

  private async readState(): Promise<BundledSeedState> {
    try {
      const raw = await readFile(this.stateFilePath, "utf8");
      const parsed: unknown = JSON.parse(raw);
      if (parsed && typeof parsed === "object") return parsed as BundledSeedState;
      return {};
    } catch {
      return {};
    }
  }

  private async writeState(state: BundledSeedState): Promise<void> {
    await mkdir(this.userRoot, { recursive: true });
    await writeFile(this.stateFilePath, `${JSON.stringify(state, null, 2)}\n`, "utf8");
  }
}

async function listPluginDirs(root: string): Promise<string[]> {
  let entries: string[];
  try {
    entries = await readdir(root);
  } catch {
    return [];
  }
  const dirs: string[] = [];
  for (const entry of entries) {
    const info = await stat(join(root, entry)).catch(() => null);
    if (info?.isDirectory()) dirs.push(entry);
  }
  return dirs.sort();
}

async function readManifest(dir: string): Promise<{ version: string; mcp?: { autostart?: boolean; keepAliveOnClose?: boolean } } | null> {
  try {
    const raw = await readFile(join(dir, "manifest.json"), "utf8");
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && typeof (parsed as { version?: unknown }).version === "string") {
      const record = parsed as { version: string; mcp?: { autostart?: unknown; keepAliveOnClose?: unknown } };
      return {
        version: record.version,
        ...(record.mcp && typeof record.mcp === "object"
          ? { mcp: {
              ...(typeof record.mcp.autostart === "boolean" ? { autostart: record.mcp.autostart } : {}),
              ...(typeof record.mcp.keepAliveOnClose === "boolean" ? { keepAliveOnClose: record.mcp.keepAliveOnClose } : {}),
            } }
          : {}),
      };
    }
    return null;
  } catch {
    return null;
  }
}

async function preserveRuntimePreferences(
  dir: string,
  previous: { mcp?: { autostart?: boolean; keepAliveOnClose?: boolean } } | null,
): Promise<void> {
  if (!previous?.mcp || (previous.mcp.autostart === undefined && previous.mcp.keepAliveOnClose === undefined)) return;
  try {
    const path = join(dir, "manifest.json");
    const raw = JSON.parse(await readFile(path, "utf8")) as { mcp?: Record<string, unknown> };
    raw.mcp = {
      ...(raw.mcp ?? {}),
      ...(previous.mcp.autostart !== undefined ? { autostart: previous.mcp.autostart } : {}),
      ...(previous.mcp.keepAliveOnClose !== undefined ? { keepAliveOnClose: previous.mcp.keepAliveOnClose } : {}),
    };
    await writeFile(path, `${JSON.stringify(raw, null, 2)}\n`, "utf8");
  } catch {
    // A malformed upgrade manifest will be rejected by the normal sync path.
  }
}

async function dirExists(dir: string): Promise<boolean> {
  const info = await stat(dir).catch(() => null);
  return info?.isDirectory() ?? false;
}

async function copyPluginDir(src: string, dest: string): Promise<void> {
  await mkdir(dest, { recursive: true });
  await cp(src, dest, { recursive: true });
}

/**
 * Compare two semver strings. Returns >0 if a is newer, <0 if older, 0 if equal.
 * Only numeric major.minor.patch is compared (prerelease/build suffixes are
 * ignored) — sufficient for bundled plugin reconcile decisions.
 */
export function compareSemver(a: string, b: string): number {
  const parse = (v: string): number[] => {
    const core = v.replace(/[-+].*$/, "").split(".");
    const nums = core.map((part) => {
      const n = Number.parseInt(part, 10);
      return Number.isNaN(n) ? 0 : n;
    });
    while (nums.length < 3) nums.push(0);
    return nums;
  };
  const av = parse(a);
  const bv = parse(b);
  for (let i = 0; i < 3; i++) {
    const x = av[i] ?? 0;
    const y = bv[i] ?? 0;
    if (x !== y) return x - y;
  }
  return 0;
}
