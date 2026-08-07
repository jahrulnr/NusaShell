import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const controllerSrc = readFileSync(new URL("../src/renderer/agent-conversation-controller.js", import.meta.url), "utf8");
const canvasSrc = readFileSync(new URL("../src/renderer/agent-canvas-render.js", import.meta.url), "utf8");

describe("agent canvas & streaming timer cleanup (ticket #34)", () => {
  it("clears the streaming canvas timer so a stale 350ms render never scans a detached message", () => {
    expect(controllerSrc).toMatch(/clearTimeout\(streamState\.canvasRenderTimer\)/);
  });

  it("clears the canvas zoom hide timer so it cannot fire against a detached host", () => {
    // The hideTimer is scheduled inside applyScale (900ms). Any detach path
    // must cancel it; keep the source-invariant that the timer is cleared.
    expect(canvasSrc).toMatch(/hidden\s*=\s*true/);
    expect(canvasSrc).toMatch(/window\.clearTimeout\(hideTimer\)/);
    expect(canvasSrc).toMatch(/hideTimer\s*=\s*0/);
  });
});
