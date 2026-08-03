import { delimiter as pathDelimiter, dirname, isAbsolute } from "node:path";

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
 * - Prepend `dirname(absoluteCommand)` to PATH when the command is absolute
 * - Preserve caller-supplied env (plugin PATH wins over process PATH for keys
 *   already set; dirname is always prepended ahead of the effective PATH)
 */
export function enrichSpawnEnv(
  command: string,
  env: Readonly<Record<string, string | undefined>>,
): Record<string, string> {
  const next: Record<string, string> = {};
  for (const [key, value] of Object.entries(env)) {
    if (value !== undefined) next[key] = value;
  }
  const commandDir = commandDirForPath(command);
  if (commandDir) {
    next.PATH = mergePathSegments(commandDir, next.PATH, process.env.PATH);
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
