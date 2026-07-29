import { readFile, realpath, stat } from "node:fs/promises";
import { isAbsolute, relative } from "node:path";
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

  const [pluginRoot, iconPath] = await Promise.all([
    realpath(installPath),
    realpath(fileURLToPath(sourceUrl)),
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
