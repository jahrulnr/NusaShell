import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  isExcludedPluginPackageEntry,
  stagePluginsResource,
} from "../scripts/stage-plugins-resource";

const temporaryDirectories: string[] = [];

async function temporaryDirectory(prefix: string): Promise<string> {
  const { mkdtemp } = await import("node:fs/promises");
  const directory = await mkdtemp(join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

afterEach(async () => {
  await Promise.all(
    temporaryDirectories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })),
  );
});

describe("plugin package staging", () => {
  it("excludes notes.json basename as packaged plugin runtime state", () => {
    expect(isExcludedPluginPackageEntry("notes.json")).toBe(true);
    expect(isExcludedPluginPackageEntry("/plugins/notes/notes.json")).toBe(true);
    expect(isExcludedPluginPackageEntry("manifest.json")).toBe(false);
    expect(isExcludedPluginPackageEntry("mcp/server.cjs")).toBe(false);
  });

  it("excludes plugin test trees and vitest configs", () => {
    expect(isExcludedPluginPackageEntry("/plugins/notes/tests/tools.test.js")).toBe(true);
    expect(isExcludedPluginPackageEntry("/plugins/notes/vitest.config.ts")).toBe(true);
    expect(isExcludedPluginPackageEntry("/plugins/kanban/node_modules/.vite/results.json")).toBe(true);
  });

  it("copies plugin files but drops runtime state and tests", async () => {
    const source = await temporaryDirectory("nusashell-plugins-src-");
    const dest = await temporaryDirectory("nusashell-plugins-dst-");
    await mkdir(join(source, "notes", "mcp"), { recursive: true });
    await mkdir(join(source, "notes", "tests"), { recursive: true });
    await writeFile(join(source, "notes", "manifest.json"), "{}\n");
    await writeFile(join(source, "notes", "mcp", "server.cjs"), "module.exports = {};\n");
    await writeFile(join(source, "notes", "vitest.config.ts"), "export default {};\n");
    await writeFile(join(source, "notes", "tests", "tools.test.js"), "export {};\n");
    await writeFile(join(source, "notes", "notes.json"), JSON.stringify({
      notes: [{ id: 1, text: "Hello from E2E" }],
    }));

    await stagePluginsResource(source, dest);

    await expect(readFile(join(dest, "notes", "manifest.json"), "utf8")).resolves.toBe("{}\n");
    await expect(readFile(join(dest, "notes", "mcp", "server.cjs"), "utf8")).resolves.toContain("module.exports");
    await expect(readFile(join(dest, "notes", "notes.json"), "utf8")).rejects.toMatchObject({
      code: "ENOENT",
    });
    await expect(readFile(join(dest, "notes", "vitest.config.ts"), "utf8")).rejects.toMatchObject({
      code: "ENOENT",
    });
    await expect(readFile(join(dest, "notes", "tests", "tools.test.js"), "utf8")).rejects.toMatchObject({
      code: "ENOENT",
    });
  });
});
