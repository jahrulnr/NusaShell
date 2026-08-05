import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const launcher = readFileSync(new URL("../src/renderer/launcher.js", import.meta.url), "utf8");

describe("agent model picker accessibility", () => {
  it("exposes selected options and keyboard navigation", () => {
    expect(launcher).toMatch(/setAttribute\("aria-selected", String\(.*\)\)/);
    expect(launcher).toMatch(/event\.key === "ArrowDown"/);
    expect(launcher).toMatch(/event\.key === "ArrowUp"/);
    expect(launcher).toMatch(/event\.key === "Home"/);
    expect(launcher).toMatch(/event\.key === "End"/);
  });

  it("returns focus to the trigger when Escape closes the picker", () => {
    expect(launcher).toMatch(/agent-model-trigger.*focus\(\{ preventScroll: true \}\)/);
  });
});
