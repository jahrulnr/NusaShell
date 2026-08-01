import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  missingPackagedRuntimeFiles,
  REQUIRED_RUNTIME_FILES,
  stageRuntimeDependencies,
} from "../scripts/package-runtime-dependencies";

const temporaryDirectories: string[] = [];

async function temporaryDirectory(prefix: string): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(async () => {
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe("desktop runtime dependency packaging", () => {
  it("recognizes Windows-style paths returned by the ASAR reader", () => {
    const windowsArchiveFiles = REQUIRED_RUNTIME_FILES.map(
      (runtimeFile) => `\\node_modules\\${runtimeFile.replaceAll("/", "\\")}`,
    );

    expect(missingPackagedRuntimeFiles(windowsArchiveFiles)).toEqual([]);
  });

  it("copies a standalone production dependency tree into the Forge build", async () => {
    const buildPath = await temporaryDirectory("nusashell-forge-build-");

    await stageRuntimeDependencies({
      buildPath,
      deploy: async (deployPath) => {
        for (const runtimeFile of REQUIRED_RUNTIME_FILES) {
          const target = join(deployPath, "node_modules", runtimeFile);
          await mkdir(join(target, ".."), { recursive: true });
          await writeFile(target, "fixture\n");
        }
      },
    });

    for (const runtimeFile of REQUIRED_RUNTIME_FILES) {
      await expect(
        import("node:fs/promises").then(({ access }) =>
          access(join(buildPath, "node_modules", runtimeFile)),
        ),
      ).resolves.toBeUndefined();
    }
  });

  it("fails packaging when the deployed dependency tree is incomplete", async () => {
    const buildPath = await temporaryDirectory("nusashell-forge-build-");

    await expect(
      stageRuntimeDependencies({
        buildPath,
        deploy: async (deployPath) => {
          const target = join(deployPath, "node_modules", REQUIRED_RUNTIME_FILES[0]);
          await mkdir(join(target, ".."), { recursive: true });
          await writeFile(target, "fixture\n");
        },
      }),
    ).rejects.toThrow("Production deployment is missing required runtime files");
  });
});
