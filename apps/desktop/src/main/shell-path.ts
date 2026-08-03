import { execFile } from "node:child_process";
import { delimiter as hostPathDelimiter } from "node:path";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

export interface EnrichProcessPathOptions {
  readonly platform?: NodeJS.Platform;
  readonly shell?: string;
  readonly timeoutMs?: number;
  readonly env?: NodeJS.ProcessEnv;
  readonly readLoginPath?: () => Promise<string | undefined>;
}

function pathDelimiterFor(platform: NodeJS.Platform): string {
  return platform === "win32" ? ";" : ":";
}

/**
 * Merge PATH-like segments, preserving order and dropping empty/duplicate entries.
 * Defaults to the host PATH delimiter; pass `delimiter` when simulating another OS.
 */
export function mergePathSegments(
  ...parts: Array<string | undefined | null>
): string {
  return mergePathSegmentsWith(hostPathDelimiter, ...parts);
}

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
 * Merge the user's login-shell PATH into `process.env.PATH` so GUI-launched
 * Electron can find nvm/fnm tools (`npx`, `node`, `agent`, …). Best-effort:
 * never throws; no-ops on Windows or when the login shell cannot be queried.
 */
export async function enrichProcessPathFromLoginShell(
  options: EnrichProcessPathOptions = {},
): Promise<{ readonly enriched: boolean; readonly path: string }> {
  const platform = options.platform ?? process.platform;
  const env = options.env ?? process.env;
  const current = env.PATH ?? "";

  if (platform === "win32") {
    return { enriched: false, path: current };
  }

  try {
    const loginPath = options.readLoginPath
      ? await options.readLoginPath()
      : await readLoginShellPath({
          shell: options.shell ?? env.SHELL ?? "/bin/bash",
          timeoutMs: options.timeoutMs ?? 5_000,
          env,
        });
    if (!loginPath?.trim()) {
      return { enriched: false, path: current };
    }
    const merged = mergePathSegmentsWith(pathDelimiterFor(platform), current, loginPath);
    if (merged === current) {
      return { enriched: false, path: current };
    }
    env.PATH = merged;
    return { enriched: true, path: merged };
  } catch {
    return { enriched: false, path: current };
  }
}

async function readLoginShellPath(input: {
  readonly shell: string;
  readonly timeoutMs: number;
  readonly env: NodeJS.ProcessEnv;
}): Promise<string | undefined> {
  const { stdout } = await execFileAsync(
    input.shell,
    ["-ilc", 'printf %s "$PATH"'],
    {
      timeout: input.timeoutMs,
      env: input.env,
      maxBuffer: 1024 * 1024,
    },
  );
  const path = String(stdout ?? "").trim();
  return path || undefined;
}
