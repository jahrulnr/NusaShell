import { readFile, realpath, stat } from "node:fs/promises";
import { isAbsolute, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const MAX_PLUGIN_ICON_BYTES = 5 * 1024 * 1024;
const PNG_SIGNATURE = Buffer.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
]);

export async function loadPluginPngDataUrl(
  source: string,
  installPath: string,
): Promise<string> {
  const sourceUrl = new URL(source);
  if (sourceUrl.protocol !== "file:") {
    throw new Error("Plugin icon must use a file URL");
  }

  // `file://icon.png` (relative to the plugin dir) and `file:///abs/icon.png`
  // (absolute) are both documented manifest forms. `new URL` parses the
  // relative form with a non-empty host and empty path, which `fileURLToPath`
  // rejects, so resolve relative sources against the install path ourselves.
  const rawPath = source.slice("file://".length);
  const iconPathInput = rawPath.startsWith("/")
    ? fileURLToPath(sourceUrl)
    : resolve(installPath, rawPath);

  const [pluginRoot, iconPath] = await Promise.all([
    realpath(installPath),
    realpath(iconPathInput),
  ]);
  const pathWithinPlugin = relative(pluginRoot, iconPath);
  if (
    pathWithinPlugin === "" ||
    pathWithinPlugin.startsWith("..") ||
    isAbsolute(pathWithinPlugin)
  ) {
    throw new Error("Plugin icon must be inside the plugin folder");
  }

  const iconStat = await stat(iconPath);
  if (!iconStat.isFile() || iconStat.size > MAX_PLUGIN_ICON_BYTES) {
    throw new Error("Plugin icon must be a PNG file no larger than 5 MiB");
  }

  const contents = await readFile(iconPath);
  if (
    contents.length < PNG_SIGNATURE.length ||
    !contents.subarray(0, PNG_SIGNATURE.length).equals(PNG_SIGNATURE)
  ) {
    throw new Error("Plugin icon is not a valid PNG");
  }

  return `data:image/png;base64,${contents.toString("base64")}`;
}
