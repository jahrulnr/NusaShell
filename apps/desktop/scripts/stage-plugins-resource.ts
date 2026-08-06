/**
 * Stage the monorepo `plugins/` tree for electron-forge `extraResource`.
 *
 * Copies into a clean staging directory while excluding runtime state and test
 * artifacts that must never ship inside the install bundle (e.g. Notes MCP data
 * written next to the plugin during local dev or tests).
 */
import { cp, mkdir, readdir, rename, rm } from "node:fs/promises";
import { basename, dirname, join, resolve, sep } from "node:path";

/** Basenames of plugin-local runtime state files excluded from packaging. */
export const PLUGIN_PACKAGE_EXCLUDED_BASENAMES = Object.freeze([
  "notes.json",
] as const);

/** Directory basenames excluded anywhere under a plugin tree. */
export const PLUGIN_PACKAGE_EXCLUDED_DIRNAMES = Object.freeze([
  "tests",
  "__tests__",
  ".vite",
  "coverage",
] as const);

/** File basenames excluded from packaging (dev/test harness only). */
export const PLUGIN_PACKAGE_EXCLUDED_FILE_BASENAMES = Object.freeze([
  "vitest.config.ts",
  "vitest.config.js",
  "vitest.config.mjs",
  "vitest.config.cjs",
] as const);

function pathSegments(entryPath: string): string[] {
  return resolve(entryPath).split(sep).filter(Boolean);
}

export function isExcludedPluginPackageEntry(entryPath: string): boolean {
  const name = basename(entryPath);
  if ((PLUGIN_PACKAGE_EXCLUDED_BASENAMES as readonly string[]).includes(name)) {
    return true;
  }
  if ((PLUGIN_PACKAGE_EXCLUDED_FILE_BASENAMES as readonly string[]).includes(name)) {
    return true;
  }
  const segments = pathSegments(entryPath);
  return segments.some((segment) =>
    (PLUGIN_PACKAGE_EXCLUDED_DIRNAMES as readonly string[]).includes(segment),
  );
}

export async function stagePluginsResource(
  sourcePluginsRoot: string,
  stagedPluginsRoot: string,
): Promise<void> {
  const source = resolve(sourcePluginsRoot);
  const dest = resolve(stagedPluginsRoot);
  // Rename the previous tree away before copying. On fuseblk/NTFS, deleting
  // files still open by a running packaged app can leave hidden tombstones and
  // make recursive rmdir fail with ENOTEMPTY. A same-volume rename is atomic
  // and lets the next package start from a clean destination.
  if (await pathExists(dest)) {
    const staleRoot = join(dirname(dest), ".stale");
    await mkdir(staleRoot, { recursive: true });
    const staleDest = join(staleRoot, `${basename(dest)}-${Date.now()}`);
    await rename(dest, staleDest);
    const staleEntries = await readdir(staleRoot).catch(() => [] as string[]);
    await Promise.all(
      staleEntries
        .filter((entry) => entry !== basename(staleDest))
        .map((entry) => rm(join(staleRoot, entry), { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }).catch(() => undefined)),
    );
  }
  await mkdir(dest, { recursive: true });
  await cp(source, dest, {
    recursive: true,
    filter: (src) => !isExcludedPluginPackageEntry(src),
  });
}

async function pathExists(target: string): Promise<boolean> {
  try {
    await readdir(target);
    return true;
  } catch {
    return false;
  }
}
