// Smart placement for the agent model picker (#agent-model-menu).
//
// The picker is an anchored popover (relative to the model trigger in the
// composer), not a centered modal. Instead of the fragile CSS-only
// `position:absolute; right:0; bottom:100%` anchor trick, we measure the
// trigger in viewport coordinates (getBoundingClientRect) and compute a
// fixed-position rect that always stays fully inside the viewport:
//
//   - vertical flip: opens upward when there is room above, otherwise below
//     (the trigger sits in the composer at the bottom of the workspace, so it
//     usually opens upward);
//   - horizontal clamp: left edges line up with the trigger, then the menu is
//     shifted so it never overflows the right/left viewport edges.
//
// Pure function — no DOM access — so the geometry is unit-testable.

export const MODEL_MENU_GAP = 8;
export const MODEL_MENU_MAX_HEIGHT = 430;
export const MODEL_MENU_EDGE_PADDING = 16;

/**
 * Compute the fixed-position placement for the model picker popover.
 *
 * @param {object} sizes
 * @param {{ width: number, height: number }} trigger - the trigger button rect
 * @param {{ width: number, height: number }} menu - measured menu size
 * @param {{ width: number, height: number }} viewport - window inner size
 * @returns {{ left: number, top: number, orientation: "above" | "below" }}
 *   A rect whose top-left is relative to the viewport (for position:fixed).
 */
export function computeAgentModelMenuPlacement(sizes) {
  const { trigger, menu, viewport } = sizes;
  const gap = MODEL_MENU_GAP;

  const width = Math.min(menu.width, viewport.width - MODEL_MENU_EDGE_PADDING * 2);

  // Horizontal clamp: align the left edge with the trigger, then keep the
  // whole popover inside the viewport on both sides.
  const preferredLeft = trigger.left;
  let left = clamp(preferredLeft, MODEL_MENU_EDGE_PADDING, viewport.width - MODEL_MENU_EDGE_PADDING - width);

  // Preferred vertical orientation: upward, exactly like the previous
  // bottom:calc(100% + 8px) anchor. Flip to below only when there is not
  // enough room above the trigger.
  const aboveTop = trigger.top - gap - menu.height;
  const belowTop = trigger.top + trigger.height + gap;
  const opensAbove = aboveTop >= MODEL_MENU_EDGE_PADDING;
  const top = opensAbove ? aboveTop : belowTop;

  return { left: Math.round(left), top: Math.round(top), orientation: opensAbove ? "above" : "below", width: Math.round(width) };
}

function clamp(value, min, max) {
  if (max < min) return min;
  return Math.min(Math.max(value, min), max);
}
