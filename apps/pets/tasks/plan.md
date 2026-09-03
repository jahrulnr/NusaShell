# NusaShell Pet Runtime Plan

## Scope

Promote the SDL2 spike into `apps/pets` as a reliable Linux X11 desktop pet
runtime connected to NusaShell's existing `/ws`. The shipped artwork is a
hatch-pet v2 WebP atlas: nine standard animation rows plus sixteen clockwise
look-direction cells.

## Architecture decisions

- Use an SDL2 borderless, always-on-top window with the X11 Shape extension for
  real per-pixel transparency. The renderer draws RGBA textures while native
  bounding/input shapes are derived from the active cell alpha.
- Decode the fixed 1536×2288 WebP atlas in a pure-Go `char` package. Keep atlas
  geometry, logical state-row mapping, timing, look-cell selection, and
  frame-shape output explicit and testable.
- Keep click-vs-drag policy and pointer-to-direction math in pure packages so
  desktop behavior can be tested without SDL.
- Connect to NusaShell's existing `/ws` event envelope through a small adapter;
  the pet maps lifecycle events to its state vocabulary instead of inventing a
  second backend endpoint.

## State and interaction contract

- `idle` uses the idle animation and switches to one of 16 look cells when the
  pointer leaves the center deadzone.
- `thinking`, `reasoning`, and `waiting` loop their mapped atlas rows.
- Atlas frame timing is rendered at 0.5× speed (runtime delay ×2) so the pet's
  small poses remain legible without changing the packaged artwork.
- `done` and `error` play their mapped one-shot row, hold the last frame for one
  tick, then return to `idle`.
- Left click launches the optional Electron path; only a left-button hold can
  start a drag, and hover motion never moves the window. X11 root-pointer
  polling keeps drag position and release detection active after the cursor
  leaves the shaped window; its cadence follows the active display refresh
  rate, falling back to 60 Hz when SDL leaves it unspecified.
  Right click and `SIGUSR1` toggle whole-window click-through.
- Relevant NusaShell `/ws` lifecycle events drive state transitions; unrelated
  stream events are ignored.

## Slices

1. Static image loading, alpha-derived X11 bounding shape, and input behavior.
2. Drag, click-to-open, click-through toggle, and graceful shutdown.
3. WebSocket envelope/state mapping, reconnect/cancellation, and diagnostics.
4. Hatch-pet v2 atlas decoding, state playback, dynamic frame shape, and
   pointer-driven look direction.
5. Generated WebP asset packaging, v2 QA artifacts, and local X11 smoke
   verification.

## Acceptance criteria

- The configured `spritesheet.webp` is RGBA, 1536×2288, and validates as
  `spriteVersionNumber: 2` with all 11 rows present.
- No opaque rectangle is visible around a transparent pet image on X11.
- Visible pixels are interactive by default; transparent pixels pass through.
- Pointer movement selects the expected clockwise look cell and leaving the
  window restores neutral idle.
- A left-button hold moves the pet without launching NusaShell; a simple click
  launches the configured Electron path, while hover motion is inert.
- Relevant backend events select the expected state; one-shot completion/error
  animations return to idle.
- SIGINT/SIGTERM closes the SDL window and WebSocket promptly.
- Default Go tests, race tests, vet, and SDL2-tagged tests/build remain green.

## Deferred

- Native Wayland shaped input/always-on-top support.
- More backend event types or a notification panel beyond the head bubble.
- Alternate mascot skins and richer motion effects beyond the atlas rows.
