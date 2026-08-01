---
name: build-nusashell-plugin
description: "Build, extend, review, convert, or debug NusaShell plugins in either shape: headless MCP-only plugins with no window, or headed/windowed plugins that bundle UI plus MCP. Use for manifest.json, plugin mcp/ or ui/ work, MCP tool/prompt/resource contracts, window.shell bridge integration, transport and lifecycle choices, packaging, safety, and plugin tests. Route to headless when capability is agent/automation-only and to headed when users need a Home tile and visual workspace."
---

# Build a NusaShell Plugin

Build one coherent plugin bundle around a public MCP capability contract, then add a UI only when the user needs a visual surface. Keep the shell as the sole broker and make the chosen shape explicit before editing.

## Load project context

1. Read the repository `AGENTS.md` and the plugin-authoring section of `README.md`.
2. Read [references/repository-contract.md](references/repository-contract.md) for the shared manifest, MCP, packaging, source-map, and verification rules.
3. Inspect the current manifest schema and the closest built-in plugin. Prefer implemented contracts over stale examples.
4. If the task changes the manifest or bridge shape, also read `docs/blueprint.md` and the touch points named by `AGENTS.md`.

## Choose the shape

Use **headless** when tools, prompts, or resources are consumed by agents, scheduled jobs, or the Plugins view and no continuous visual interaction is needed. Read [guides/headless.md](guides/headless.md).

Use **headed** when users must browse, edit, monitor, or interact through a dedicated window and Home launcher tile. Read [guides/headed.md](guides/headed.md), then read `.agents/skills/frontend-design/SKILL.md` before designing or reshaping the UI.

Preserve an existing plugin's shape unless requirements justify changing it. Do not add a UI merely for settings, status, or discoverability that the Plugins view, logs, tools, prompts, or resources already provide.

## Design the shared capability

- Define user-centered, namespaced tool names and bounded schemas before wiring adapters or UI.
- Make UI callers and agent callers share the same MCP names, validation, errors, and result semantics.
- Separate service/domain behavior from `server.*`, tool descriptors/dispatch, persistence, and UI state.
- Decide transport, lifecycle, persistence, workspace/root behavior, credentials, cancellation, concurrency, and output limits explicitly.
- Treat every argument, path, URL, command, remote response, stored value, and UI field as untrusted input.
- Keep credentials host-owned when shell settings exist; inject them only at runtime and never expose them in schemas, results, or logs.

## Implement the MCP boundary

- Use the official MCP SDK and advertise only implemented capabilities.
- Keep stdout exclusively for stdio MCP protocol traffic; write bounded, redacted diagnostics to stderr.
- Validate runtime inputs strictly even when JSON Schema already describes them.
- Keep a canonical catalog synchronized with descriptors and dispatch handlers.
- Return useful `structuredContent` with a compact text fallback. Return safe `isError: true` results for expected failures.
- Use truthful `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint` annotations.
- Bound work and output; support effective cleanup/cancellation for long-running operations.
- Handle SIGINT/SIGTERM and fatal startup failures predictably.

## Verify in layers

1. Unit-test services with temporary data or fakes rather than developer-owned state.
2. Test each MCP operation's valid path, missing/extra fields, bounds, hostile input, unknown name, safe errors, annotations, and catalog synchronization.
3. Build the exact runtime artifact declared by the manifest, then type-check and run plugin-local tests.
4. Validate package paths through the installer/sync path and exercise lifecycle plus tool discovery/calls in the shell.
5. Run narrow repository tests first; run `pnpm test:backend` for shared contract/runtime changes and `pnpm test:frontend` for desktop integration changes.
6. If mapped controls under `apps/desktop/src/renderer/` change, update the UI map and run `pnpm scan:ui-docs`; never edit generated UI markdown manually.

## Preserve architecture locks

- Never connect plugin UI and MCP directly; route UI calls through `window.shell` and the host/backend broker.
- Never create a plugin-owned direct WebSocket path.
- Keep live runtime truth in `PluginRuntimeManager`, not in a renderer, gateway, or database flag.
- Keep plugin-local changes local. If a public manifest or bridge contract changes, update blueprint, PoC, contracts, SDK, layers, desktop projection, and agent docs together.
- Do not mix deferred signing, sandboxing, permissions, or process-isolation work into ordinary plugin plumbing unless explicitly requested.

## Completion gate

Finish only when the chosen shape, manifest, package files, MCP catalog, lifecycle, consumers, and tests agree. Report whether the plugin is headless or headed, why, the selected transport/lifecycle, exposed capabilities, verification commands, and deferred risks.
