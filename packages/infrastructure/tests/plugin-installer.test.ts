import { describe, expect, it, beforeEach, afterEach } from "vitest";
import { mkdtemp, mkdir, rm, writeFile, access } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { PluginInstaller } from "../src/plugins/plugin-installer.js";

const NOTES_MANIFEST = {
  id: "nusashell.notes",
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

  describe("installFromPath (directory)", () => {
    it("installs a headless plugin without ui", async () => {
      const src = await mkdtemp(join(tmpdir(), "nusashell-src-"));
      try {
        await writeFile(join(src, "manifest.json"), JSON.stringify({
          id: "nusashell.indexer",
          name: "Indexer",
          version: "1.0.0",
          icon: "🧩",
          mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"] },
        }));
        await mkdir(join(src, "mcp"), { recursive: true });
        await writeFile(join(src, "mcp", "server.js"), "// stub");

        const installer = new PluginInstaller(pluginsRoot);
        const result = await installer.installFromPath(src);

        expect(result.pluginId).toBe("nusashell.indexer");
        await expect(access(join(pluginsRoot, "nusashell.indexer", "manifest.json"))).resolves.toBeUndefined();
      } finally {
        await rm(src, { recursive: true, force: true });
      }
    });

    it("fails install before copy when ui.entry is declared but missing", async () => {
      const src = await mkdtemp(join(tmpdir(), "nusashell-src-"));
      try {
        await writeFile(join(src, "manifest.json"), JSON.stringify({
          id: "nusashell.broken",
          name: "Broken",
          version: "1.0.0",
          icon: "📝",
          ui: { entry: "ui/index.html" },
          mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"] },
        }));
        await mkdir(join(src, "mcp"), { recursive: true });
        await writeFile(join(src, "mcp", "server.js"), "// stub");

        const installer = new PluginInstaller(pluginsRoot);
        await expect(installer.installFromPath(src)).rejects.toThrow(/ui\.entry not found/);

        // Nothing should have been copied.
        await expect(access(join(pluginsRoot, "nusashell.broken"))).rejects.toThrow();
      } finally {
        await rm(src, { recursive: true, force: true });
      }
    });

    it("fails install when a local file icon is missing", async () => {
      const src = await mkdtemp(join(tmpdir(), "nusashell-src-"));
      try {
        await writeFile(join(src, "manifest.json"), JSON.stringify({
          id: "nusashell.noicon",
          name: "NoIcon",
          version: "1.0.0",
          icon: "file://icon.png",
          ui: { entry: "ui/index.html" },
          mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"] },
        }));
        await mkdir(join(src, "ui"), { recursive: true });
        await writeFile(join(src, "ui", "index.html"), "<html></html>");
        await mkdir(join(src, "mcp"), { recursive: true });
        await writeFile(join(src, "mcp", "server.js"), "// stub");

        const installer = new PluginInstaller(pluginsRoot);
        await expect(installer.installFromPath(src)).rejects.toThrow(/icon not found/);
      } finally {
        await rm(src, { recursive: true, force: true });
      }
    });

    it("installs a UI plugin when ui.entry and local icon exist", async () => {
      const src = await mkdtemp(join(tmpdir(), "nusashell-src-"));
      try {
        await writeFile(join(src, "manifest.json"), JSON.stringify({
          id: "nusashell.notes",
          name: "Notes",
          version: "1.0.0",
          icon: "file://icon.png",
          ui: { entry: "ui/index.html" },
          mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"] },
        }));
        await mkdir(join(src, "ui"), { recursive: true });
        await writeFile(join(src, "ui", "index.html"), "<html></html>");
        await writeFile(join(src, "icon.png"), Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
        await mkdir(join(src, "mcp"), { recursive: true });
        await writeFile(join(src, "mcp", "server.js"), "// stub");

        const installer = new PluginInstaller(pluginsRoot);
        const result = await installer.installFromPath(src);
        expect(result.pluginId).toBe("nusashell.notes");
      } finally {
        await rm(src, { recursive: true, force: true });
      }
    });
  });

  describe("uninstall", () => {
    it("removes a plugin whose directory name matches its id", async () => {
      const dir = join(pluginsRoot, "nusashell.notes");
      await mkdir(dir, { recursive: true });
      await writeFile(join(dir, "manifest.json"), JSON.stringify(NOTES_MANIFEST));

      const installer = new PluginInstaller(pluginsRoot);
      await installer.uninstall("nusashell.notes");

      await expect(access(dir)).rejects.toThrow();
    });

    it("removes a plugin whose directory name differs from its id", async () => {
      const dir = join(pluginsRoot, "notes");
      await mkdir(dir, { recursive: true });
      await writeFile(join(dir, "manifest.json"), JSON.stringify(NOTES_MANIFEST));

      const installer = new PluginInstaller(pluginsRoot);
      await installer.uninstall("nusashell.notes");

      await expect(access(dir)).rejects.toThrow();
    });

    it("throws when no matching plugin directory exists", async () => {
      const installer = new PluginInstaller(pluginsRoot);
      await expect(installer.uninstall("example.missing")).rejects.toThrow(
        /Plugin directory not found/,
      );
    });
  });
});
