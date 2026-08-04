import { describe, expect, it } from "vitest";
import { clampCanvasDrawerWidth } from "../src/renderer/agent-canvas-layout.js";

describe("canvas drawer layout", () => {
  it("keeps the drawer inside usable viewport bounds", () => {
    expect(clampCanvasDrawerWidth(200, 1200)).toBe(360);
    expect(clampCanvasDrawerWidth(1400, 1200)).toBe(960);
    expect(clampCanvasDrawerWidth(700, 1200)).toBe(700);
  });

  it("does not leave an unusably wide drawer on a narrow viewport", () => {
    expect(clampCanvasDrawerWidth(700, 500)).toBe(476);
  });
});
