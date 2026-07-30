import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";

/**
 * Resolves the root directory for file operations.
 *
 * Source: NUSASHELL_FILES_ROOT env var, or user home directory as fallback.
 * The root must exist and be a directory.
 *
 * No path sandboxing is performed. All file operations resolve paths
 * relative to this root but do NOT restrict access to it. This is a
 * known limitation documented in the plugin docs.
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
  return resolved;
}
