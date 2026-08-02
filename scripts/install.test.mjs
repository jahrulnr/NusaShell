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
  it("keeps the installed version and exactly one previous version", async () => {
    const root = await mkdtemp(join(tmpdir(), "nusashell-installer-prune-"));
    temporaryDirectories.push(root);
    const versions = join(root, "versions");
    await mkdir(join(versions, "0.1.6"), { recursive: true });
    await mkdir(join(versions, "0.1.7"), { recursive: true });
    await mkdir(join(versions, "0.1.9"), { recursive: true });
    await symlink(join(versions, "0.1.6"), join(root, "current"));

    const prune = `
      set -euo pipefail
      root="$1"; versions="$root/versions"; current="$root/current"; resolved_version="$2"
      previous_target="$(readlink -f "$current")"
      previous_version="$(basename "$previous_target")"
      ln -sfn "$versions/$resolved_version" "$root/.current-$resolved_version"
      mv -Tf "$root/.current-$resolved_version" "$current"
      for candidate in "$versions"/*; do
        [[ -d "$candidate" && ! -L "$candidate" ]] || continue
        candidate_version="$(basename "$candidate")"
        if [[ "$candidate_version" != "$resolved_version" && "$candidate_version" != "$previous_version" ]]; then rm -rf "$candidate"; fi
      done
    `;
    await execFileAsync("bash", ["-c", prune, "prune", root, "0.1.7"]);
    await expect(realpath(join(root, "current"))).resolves.toBe(join(versions, "0.1.7"));
    await expect(realpath(join(versions, "0.1.6"))).resolves.toBe(join(versions, "0.1.6"));
    await expect(realpath(join(versions, "0.1.9"))).rejects.toThrow();

    await mkdir(join(versions, "0.1.9"), { recursive: true });
    await execFileAsync("bash", ["-c", prune, "prune", root, "0.1.9"]);
    await expect(realpath(join(root, "current"))).resolves.toBe(join(versions, "0.1.9"));
    await expect(realpath(join(versions, "0.1.7"))).resolves.toBe(join(versions, "0.1.7"));
    await expect(realpath(join(versions, "0.1.6"))).rejects.toThrow();
  });

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
    expect(installer).toContain('previous_version="$(basename "$previous_target")"');
    expect(installer).toContain('rm -rf "$candidate"');
    const windowsInstaller = await readFile(new URL("./install.ps1", import.meta.url), "utf8");
    expect(windowsInstaller).toContain("$previousVersion");
    expect(windowsInstaller).toContain("Remove-Item -Recurse -Force");
  });
});
