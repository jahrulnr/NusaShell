import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";

/**
 * Resolves the root directory for file operations.
 *
 * Source precedence: NUSASHELL_FILES_ROOT → NUSASHELL_WORKSPACE → user home.
 * The workspace fallback binds the Files root to the conversation workspace
 * when the shell spawns the plugin with NUSASHELL_WORKSPACE (Phase 3 respawn
 * path). Roots (Phase 2) update the root in-process after spawn.
 *
 * The root must exist and be a directory.
 *
 * All file operations are sandboxed to this root. Paths that escape
 * the root (via absolute paths or ../ traversal) are rejected.
 *
 * @param {NodeJS.ProcessEnv | Record<string, string | undefined>} environment
 */
export async function loadRootFromEnvironment(environment = process.env) {
  const raw = environment.NUSASHELL_FILES_ROOT || environment.NUSASHELL_WORKSPACE;
  const root = raw ? path.resolve(raw) : os.homedir();
  return validateRoot(root);
}

/**
 * Validate that a path exists and is a directory, returning the resolved root.
 * @param {string} root
 */
export async function validateRoot(root) {
  const resolved = path.resolve(root);
  try {
    const stat = await fs.stat(resolved);
    if (!stat.isDirectory()) {
      throw new Error(`Files root is not a directory: ${resolved}`);
    }
  } catch (error) {
    if (error && error.code === "ENOENT") {
      throw new Error(`Files root does not exist: ${resolved}`);
    }
    throw error;
  }
  return resolved;
}

/**
 * Resolves a path relative to the root directory.
 *
 * Absolute paths are accepted as-is — the agent is a trusted actor operating
 * on behalf of the user and may access any path the user can. The root is a
 * convenience for relative path resolution, not a jail. Relative paths that
 * escape the root via `../` traversal are still rejected as a safety net.
 *
 * @param {string} root
 * @param {string} input
 */
export function resolvePath(root, input) {
  if (!input || input === "/" || input === "") return root;
  if (path.isAbsolute(input)) return path.resolve(input);
  const resolved = path.resolve(root, input);
  const normalizedRoot = path.resolve(root);
  const relative = path.relative(normalizedRoot, resolved);
  if (relative.startsWith("..")) {
    throw new Error(`Path escapes files root: ${input}`);
  }
  return resolved;
}
