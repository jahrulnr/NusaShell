import { describe, expect, it } from "vitest";
import { hasPluginUi } from "../src/plugin/services/has-plugin-ui.js";

describe("hasPluginUi", () => {
  it("returns true when ui.entry is a non-empty string", () => {
    expect(hasPluginUi({ ui: { entry: "ui/index.html" } })).toBe(true);
  });

  it("returns false when ui is undefined (headless)", () => {
    expect(hasPluginUi({})).toBe(false);
    expect(hasPluginUi({ ui: undefined })).toBe(false);
  });

  it("returns false when ui.entry is empty or whitespace", () => {
    expect(hasPluginUi({ ui: { entry: "" } })).toBe(false);
    expect(hasPluginUi({ ui: { entry: "   " } })).toBe(false);
  });

  it("returns false when ui.entry is missing", () => {
    expect(hasPluginUi({ ui: {} })).toBe(false);
  });
});
