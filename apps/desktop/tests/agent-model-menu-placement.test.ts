import { describe, expect, it } from "vitest";
import {
  MODEL_MENU_EDGE_PADDING,
  computeAgentModelMenuPlacement,
} from "../src/renderer/agent-model-menu-placement.js";

// Trigger sits in the composer at the bottom of the workspace, ~24px from the
// bottom edge. The picker should open upward when there is room above.
const triggerNearBottom = { width: 200, height: 34, top: 600, left: 80 };
const bigMenu = { width: 560, height: 430 };
const tallViewport = { width: 1280, height: 900 };

describe("computeAgentModelMenuPlacement", () => {
  it("opens upward and left-aligns with the trigger when there is room above", () => {
    const p = computeAgentModelMenuPlacement({ trigger: triggerNearBottom, menu: bigMenu, viewport: tallViewport });
    expect(p.orientation).toBe("above");
    expect(p.left).toBe(triggerNearBottom.left);
    // 600 - 8 - 430 = 162
    expect(p.top).toBe(162);
  });

  it("flips below when the menu does not fit above the trigger", () => {
    // Menu (430) + gap (8) does not fit in the 118px above a trigger at top 118.
    const tiny = { trigger: { width: 200, height: 34, top: 118, left: 80 }, menu: bigMenu, viewport: tallViewport };
    const p = computeAgentModelMenuPlacement(tiny);
    expect(p.orientation).toBe("below");
    expect(p.top).toBe(118 + 34 + 8);
  });

  it("clamps horizontally so the popover never overflows the right viewport edge", () => {
    const triggerAtRight = { width: 200, height: 34, top: 600, left: tallViewport.width - 80 };
    const p = computeAgentModelMenuPlacement({ trigger: triggerAtRight, menu: bigMenu, viewport: tallViewport });
    expect(p.orientation).toBe("above");
    expect(p.left).toBe(tallViewport.width - MODEL_MENU_EDGE_PADDING - bigMenu.width);
    // 1280 - 16 - 560 = 704
    expect(p.left).toBe(704);
  });

  it("clamps horizontally so the popover never overflows the left viewport edge", () => {
    // Wide menu on a narrow viewport, trigger near the left edge.
    const narrow = { width: 640, height: 900 };
    const triggerAtLeft = { width: 120, height: 34, top: 200, left: 4 };
    const p = computeAgentModelMenuPlacement({ trigger: triggerAtLeft, menu: bigMenu, viewport: narrow });
    expect(p.left).toBe(MODEL_MENU_EDGE_PADDING);
  });

  it("keeps the menu inside the viewport even when the viewport is smaller than the menu", () => {
    // Menu 560 wide in a 400px viewport → left clamps to the edge padding
    // because there is not enough room for the full menu; it must still stay
    // within the viewport horizontally (left >= 0 and left + width <= 400).
    const p = computeAgentModelMenuPlacement({ trigger: triggerNearBottom, menu: bigMenu, viewport: { width: 400, height: 900 } });
    expect(p.left).toBeGreaterThanOrEqual(0);
    expect(p.left).toBeLessThanOrEqual(MODEL_MENU_EDGE_PADDING);
    expect(p.left + p.width).toBeLessThanOrEqual(400);
    // Width is clamped to the available viewport minus padding.
    expect(p.width).toBe(400 - MODEL_MENU_EDGE_PADDING * 2);
  });
});
