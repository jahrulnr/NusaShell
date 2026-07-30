import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";

/**
 * Resolves the root directory for file operations.
 *
 * Source: NUSASHELL_FILES_ROOT env var, or user home directory as fallback.
 * The root must exist and be a directory.
 *
 * All file operations are sandboxed to this root. Paths that escape
 * the root (via absolute paths or ../ traversal) are rejected.
 *
 * @param {NodeJS.ProcessEnv | Record<string, string | undefined>} environment
 */
export async function loadRootFromEnvironment(environment = process.env) {
  const raw = environment.NUSASHELL_FILES_ROOT;
  const root = raw ? path.resolve(raw) : os.homedir();

  try {
    const stat = await fs.stat(root);
    if (!stat.isDirectory()) {
      throw new Error(`Files root is not a directory: ${root}`);
    }
  } catch (error) {
    if (error.code === "ENOENT") {
      throw new Error(`Files root does not exist: ${root}`);
    }
    throw error;
  }

  return root;
}

/**
 * Resolves a path relative to the root directory.
 * @param {string} root
 * @param {string} input
 */
export function resolvePath(root, input) {
  if (!input || input === "/" || input === "") return root;
  const resolved = path.isAbsolute(input) ? input : path.resolve(root, input);
  const normalizedRoot = path.resolve(root);
  const relative = path.relative(normalizedRoot, resolved);
  if (relative.startsWith("..") || path.isAbsolute(relative)) {
    throw new Error(`Path escapes files root: ${input}`);
  }
  return resolved;
}
