import { readdir, stat } from "node:fs/promises";
import { dirname, resolve, join } from "node:path";

export interface PluginDirectoryInfo {
  readonly id: string;
  readonly path: string;
}

export async function scanPluginDirectories(
  rootDir: string,
): Promise<readonly PluginDirectoryInfo[]> {
  let entries: string[];
  try {
    entries = await readdir(rootDir);
  } catch {
    return [];
  }

  const results: PluginDirectoryInfo[] = [];

  for (const entry of entries) {
    const fullPath = join(rootDir, entry);
    let info;
    try {
      info = await stat(fullPath);
    } catch {
      continue;
    }
    if (info.isDirectory()) {
      results.push({ id: entry, path: fullPath });
    }
  }

  return results;
}

export function resolveManifestPath(pluginDir: string): string {
  return join(pluginDir, "manifest.json");
}

export function resolvePluginRoot(manifestPath: string): string {
  return dirname(resolve(manifestPath));
}
