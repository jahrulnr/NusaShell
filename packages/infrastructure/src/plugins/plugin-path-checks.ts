import { stat } from "node:fs/promises";
import { resolve, relative, isAbsolute } from "node:path";

/**
 * Resolve a manifest-declared relative path against a plugin directory,
 * enforcing that the target stays inside the plugin folder. Throws when the
 * path is absolute or escapes the plugin root.
 */
export function resolveInsidePluginDir(dir: string, relPath: string, field: string): string {
  if (isAbsolute(relPath)) {
    throw new Error(`Invalid plugin package: ${field} points outside its install directory: ${relPath}`);
  }
  const root = resolve(dir);
  const target = resolve(root, relPath);
  const pathFromRoot = relative(root, target);
  if (!pathFromRoot || pathFromRoot.startsWith("..") || isAbsolute(pathFromRoot)) {
    throw new Error(`Invalid plugin package: ${field} points outside its install directory: ${relPath}`);
  }
  return target;
}

/**
 * Whether a manifest icon string refers to a plugin-relative local file that
 * should exist on disk. Emoji/text, HTTP(S) URLs, and absolute `file:///`
 * URLs are not checked (absolute file URLs may intentionally point outside).
 */
export function isLocalIconPath(icon: string): boolean {
  if (!icon) return false;
  if (/^(?:https?:\/\/|file:\/\/\/)/i.test(icon)) return false;
  if (/^file:\/\//i.test(icon)) return true;
  if (/^\.?\.?[/\\]/.test(icon)) return true;
  if (/^[A-Za-z]:[\\/]/.test(icon)) return true;
  if (/\.[a-zA-Z0-9]{1,8}$/.test(icon) && !/\s/.test(icon)) return true;
  return false;
}

/** Strip a leading `file://` scheme from a relative icon path. */
export function stripFileScheme(icon: string): string {
  return icon.replace(/^file:\/\//i, "");
}

/**
 * Verify that `ui.entry` (when declared) and a local icon file exist inside
 * the plugin directory. Throws an `Invalid plugin package: …` Error when a
 * declared file is missing or escapes the plugin root. Headless plugins
 * (no `ui`) skip the entry check; emoji/HTTP/absolute-file icons skip the
 * icon check.
 */
export async function assertDeclaredFilesExist(
  dir: string,
  manifest: { readonly ui?: { readonly entry?: string } | undefined; readonly icon: string },
): Promise<void> {
  if (manifest.ui?.entry) {
    const entryPath = resolveInsidePluginDir(dir, manifest.ui.entry, "ui.entry");
    await assertFileExists(entryPath, `ui.entry not found: ${manifest.ui.entry}`);
  }
  if (isLocalIconPath(manifest.icon)) {
    const relIcon = stripFileScheme(manifest.icon);
    const iconPath = resolveInsidePluginDir(dir, relIcon, "icon");
    await assertFileExists(iconPath, `icon not found: ${manifest.icon}`);
  }
}

async function assertFileExists(path: string, message: string): Promise<void> {
  try {
    const info = await stat(path);
    if (!info.isFile()) {
      throw new Error(`Invalid plugin package: ${message} (not a file)`);
    }
  } catch (err) {
    if (err instanceof Error && err.message.startsWith("Invalid plugin package:")) throw err;
    throw new Error(`Invalid plugin package: ${message}`);
  }
}
