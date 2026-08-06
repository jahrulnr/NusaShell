import { mkdir, readdir, writeFile, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { afterEach, describe, expect, it } from "vitest";
import {
  clearPackageOutput,
  packageOutputDirName,
  pathExists,
} from "../scripts/clean-package-output";

const temporaryDirectories: string[] = [];

async function temporaryDirectory(prefix: string): Promise<string> {
  const { mkdtemp } = await import("node:fs/promises");
  const directory = await mkdtemp(join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(async () => {
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) =>
      rm(directory, { recursive: true, force: true }),
    ),
  );
});

describe("clean-package-output", () => {
  it("names package dirs like electron-forge (NusaShell-platform-arch)", () => {
    expect(packageOutputDirName("linux", "x64")).toBe("NusaShell-linux-x64");
    expect(packageOutputDirName("darwin", "arm64")).toBe("NusaShell-darwin-arm64");
  });

  it("renames existing package output out of the packager path", async () => {
    const outRoot = await temporaryDirectory("nusashell-clean-pkg-");
    const dirName = "NusaShell-linux-x64";
    const packageDir = join(outRoot, dirName);
    await mkdir(join(packageDir, "resources"), { recursive: true });
    // Simulate NTFS/fuse tombstone that breaks forge rmdir.
    await writeFile(join(packageDir, "resources", ".fuse_hiddenDEADBEEF"), "still open on ntfs");
    await writeFile(join(packageDir, "NusaShell"), "binary");

    const moved = await clearPackageOutput({
      outRoot,
      packageDirName: dirName,
      now: 1_700_000_000_000,
    });

    expect(moved).toBe(join(outRoot, ".stale", `${dirName}-1700000000000`));
    await expect(pathExists(packageDir)).resolves.toBe(false);
    await expect(pathExists(moved!)).resolves.toBe(true);
    await expect(
      pathExists(join(moved!, "resources", ".fuse_hiddenDEADBEEF")),
    ).resolves.toBe(true);
  });

  it("returns null when there is nothing to clear", async () => {
    const outRoot = await temporaryDirectory("nusashell-clean-empty-");
    await expect(
      clearPackageOutput({ outRoot, packageDirName: "NusaShell-linux-x64" }),
    ).resolves.toBeNull();
    await expect(readdir(outRoot)).resolves.toEqual([]);
  });
});
