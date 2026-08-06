/**
 * Move previous electron-forge package output out of the way before packaging.
 *
 * On fuseblk/NTFS (common for external/shared Linux mounts), deleting files that
 * are still open (e.g. a running NusaShell that still holds app.asar) leaves
 * `.fuse_hidden*` tombstones. electron-forge then fails with:
 *   ENOTEMPTY: directory not empty, rmdir '.../resources'
 *
 * Renaming the whole package dir succeeds even when some files stay open; forge
 * can write a fresh tree at the original path. Stale dirs under out/.stale/ are
 * best-effort cleaned afterward.
 */
import { mkdir, readdir, rename, rm, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import { platform, arch as nodeArch } from "node:os";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";

const desktopRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const outRoot = join(desktopRoot, "out");

export function forgeArch(): string {
  const a = process.env.npm_config_arch || nodeArch();
  if (a === "x86_64" || a === "amd64") return "x64";
  if (a === "aarch64") return "arm64";
  return a;
}

export function forgePlatform(): string {
  return process.env.npm_config_platform || platform();
}

/** electron-forge packager output folder name: NusaShell-<platform>-<arch> */
export function packageOutputDirName(
  osPlatform = forgePlatform(),
  cpuArch = forgeArch(),
): string {
  return `NusaShell-${osPlatform}-${cpuArch}`;
}

export async function pathExists(target: string): Promise<boolean> {
  try {
    await stat(target);
    return true;
  } catch {
    return false;
  }
}

/**
 * Rename package output aside and prune older .stale entries.
 * Returns the path moved to stale, or null when nothing was present.
 */
export async function clearPackageOutput(options?: {
  readonly outRoot?: string;
  readonly packageDirName?: string;
  readonly now?: number;
}): Promise<string | null> {
  const root = resolve(options?.outRoot ?? outRoot);
  const dirName = options?.packageDirName ?? packageOutputDirName();
  const packageDir = join(root, dirName);
  if (!(await pathExists(packageDir))) return null;

  const staleRoot = join(root, ".stale");
  await mkdir(staleRoot, { recursive: true });
  const stamp = options?.now ?? Date.now();
  const destination = join(staleRoot, `${dirName}-${stamp}`);
  await rename(packageDir, destination);

  // Best-effort: drop older stale trees (keep the one we just moved for debug).
  const entries = await readdir(staleRoot).catch(() => [] as string[]);
  await Promise.all(
    entries
      .filter((name) => name !== `${dirName}-${stamp}`)
      .map(async (name) => {
        const candidate = join(staleRoot, name);
        await rm(candidate, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }).catch(() => {
          // Locked fuse_hidden trees may remain until the process exits — ignore.
        });
      }),
  );

  return destination;
}

const isMain =
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (isMain) {
  const moved = await clearPackageOutput();
  if (moved) {
    console.log(`Cleared previous package output → ${moved}`);
  } else {
    console.log("No previous package output to clear.");
  }
}
