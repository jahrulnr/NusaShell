import { listPackage } from "@electron/asar";
import { access, readdir } from "node:fs/promises";
import { basename, dirname, join, resolve, sep } from "node:path";
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

// Never ship Notes runtime state written by local dev or E2E into installs.
// That data must stay under Electron userData (e.g. ~/.config/nusashell), not
// ride along `make install` into the binary tree.
const packagedNotesData = join(resourcesPath, "plugins", "notes", "notes.json");
try {
  await access(packagedNotesData);
  throw new Error(
    `Packaged plugins must not include notes runtime state: ${packagedNotesData}`,
  );
} catch (error) {
  if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
}

// First-party plugin unit-test trees must not ship (bloat + accidental fixtures).
const packagedPluginTestTrees = await findFiles(
  join(resourcesPath, "plugins"),
  (fileName) => fileName.endsWith(".test.js") || fileName.endsWith(".test.ts"),
);
const firstPartyPluginTests = packagedPluginTestTrees.filter((filePath) =>
  !filePath.includes(`${sep}node_modules${sep}`),
);
if (firstPartyPluginTests.length > 0) {
  throw new Error(
    `Packaged plugins must not include first-party tests: ${firstPartyPluginTests.slice(0, 5).join(", ")}`,
  );
}

// Verify Terminal plugin bundle and staged node-pty
const terminalBundle = join(resourcesPath, "plugins", "terminal", "mcp", "server.cjs");
try {
  await access(terminalBundle);
} catch {
  throw new Error(`Terminal plugin bundle not found at ${terminalBundle}`);
}
const terminalBundleSource = await import("node:fs/promises").then((fs) => fs.readFile(terminalBundle, "utf8"));
if (terminalBundleSource.includes('require("@modelcontextprotocol/sdk')) {
  throw new Error("Terminal plugin server.cjs has a bare SDK require — bundle is stale or unbundled");
}

const terminalNodePtyDir = join(resourcesPath, "plugins", "terminal", "node_modules", "node-pty");
const ptyBinaries = await findFiles(terminalNodePtyDir, (fileName) => fileName.endsWith(".node"));
if (ptyBinaries.length === 0) {
  throw new Error(`No node-pty .node binary found under ${terminalNodePtyDir}`);
}

console.log(
  `Verified ${basename(packagedAppPath)} runtime dependencies, agent resources, ${nativeModules.length} unpacked SQLite binary/binaries, and ${ptyBinaries.length} node-pty binary/binaries.`,
);
