import { copyFile, mkdir } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveLinuxDevDesktopPaths } from "../src/main/window-assets.js";

async function registerLinuxDesktopIdentity(): Promise<void> {
  if (process.platform !== "linux") return;

  const dataHome = process.env.XDG_DATA_HOME?.trim()
    || resolve(homedir(), ".local", "share");
  const targets = resolveLinuxDevDesktopPaths(dataHome);
  const assetsDir = resolve(dirname(fileURLToPath(import.meta.url)), "..", "assets");

  await Promise.all([
    mkdir(dirname(targets.desktopEntry), { recursive: true }),
    mkdir(dirname(targets.icon), { recursive: true }),
  ]);
  await Promise.all([
    copyFile(resolve(assetsDir, "nusashell.desktop"), targets.desktopEntry),
    copyFile(resolve(assetsDir, "nusashell.png"), targets.icon),
  ]);
}

await registerLinuxDesktopIdentity();
