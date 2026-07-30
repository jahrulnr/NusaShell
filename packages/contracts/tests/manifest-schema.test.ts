import { describe, expect, it } from "vitest";
import { ManifestSchema } from "../src/manifest/manifest-schema.js";

const VALID = {
  id: "nusashell.notes",
  name: "Notes",
  version: "1.0.0",
  icon: "📝",
  ui: { entry: "ui/index.html" },
  mcp: { transport: "stdio", command: "node", args: ["mcp/server.js"] },
};

describe("ManifestSchema", () => {
  it("accepts a valid manifest", () => {
    const result = ManifestSchema.safeParse(VALID);
    expect(result.success).toBe(true);
  });

  it("accepts manifest with optional fields", () => {
    const result = ManifestSchema.safeParse({
      ...VALID,
      ui: {
        entry: "ui/index.html",
        window: { mode: "panel", defaultSize: { width: 480, height: 560 }, resizable: true },
      },
      mcp: { transport: "stdio", command: "node", env: { FOO: "bar" }, autostart: true, keepAliveOnClose: false },
      dependencies: { shell: ">=0.1.0" },
    });
    expect(result.success).toBe(true);
  });

  it("rejects missing id", () => {
    const result = ManifestSchema.safeParse({ ...VALID, id: "" });
    expect(result.success).toBe(false);
  });

  it("rejects missing name", () => {
    const result = ManifestSchema.safeParse({ ...VALID, name: "" });
    expect(result.success).toBe(false);
  });

  it("rejects missing version", () => {
    const result = ManifestSchema.safeParse({ ...VALID, version: "" });
    expect(result.success).toBe(false);
  });

  it("rejects missing icon", () => {
    const result = ManifestSchema.safeParse({ ...VALID, icon: "" });
    expect(result.success).toBe(false);
  });

  it("accepts file path icon", () => {
    const result = ManifestSchema.safeParse({ ...VALID, icon: "file://icon.png" });
    expect(result.success).toBe(true);
  });

  it("accepts URL icon", () => {
    const result = ManifestSchema.safeParse({ ...VALID, icon: "https://example.com/icon.png" });
    expect(result.success).toBe(true);
  });

  it("accepts relative file path icon", () => {
    const result = ManifestSchema.safeParse({ ...VALID, icon: "./assets/icon.png" });
    expect(result.success).toBe(true);
  });

  it("rejects missing ui.entry", () => {
    const result = ManifestSchema.safeParse({ ...VALID, ui: { entry: "" } });
    expect(result.success).toBe(false);
  });

  it("rejects invalid transport type", () => {
    const result = ManifestSchema.safeParse({
      ...VALID,
      mcp: { transport: "websocket", command: "node" },
    });
    expect(result.success).toBe(false);
  });

  it("rejects invalid window mode", () => {
    const result = ManifestSchema.safeParse({
      ...VALID,
      ui: { entry: "ui/index.html", window: { mode: "floating" } },
    });
    expect(result.success).toBe(false);
  });

  it("accepts sse transport with url", () => {
    const result = ManifestSchema.safeParse({
      ...VALID,
      mcp: { transport: "sse", url: "http://localhost:3001/sse" },
    });
    expect(result.success).toBe(true);
  });

  it("accepts http transport with url", () => {
    const result = ManifestSchema.safeParse({
      ...VALID,
      mcp: { transport: "http", url: "http://localhost:3001" },
    });
    expect(result.success).toBe(true);
  });
});
