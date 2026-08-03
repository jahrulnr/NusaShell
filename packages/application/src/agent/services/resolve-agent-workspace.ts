import os from "node:os";
import path from "node:path";

/**
 * Resolve the effective agent/subagent working directory.
 *
 * Priority: explicit override → conversation/turn workspace → user home.
 * Never falls back to `process.cwd()` — that is the Electron/backend package
 * directory in desktop runs and must not become a silent write target.
 */
export function resolveAgentWorkspace(
  override: string | undefined,
  turnWorkspace: string | undefined,
): string {
  const chosen = (override?.trim() || turnWorkspace?.trim() || "");
  if (chosen) return path.resolve(chosen);
  return os.homedir();
}
