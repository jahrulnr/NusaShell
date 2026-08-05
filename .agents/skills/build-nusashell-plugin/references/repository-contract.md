# Shared NusaShell Plugin Contract

Use this reference as a source map and shared checklist. Re-read repository sources when they change; this summary does not replace implemented contracts.

## Invariants

- Install one folder containing `manifest.json` and `mcp/`; add `ui/` only for headed plugins.
- Match contract ids to `publisher.name` with lowercase letters, digits, and hyphens.
- Use semantic versions. Domain validation is stricter than the shallow Zod version field.
- Keep `icon` non-empty for both shapes. Relative file icons must exist inside the plugin folder.
- Keep plugin UI and MCP from peer-connecting; the shell owns lifecycle and routing.
- Keep plugin capability knowledge with the plugin: live tool schemas plus MCP prompts for narrative howtos. Do not rely on `resources/agent/docs/` for removable plugin catalogs.
- Treat the PoC as a behavioral reference, not the target scaffold.

## Transport selection

| Transport | Manifest requirement | Runtime behavior |
| --- | --- | --- |
| `stdio` | `command`; keep parameters in `args` | Shell starts it with plugin install dir as `cwd`; stdout is MCP protocol |
| `http` | `url` | Shell connects remotely; do not assume process spawn/kill |
| `sse` | `url` | Shell connects remotely; retain only legacy-compatible usage |

In packaged Electron builds, the exact stdio command `node` resolves to the
shell's bundled Electron executable in Node mode, so JavaScript plugins do not
depend on a system-wide Node.js installation. Other executable names must still
be available through `PATH` or declared as an appropriate absolute command.

The runtime may inject `NUSASHELL_WORKSPACE` and MCP roots for workspace-aware calls. Do not assume the conversation workspace becomes process `cwd`.

## MCP server pattern

Organize non-trivial Node plugins around these responsibilities:

```text
mcp/
├── server.js          # SDK transport, handlers, shutdown boundary
├── server.cjs         # bundled artifact when manifest declares it
├── tool-catalog.js    # canonical names (create rule: no domain prefix)
├── prompts.js         # domain: strongly required; native-like: recommended
├── tools.js           # descriptors, strict validation, dispatch
├── service.js         # capability and I/O behavior
├── config.js          # bounded environment/config parsing
└── errors.js          # safe public errors/redaction
```

## Tool naming checklist

- **Create:** tool names must not start with `${domain}_` or equal `domain`.
  Use short verbs (`list`, `read`, `write`, `exec`) or multi-word verbs
  without the domain (`list_projects`, `create_ticket`). See `SKILL.md` →
  "Tool naming (create vs convert)".
- **Convert:** preserve upstream MCP tool names as-is; do not strip prefixes
  or redesign the catalog. Only package `manifest.json` + transport.
- Exemplars: `plugins/files/` (`list`, `read`, `write` — create),
  `plugins/kanban/` (`list_projects`, `create_ticket` — convert-friendly
  multi-word verbs).

Return structured and fallback representations:

```js
return {
  content: [{ type: "text", text: JSON.stringify(result) }],
  structuredContent: result,
};
```

For expected failures, return `isError: true` with a bounded, sanitized message. Never put credentials, tokens, unsafe paths, or raw remote errors in results or diagnostics.

## Choose an exemplar

- `plugins/notes/`: domain-tier compact CRUD, strict Zod input, persistence, safe errors, catalog synchronization, and a plugin-owned howto prompt.
- `plugins/files/`: native-like root containment, bounded reads/search, destructive operations, split UI state, and a constraints prompt.
- `plugins/mail/`: domain-tier host-owned credentials, runtime injection, multi-account behavior, remote errors, and a plugin-owned howto prompt.
- `plugins/terminal/`: native-like long-running sessions, process cleanup, absolute cwd rules, keep-alive behavior, and a constraints prompt.

Reuse the matching risk pattern rather than copying an entire plugin.

## Sources of truth

- `packages/contracts/src/manifest/manifest-schema.ts`: accepted JSON shape.
- `packages/domain/src/plugin/entities/plugin-manifest.ts`: cross-field validation and defaults.
- `packages/application/src/plugin/services/mcp-session-manager.ts`: transport, environment, cwd, roots.
- `packages/application/src/plugin/services/plugin-runtime-manager.ts`: live runtime source of truth.
- `packages/infrastructure/src/plugins/plugin-path-checks.ts`: declared-file containment.
- `packages/infrastructure/src/mcp/tool-result.ts`: result and error unwrapping.
- `apps/desktop/src/main/plugin-window-options.ts`: headed window normalization.
- `apps/desktop/src/main/window-manager.ts`: headed startup-before-load and plugin id injection.
- `apps/desktop/src/preload/index.ts`: current `window.shell` contract.
- `resources/agent/docs/plugins.md`: user-visible lifecycle.
- `docs/blueprint.md` sections 3–4: package, broker, launcher, and window intent.

## Shared verification matrix

| Surface | Prove |
| --- | --- |
| Manifest | Valid id/version/transport and shape-specific fields |
| Package | Declared UI, MCP artifact, and local icon exist inside the folder |
| Catalog | Names, descriptions, schemas, annotations, and dispatch agree |
| Validation | Missing, extra, over-limit, hostile, and unknown input fails safely |
| Results | Structured result useful; text fallback bounded; secrets absent |
| Lifecycle | Start, call, stop, crash, restart, shutdown behave predictably |
| Plugin knowledge | Start the plugin, then reach its howto/workflow through `mcp_context` `list_prompts` / `get_prompt`; domain tier must pass, native-like tier should pass |
| Product | Launcher/Plugins/agent discovery matches the selected shape |

Run plugin-local build, typecheck, and tests when defined. Run shared suites only for the layers changed.
