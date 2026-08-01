import { execFile } from "node:child_process";
import { mkdtemp, mkdir, readFile, realpath, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { afterEach, describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const temporaryDirectories = [];

afterEach(async () => {
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) =>
      rm(directory, { recursive: true, force: true }),
    ),
  );
});

describe.runIf(process.platform === "linux")("Linux installer version activation", () => {
  it("atomically replaces a current symlink that already points to a directory", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-installer-current-"));
    temporaryDirectories.push(root);
    const oldTarget = join(root, "versions", "0.1.0");
    const newTarget = join(root, "versions", "0.1.1");
    const current = join(root, "current");
    await mkdir(oldTarget, { recursive: true });
    await mkdir(newTarget, { recursive: true });
    await symlink(oldTarget, current);

    await execFileAsync(
      "bash",
      [
        "-c",
        'ln -sfn "$2" "$1/.current-$3"; mv -Tf "$1/.current-$3" "$1/current"',
        "activate-version",
        root,
        newTarget,
        "0.1.1",
      ],
    );

    await expect(realpath(current)).resolves.toBe(newTarget);
    await expect(realpath(oldTarget)).resolves.toBe(oldTarget);

    const installer = await readFile(new URL("./install.sh", import.meta.url), "utf8");
    expect(installer).toContain(
      'mv -Tf "$root/.current-$resolved_version" "$current"',
    );
  });
});
