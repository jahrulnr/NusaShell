import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";

/**
 * Resolve a plugin manifest icon string to a usable source value.
 *
 * Accepted formats:
 * 1. Text / emoji — e.g. "📝" or "N" → returned as-is
 * 2. Relative file path — e.g. "file://icon.png" or "./assets/icon.png"
 *    → resolved against the plugin's installPath to an absolute file:// URL
 * 3. Absolute file path — e.g. "file:///abs/path/icon.png" → returned as-is
 * 4. HTTP(S) URL — e.g. "https://example.com/icon.png" → returned as-is
 */
export function resolveIcon(icon: string, installPath: string): string {
  const trimmed = icon.trim();
  if (trimmed.length === 0) return trimmed;

  // HTTP(S) URL — pass through
  if (/^https?:\/\//i.test(trimmed)) return trimmed;

  // Absolute file:// URL — pass through
  if (/^file:\/\/\//i.test(trimmed)) return trimmed;

  // Relative file:// path — resolve against installPath
  // e.g. "file://icon.png" or "file://assets/icon.png"
  const fileRelMatch = trimmed.match(/^file:\/\/(.+)$/i);
  if (fileRelMatch) {
    const relPath = fileRelMatch[1];
    return pathToFileURL(resolve(installPath, relPath)).href;
  }

  // Relative path without scheme — resolve against installPath
  // e.g. "./icon.png", "assets/icon.png", "icon.png"
  if (/^\.?\//.test(trimmed) || /^\.?\.\\/.test(trimmed) || /^[A-Za-z]:[\\/]/.test(trimmed)) {
    return pathToFileURL(resolve(installPath, trimmed)).href;
  }

  // Bare filename that looks like a file (has an extension)
  if (/\.[a-zA-Z0-9]{1,8}$/.test(trimmed) && !/\s/.test(trimmed)) {
    return pathToFileURL(resolve(installPath, trimmed)).href;
  }

  // Default: treat as text/emoji
  return trimmed;
}
