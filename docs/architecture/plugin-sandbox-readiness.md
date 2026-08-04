# Plugin Sandbox Readiness

Mitigations for three real plugin-broker risks identified in the sandbox
readiness review. Each is a focused fix; none expand the MVP security surface
(see `AGENTS.md` → "Security is deferred").

## Finding 1 — Files root containment (P0, critical)

**Risk:** the Files MCP server's source `mcp/config.js` rejected paths that
escape the root, but the **shipped esbuild bundle** `mcp/server.cjs` was stale
and returned the resolved path with **no check**. The manifest runs the
bundle, so production was vulnerable to `../../` and absolute-path escape.

**Fix:**

- Rebuilt `plugins/files/mcp/server.cjs` so the guarded `resolvePath` is in
  the shipped artifact.
- `plugins/files/tests/bundle-containment.test.js` asserts the bundle
  contains the containment guard (stale-bundle regression guard).
- `packages/infrastructure/tests/files-bundle-sandbox.test.ts` spawns the
  bundle over stdio MCP and verifies `../../` and absolute paths are rejected
  at runtime, plus a positive read inside the root.

Containment is solely `resolvePath` under `NUSASHELL_FILES_ROOT` / home. No
`System32` denylist. See `plugins/files/README.md` for the rebuild contract.

> **Reversed on 2026-08-04:** the Files plugin's `../` traversal containment
> was removed because it was both confusing (`/` resolved to the home root
> instead of the OS root) and a false sense of security (the agent is a
> trusted actor; real security is the user/AI provider's responsibility per
> `docs/architecture/security-boundary.md`). `resolvePath` now does a plain
> `path.resolve(root, input)` — `/` and absolute paths resolve to OS-absolute
> paths, `../` traversal is allowed, and the root is purely a convenience for
> relative path resolution. The Terminal plugin's containment (if any) is
> unaffected. The `bundle-containment.test.js` and
> `files-bundle-sandbox.test.ts` regression guards were removed as obsolete.
> See plan: `enrich_files_plugin_for_agent_reliability`.

## Finding 2 — Process death ↔ Status SoT (P1)

**Risk:** killing the MCP process from outside NusaShell left the runtime
state-of-truth stuck on `running` because:

1. The `mcpClient.onClose` watcher was registered **after** the transition to
   `running` and the `plugin.started` event — a race window where an external
   kill was missed.
2. For stdio, `entry.process` was never set, so the `process.exited` path was
   dead code; only the SDK `onclose` could observe death, and it was late.
3. `plugin.started` hardcoded `pid: 0` in the WS event.

**Fix** (in `packages/application/src/plugin/services/plugin-runtime-manager.ts`):

- Extracted `registerExitWatcher(entry)` and call it **before** the
  `running` transition and the `plugin.started` event, closing the race window.
- The `onClose` callback no longer guards only on `state === "running"`; it
  lets `handleProcessExit` decide (it skips `stopping`/`idle`), so a death
  during `starting` is also caught (`starting → crashed` is an allowed
  transition).
- `PluginStartedEvent` now carries an optional `pid` (from `mcpClient.pid` or
  `entry.process.pid`); `client-event.mapper.ts` uses `e.pid ?? 0` instead of
  a hardcoded `0`.

**Out of scope:** the missing plugin-recovery/auto-restart service. This fix
only corrects the SoT and events; it does not auto-heal.

Tests: `packages/application/tests/plugin-runtime-manager.process-death.test.ts`
(close → crashed + event; close watcher registered before `plugin.started`;
stop path does not flap; started event carries pid).

## Finding 3a — Tools=0 honesty (P2)

**Risk:** the launcher's `listTools` swallowed `tool.list` errors as
`{ tools: [] }`, so the plugin drawer showed "No tools available" even when
the plugin was running but the listing failed (transport error, crash). A
silent `Tools=0` masked failures as success.

**Fix:**

- `apps/desktop/src/renderer/launcher.js` `listTools` now returns
  `{ tools, error }` on failure instead of an empty tool list.
- `describeToolsPanel(result, plugin)` in `launcher-ui.js` maps the outcome
  to `{ status: "ready" | "empty" | "unavailable", count, tools, message }`,
  distinguishing a genuine empty toolset from a failed listing.
- The drawer renders `tools-unavailable` (with the error reason) vs
  `tools-empty` (idle / running-but-empty) with distinct styling.
- The WS `tool.list` handler already propagates `PLUGIN_NOT_RUNNING` rather
  than an empty array (verified — no change needed in the application layer).

Tests: `apps/desktop/tests/launcher-ui.test.ts` (`describeToolsPanel` covers
ready, unavailable with reason, unavailable without message, running-empty
vs idle-empty, and null result).

## Finding 3b — Deferred (not in this repo)

The reporter mentioned `ui.capture`, `panelIndex`, and `FileSystem not
renderable`. These strings have **zero matches** in NusaShell. The closest
existing concepts are unrelated (skills "not rendered or editable"; Files
uses `tabIndex` for a11y only; window `mode: panel|fullscreen`). Treat as
out-of-scope pending reporter clarification (plugin id / screenshot / tool
name). No new capture APIs were invented for this mitigation.

## Verification

1. **Escape:** with the rebuilt `server.cjs`, `files_read` / `files_list`
   with `../../…` or an absolute path outside root → reject
   `Path escapes files root`; a read inside root still works.
2. **Kill:** start Files → kill the node MCP PID from a shell → status
   becomes `crashed` within event latency; the drawer does not claim
   `running` forever.
3. **Tools:** start Files → the drawer shows tools or an explicit error, not
   a silent `0` on transport failure; after a healthy start, tool count > 0.
4. Unit/integration tests green for containment + close-ordering +
   listTools error surfacing.
