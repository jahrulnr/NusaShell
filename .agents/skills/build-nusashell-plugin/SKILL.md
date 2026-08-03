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

## Plugin knowledge and prompt tiers

Let live tool schemas (`tool_list`, `tool_search`, and `tool_schema`) describe
executable capability. Narrative usage guidance belongs in plugin-owned MCP
prompts exposed by `prompts/list` and `prompts/get`; retrieving a prompt is
context, not tool execution.

Choose the prompt tier from the capability, not only from whether the plugin has
UI:

| Plugin kind | Examples | MCP howto/workflow prompt expectation |
| --- | --- | --- |
| **Domain / multi-step** | Mail, Notes, business MCPs, ordered or non-obvious tool flows | **Strongly required.** Ship at least one `howto` or `workflow` prompt covering purpose, tool order, critical constraints, and common failure modes. Schemas alone are not enough. |
| **Native-like tools** | Files/file managers, Terminal/shell, simple self-describing CRUD | **Still recommended.** Ship a short constraints prompt covering root/cwd semantics, destructive operations, limits, and the instruction to use live schemas. |

When creating or extending a plugin, register `capabilities.prompts` and
implement `prompts/list` plus `prompts/get` for the selected tier. Prompts
should be short English prose, name the main tools and their order, explain
host-owned credentials or containment where relevant, point agents to live
schemas, and remain inside the plugin package. Agents consuming a plugin should
load its howto/workflow through shell `mcp_context` before guessing a multi-step
flow.

Keep plugin-specific catalogs and howtos out of `resources/agent/docs/` so
uninstalling a plugin removes its knowledge path. Use the shell corpus only for
NusaShell platform behavior.

## Implement the MCP boundary

- Use the official MCP SDK and advertise only implemented capabilities.
- Keep stdout exclusively for stdio MCP protocol traffic; write bounded diagnostics to stderr.
- Validate runtime inputs strictly even when JSON Schema already describes them.
- Keep a canonical catalog synchronized with descriptors and dispatch handlers.
- Return useful `structuredContent` with a compact text fallback. Return safe `isError: true` results for expected failures.
- Use truthful `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint` annotations.
- Bound work and output; support effective cleanup/cancellation for long-running operations.
- Handle SIGINT/SIGTERM and fatal startup failures predictably.

## Automation (plugin-emitted events)

Plugins can push automation events to the shell by declaring an `automation`
block in `manifest.json` and sending MCP notifications. This enables the
Watch→Agent loop: plugin observes something → emits event → shell matches
event-job → fires agent turn or tool call.

### Manifest `automation` block

```json
{
  "automation": {
    "emits": [
      {
        "type": "mail.new",
        "description": "New mail arrived",
        "payloadSchema": {
          "type": "object",
          "properties": { "messageId": { "type": "string" } },
          "required": ["messageId"]
        }
      }
    ],
    "poll": [
      { "tool": "mail_sync", "suggestEvery": "5m", "diffHint": "new message ids" }
    ]
  }
}
```

- `emits[].type` — event type string (e.g. `mail.new`). Ownership is bound to
  the declaring plugin; no other plugin can emit the same type.
- `emits[].payloadSchema` — JSON Schema fragment (v1: documentation only, not
  enforced at runtime).
- `poll[].tool` — must match a tool the plugin exposes; the shell may call it
  on a schedule as a polling fallback.
- `poll[].suggestEvery` — hint like `5m`, `30s`, `1h` (shell may ignore/clamp).

### Sending automation notifications

Two intake paths:

1. **`notifications/resources/updated`** (standard MCP) — for resource-modeled
   state changes. Params: `{ uri }`.
2. **`notifications/nusashell/automation`** (NusaShell convention) — for typed
   events. Params: `{ type, payload }`.

The shell binds `pluginId` from the connection identity (never from params),
enforces per-plugin rate limits (token bucket: 10/min default, 20 burst, 64KB
payload cap), and rejects event types not declared in the plugin's `emits`.

### Rules

- Only declare types your plugin owns. Type collisions between plugins are
  rejected at manifest load time.
- Keep payloads under 64KB; larger payloads are truncated + logged.
- Design for the rate limit: batch rapid bursts client-side when possible.
- `notifications/nusashell/automation` is a NusaShell convention, NOT an MCP
  standard — do not register it as a capability with the MCP spec.

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
