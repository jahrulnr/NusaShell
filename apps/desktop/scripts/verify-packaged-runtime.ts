import { listPackage } from "@electron/asar";
import { access, readdir } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { missingPackagedRuntimeFiles } from "./package-runtime-dependencies.js";

const desktopRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const packagedAppPath = join(
  desktopRoot,
  "out",
  `NusaShell-${process.platform}-${process.arch}`,
);

async function findFiles(
  root: string,
  matches: (fileName: string) => boolean,
): Promise<string[]> {
  const entries = await readdir(root, { withFileTypes: true });
  const results = await Promise.all(
    entries.map(async (entry) => {
      const entryPath = join(root, entry.name);
      if (entry.isDirectory()) {
        return findFiles(entryPath, matches);
      }
      return matches(entry.name) ? [entryPath] : [];
    }),
  );

  return results.flat();
}

const archives = await findFiles(packagedAppPath, (fileName) => fileName === "app.asar");
if (archives.length !== 1) {
  throw new Error(
    `Expected exactly one app.asar under ${packagedAppPath}, found ${archives.length}`,
  );
}

const archivePath = archives[0]!;
const resourcesPath = dirname(archivePath);
const requiredAgentResources = [
  join(resourcesPath, "agent", "prompts", "system.md"),
  join(resourcesPath, "agent", "prompts", "developer.md"),
  join(resourcesPath, "agent", "docs", "getting-started.md"),
];
const missingAgentResources = (
  await Promise.all(requiredAgentResources.map(async (resourcePath) => {
    try {
      await access(resourcePath);
      return undefined;
    } catch {
      return resourcePath;
    }
  }))
).filter((resourcePath) => resourcePath !== undefined);
if (missingAgentResources.length > 0) {
  throw new Error(
    `Packaged app is missing required agent resources: ${missingAgentResources.join(", ")}`,
  );
}

const archiveFiles = listPackage(archivePath, { isPack: false });
const missingRuntimeFiles = missingPackagedRuntimeFiles(archiveFiles);
if (missingRuntimeFiles.length > 0) {
  throw new Error(
    `Packaged app.asar is missing required runtime files: ${missingRuntimeFiles.join(", ")}`,
  );
}

const unpackedPath = `${archivePath}.unpacked`;
const nativeModules = await findFiles(
  join(unpackedPath, "node_modules", "better-sqlite3"),
  (fileName) => fileName.endsWith(".node"),
);
if (nativeModules.length === 0) {
  throw new Error(`No unpacked better-sqlite3 binary was found under ${unpackedPath}`);
}

console.log(
  `Verified ${basename(packagedAppPath)} runtime dependencies, agent resources, and ${nativeModules.length} unpacked SQLite binary/binaries.`,
);
