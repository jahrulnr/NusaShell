# Frontend layer instructions

Applies to `frontend/` in addition to the repository root `AGENTS.md`.

## Boundary

The production frontend is native HTML, CSS, and browser ES modules embedded
by Go. Do not add a required bundler, framework, transpiler, component runtime,
or production Node step. Keep data and behavior contracts aligned with
`contracts/` and `application/`; the browser does not invent backend policy.

## Reuse before creation

Search existing markup, IDs, CSS classes/tokens, view modules, and tests before
adding a component or state container. Start with:

- `js/rpc.js` for HTTP RPC, shared backend event listeners, and WebSocket
  connection state.
- `js/ui.js` for element creation, toast, styled selects, dialogs, focus
  handling, icons, and overlay cleanup.
- `js/app.js` for routing, view initialization/refresh, global connection
  state, and cross-view lifecycle.
- `js/views/` for the owning feature view and `js/views/agent/` for extracted
  agent-view components.
- `styles/global.css` and existing feature styles for design tokens and
  responsive patterns.
- `frontend/tests/` for jsdom fixtures, DOM helpers, regression patterns, and
  accessibility assertions.

Extend an existing view/component when ownership matches. Extract a shared
module only after current callers demonstrate one stable responsibility. Do
not create a generic widget, global store, or wrapper for one use site.
Derived state stays derived; browser-only preferences may use the established
local-storage pattern, while server state must be refreshed through RPC.

## UI and protocol requirements

- Use semantic HTML, labels, keyboard operation, visible focus, appropriate
  ARIA, and safe focus return for overlays.
- Reuse styled selects and dialogs. Do not expose native `alert`, `confirm`,
  `prompt`, or visible native option menus.
- Handle loading, empty, error, disabled, reconnect, and narrow-screen states.
- Keep HTTP for browser commands/queries, WebSocket for lifecycle signals,
  and per-round SSE for live agent deltas. Do not duplicate transport state.
- If an RPC or event changes, update the canonical Go contract and backend
  tests, not only a JavaScript string.
- Keep third-party code in `vendor/`; prefer existing browser APIs and vendored
  libraries before adding a dependency.

## Documentation and verification

When a view, `data-view`, control ID, modal, or interaction changes, update
`resources/agent/docs/ui-source/ui-map.json` and regenerate, never hand-edit,
the generated `ui-*.md` files.

Run the narrow test first, then:

```text
node --test frontend/tests/*.test.mjs
make scan-ui-docs-check
```

For visual changes, run the real interface at desktop and mobile widths, take
a screenshot, and inspect focus, overflow, spacing, empty/error states, and
contrast. Passing jsdom tests is not visual verification.
