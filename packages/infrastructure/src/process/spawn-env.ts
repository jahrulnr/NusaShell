import { delimiter as pathDelimiter, dirname, isAbsolute, join } from "node:path";
import { homedir } from "node:os";
import { existsSync, statSync } from "node:fs";

/**
 * Expand a leading `~` or `~user` to the user's home directory.
 * Node's `child_process.spawn` does NOT perform shell expansion, so a manifest
 * command like `~/.local/bin/messager-mcp` would be treated as a literal
 * directory named `~` and fail with ENOENT.
 *
 * Only expands when `~` is at the start of the path (POSIX convention).
 * Windows has no `~` convention; the function is a no-op there.
 */
export function expandTilde(path: string | undefined): string | undefined {
  if (!path) return path;
  if (path === "~") return homedir();
  if (path.startsWith("~/")) return join(homedir(), path.slice(2));
  // ~user — resolve via os.homedir fallback (Node doesn't expose getpwnam).
  // Most manifests use ~/ ; ~user is rare and platform-dependent.
  if (path.startsWith("~")) {
    const slash = path.indexOf("/");
    if (slash === -1) return path; // bare ~user without trailing path
    return path; // leave ~user/... as-is; Node can't resolve other users
  }
  return path;
}

/**
 * Expand `~` in every PATH segment. Returns the expanded PATH string.
 */
export function expandTildeInPath(pathValue: string | undefined): string | undefined {
  if (!pathValue) return pathValue;
  return pathValue
    .split(pathDelimiter)
    .map((segment) => expandTilde(segment) ?? segment)
    .join(pathDelimiter);
}

/**
 * Common user bin directories that GUI-launched Electron may not have on PATH.
 * GUI launches (Dock, Finder, .desktop) don't source `.bashrc`/`.zshrc`, so
 * binaries installed by `pip install --user`, `cargo install`, `go install`,
 * nvm/fnm, etc. are invisible to spawned MCP servers.
 *
 * Only directories that actually exist on disk are added.
 */
const COMMON_USER_BIN_DIRS = [
  "~/.local/bin",
  "~/bin",
  "~/.cargo/bin",
  "~/.npm-global/bin",
  "~/.bun/bin",
  "~/.deno/bin",
  "~/.volta/bin",
  "~/.nvm/current/bin", // nvm symlink (common setup)
  "~/.fnm/current/bin", // fnm symlink
  "~/.yarn/bin",
  "~/.pnpm",
];

/**
 * Detect existing user bin directories and return their absolute (tilde-expanded)
 * paths, in priority order. Used to enrich PATH for GUI-launched processes.
 */
export function discoverUserBinDirs(): string[] {
  const found: string[] = [];
  for (const dir of COMMON_USER_BIN_DIRS) {
    const expanded = expandTilde(dir);
    if (!expanded || expanded === dir) continue; // tilde didn't expand
    try {
      if (existsSync(expanded) && statSync(expanded).isDirectory()) {
        found.push(expanded);
      }
    } catch {
      // stat failed — skip silently
    }
  }
  return found;
}

/**
 * Merge PATH-like segments, preserving order and dropping empty/duplicate entries.
 * Uses the host PATH delimiter (`:` on POSIX, `;` on Windows).
 */
export function mergePathSegments(
  ...parts: Array<string | undefined | null>
): string {
  return mergePathSegmentsWith(pathDelimiter, ...parts);
}

/** Same as {@link mergePathSegments} with an explicit delimiter (for cross-platform tests). */
export function mergePathSegmentsWith(
  delimiter: string,
  ...parts: Array<string | undefined | null>
): string {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const part of parts) {
    if (!part) continue;
    for (const segment of part.split(delimiter)) {
      const trimmed = segment.trim();
      if (!trimmed || seen.has(trimmed)) continue;
      seen.add(trimmed);
      out.push(trimmed);
    }
  }
  return out.join(delimiter);
}

/**
 * When `command` is an absolute path, return its directory so shebang scripts
 * (e.g. nvm's `npx` → `/usr/bin/env node`) can find sibling binaries.
 */
export function commandDirForPath(command: string): string | undefined {
  if (!command || !isAbsolute(command)) return undefined;
  const dir = dirname(command);
  return dir && dir !== "." ? dir : undefined;
}

/**
 * Enrich spawn env for a child process:
 * - Expand `~` in PATH entries (manifest env may use `~/.local/bin:...`)
 * - Prepend `dirname(absoluteCommand)` to PATH when the command is absolute
 * - Prepend common user bin directories (`~/.local/bin`, `~/bin`, etc.) so
 *   GUI-launched Electron can find user-installed binaries that are normally
 *   only on PATH after sourcing `.bashrc`/`.zshrc`
 * - Preserve caller-supplied env (plugin PATH wins over process PATH for keys
 *   already set; dirname + user bins are always prepended ahead of the effective PATH)
 */
export function enrichSpawnEnv(
  command: string,
  env: Readonly<Record<string, string | undefined>>,
): Record<string, string> {
  const next: Record<string, string> = {};
  for (const [key, value] of Object.entries(env)) {
    if (value !== undefined) {
      next[key] = key === "PATH" ? (expandTildeInPath(value) ?? value) : value;
    }
  }
  const commandDir = commandDirForPath(command);
  const userBins = discoverUserBinDirs();
  if (commandDir || userBins.length > 0) {
    next.PATH = mergePathSegments(
      commandDir,
      ...userBins,
      next.PATH,
      process.env.PATH,
    );
  }
  return next;
}

/**
 * Actionable hint when spawn fails with ENOENT (missing binary on PATH).
 */
export function formatSpawnEnoentHint(command: string): string {
  const base = `Command not found on PATH: ${command}`;
  if (/(^|[\\/])npx(\.cmd)?$/i.test(command) || command === "npx") {
    return `${base}. GUI launches often lack nvm/fnm Node; set an absolute command or ensure Node's bin is on PATH (login shell).`;
  }
  return `${base}. Use an absolute path or ensure the binary is on PATH.`;
}
