import { access, cp, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { execFile } from "node:child_process";

const execFileAsync = promisify(execFile);
const workspaceRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");

export const REQUIRED_RUNTIME_FILES = [
  "ws/package.json",
  "electron-updater/package.json",
  "better-sqlite3/package.json",
  "ajv/dist/runtime/equal.js",
  "ajv-formats/dist/formats.js",
] as const;

export function missingPackagedRuntimeFiles(archiveFiles: Iterable<string>): string[] {
  const normalizedArchiveFiles = new Set(
    [...archiveFiles].map((archiveFile) => archiveFile.replaceAll("\\", "/")),
  );

  return REQUIRED_RUNTIME_FILES.filter(
    (runtimeFile) => !normalizedArchiveFiles.has(`/node_modules/${runtimeFile}`),
  );
}

type Deploy = (deployPath: string) => Promise<void>;

export interface StageRuntimeDependenciesOptions {
  readonly buildPath: string;
  readonly deploy?: Deploy;
}

async function deployProductionDependencies(deployPath: string): Promise<void> {
  const args = [
    "--config.inject-workspace-packages=true",
    "--filter",
    "@nusashell/desktop",
    "deploy",
    "--prod",
    deployPath,
  ];
  const npmExecPath = process.env.npm_execpath;

  if (npmExecPath) {
    await execFileAsync(process.execPath, [npmExecPath, ...args], {
      cwd: workspaceRoot,
      env: { ...process.env, CI: process.env.CI ?? "true" },
    });
    return;
  }

  await execFileAsync(process.platform === "win32" ? "pnpm.cmd" : "pnpm", args, {
    cwd: workspaceRoot,
    env: { ...process.env, CI: process.env.CI ?? "true" },
    shell: process.platform === "win32",
  });
}

async function missingRuntimeFiles(nodeModulesPath: string): Promise<string[]> {
  const results = await Promise.all(
    REQUIRED_RUNTIME_FILES.map(async (runtimeFile) => {
      try {
        await access(join(nodeModulesPath, runtimeFile));
        return undefined;
      } catch {
        return runtimeFile;
      }
    }),
  );

  return results.filter((runtimeFile) => runtimeFile !== undefined);
}

export async function stageRuntimeDependencies({
  buildPath,
  deploy = deployProductionDependencies,
}: StageRuntimeDependenciesOptions): Promise<void> {
  const deployPath = await mkdtemp(join(tmpdir(), "nusashell-desktop-deploy-"));

  try {
    await deploy(deployPath);

    const deployedNodeModules = join(deployPath, "node_modules");
    const missing = await missingRuntimeFiles(deployedNodeModules);
    if (missing.length > 0) {
      throw new Error(
        `Production deployment is missing required runtime files: ${missing.join(", ")}`,
      );
    }

    const packagedNodeModules = join(buildPath, "node_modules");
    await cp(deployedNodeModules, packagedNodeModules, { recursive: true, force: true });

    const missingAfterCopy = await missingRuntimeFiles(packagedNodeModules);
    if (missingAfterCopy.length > 0) {
      throw new Error(
        `Packaged app is missing required runtime files: ${missingAfterCopy.join(", ")}`,
      );
    }
  } finally {
    await rm(deployPath, { recursive: true, force: true });
  }
}
