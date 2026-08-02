import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { loadPluginPngDataUrl } from "../src/main/plugin-icon.js";

const PNG_SIGNATURE = Buffer.from([
  0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
]);

describe("loadPluginPngDataUrl", () => {
  it("loads a PNG inside the plugin folder as a data URL", async () => {
    const pluginRoot = await mkdtemp(join(tmpdir(), "nusashell-icon-"));
    const iconPath = join(pluginRoot, "icon.png");
    await writeFile(iconPath, Buffer.concat([PNG_SIGNATURE, Buffer.from("fixture")]));

    await expect(loadPluginPngDataUrl(
      `file://${iconPath}`,
      pluginRoot,
    )).resolves.toMatch(/^data:image\/png;base64,/);
  });

  it("loads a PNG referenced by a relative file URL inside the plugin folder", async () => {
    const pluginRoot = await mkdtemp(join(tmpdir(), "nusashell-icon-rel-"));
    const iconPath = join(pluginRoot, "icon.png");
    await writeFile(iconPath, Buffer.concat([PNG_SIGNATURE, Buffer.from("fixture")]));

    await expect(loadPluginPngDataUrl("file://icon.png", pluginRoot))
      .resolves.toMatch(/^data:image\/png;base64,/);
  });

  it("rejects files outside the plugin folder", async () => {
    const pluginRoot = await mkdtemp(join(tmpdir(), "nusashell-icon-root-"));
    const outsideRoot = await mkdtemp(join(tmpdir(), "nusashell-icon-outside-"));
    const iconPath = join(outsideRoot, "icon.png");
    await writeFile(iconPath, PNG_SIGNATURE);

    await expect(loadPluginPngDataUrl(
      `file://${iconPath}`,
      pluginRoot,
    )).rejects.toThrow("inside the plugin folder");
  });

  it("rejects a non-PNG payload", async () => {
    const pluginRoot = await mkdtemp(join(tmpdir(), "nusashell-icon-format-"));
    const iconPath = join(pluginRoot, "icon.png");
    await writeFile(iconPath, "not a png");

    await expect(loadPluginPngDataUrl(
      `file://${iconPath}`,
      pluginRoot,
    )).rejects.toThrow("valid PNG");
  });
});
