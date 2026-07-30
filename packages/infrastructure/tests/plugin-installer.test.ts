import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile, access } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { PluginInstaller } from "../src/plugins/plugin-installer.js";

const NOTES_MANIFEST = {
  id: "com.example.notes",
  name: "Notes",
  version: "1.0.0",
  icon: "📝",
  ui: { entry: "ui/index.html" },
  mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"], env: {} },
};

describe("PluginInstaller", () => {
  let pluginsRoot: string;

  beforeEach(async () => {
    pluginsRoot = await mkdtemp(join(tmpdir(), "nusashell-installer-"));
  });

  afterEach(async () => {
    await rm(pluginsRoot, { recursive: true, force: true });
  });

  describe("uninstall", () => {
    it("removes a plugin whose directory name matches its id", async () => {
      const dir = join(pluginsRoot, "com.example.notes");
      await mkdir(dir, { recursive: true });
      await writeFile(join(dir, "manifest.json"), JSON.stringify(NOTES_MANIFEST));

      const installer = new PluginInstaller(pluginsRoot);
      await installer.uninstall("com.example.notes");

      await expect(access(dir)).rejects.toThrow();
    });

    it("removes a plugin whose directory name differs from its id", async () => {
      const dir = join(pluginsRoot, "notes");
      await mkdir(dir, { recursive: true });
      await writeFile(join(dir, "manifest.json"), JSON.stringify(NOTES_MANIFEST));

      const installer = new PluginInstaller(pluginsRoot);
      await installer.uninstall("com.example.notes");

      await expect(access(dir)).rejects.toThrow();
    });

    it("throws when no matching plugin directory exists", async () => {
      const installer = new PluginInstaller(pluginsRoot);
      await expect(installer.uninstall("com.example.missing")).rejects.toThrow(
        /Plugin directory not found/,
      );
    });
  });
});
