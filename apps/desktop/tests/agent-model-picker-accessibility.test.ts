import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const launcher = readFileSync(new URL("../src/renderer/launcher.js", import.meta.url), "utf8");

describe("agent model picker accessibility", () => {
  it("exposes selected options and keyboard navigation", () => {
    expect(launcher).toMatch(/setAttribute\("aria-selected", String\(/);
    expect(launcher).toMatch(/event\.key === "ArrowDown"/);
    expect(launcher).toMatch(/event\.key === "ArrowUp"/);
    expect(launcher).toMatch(/event\.key === "Home"/);
    expect(launcher).toMatch(/event\.key === "End"/);
  });

  it("returns focus to the trigger when Escape closes the picker", () => {
    expect(launcher).toMatch(/agent-model-trigger.*focus\(\{ preventScroll: true \}\)/);
  });

  it("repositions the popover and re-renders into a fresh list on open", () => {
    // Open handler must wire the trigger <-> menu aria relation, keep the menu
    // visible in viewport at open (sets aria-expanded), re-render contents, and
    // position it. Closing path restores focus and disposes rolling listeners.
    expect(launcher).toMatch(/setAttribute\("aria-expanded", "true"\)/);
    expect(launcher).toMatch(/renderAgentModelPicker\(\);/);
    expect(launcher).toMatch(/positionAgentModelMenu\(\)/);
    expect(launcher).toMatch(/menu\.hidden = false;/);
    expect(launcher).toMatch(/closeAgentModelMenu/);
    expect(launcher).toMatch(/disposeModelMenuPositioning\(\)/);
  });

  it("keeps lazy viewport repositioning and dispose-on-close hooks", () => {
    // Rolling listeners must be cleaned up when the picker closes, so a
    // long-lived view does not accumulate resize/scroll observers.
    expect(launcher).toMatch(/disposeModelMenuPositioning/);
    expect(launcher).toMatch(/requestAnimationFrame/);
  });

  it("rerenders the room-bound selection after changing it", () => {
    const bind = launcher.indexOf("window.shell.agentConversations.setModel");
    const rerender = launcher.indexOf("renderAgentModelPicker();", bind);
    expect(bind).toBeGreaterThanOrEqual(0);
    expect(rerender).toBeGreaterThan(bind);
  });
});
