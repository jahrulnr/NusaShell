export const CANVAS_DRAWER_MIN_WIDTH = 360;
export const CANVAS_DRAWER_MAX_WIDTH = 960;
export const CANVAS_DRAWER_VIEWPORT_GUTTER = 24;

export function clampCanvasDrawerWidth(width, viewportWidth = window.innerWidth) {
  const usableMax = Math.max(
    CANVAS_DRAWER_MIN_WIDTH,
    Math.min(CANVAS_DRAWER_MAX_WIDTH, Number(viewportWidth) - CANVAS_DRAWER_VIEWPORT_GUTTER),
  );
  const numericWidth = Number(width);
  if (!Number.isFinite(numericWidth)) return Math.min(560, usableMax);
  return Math.round(Math.min(usableMax, Math.max(CANVAS_DRAWER_MIN_WIDTH, numericWidth)));
}
