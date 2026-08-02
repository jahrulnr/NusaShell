# Risk register

Known residual risks and the mitigations that bound them. This is the
authoritative risk file (named `RISK.md`, not `SECURITY.md`).

## Scope of responsibility

NusaShell is a broker/platform for AI tools, **not** a security layer that
vets MCP server behavior, AI model decisions, or prompt injection. Residual
risks below remain accepted by design. The product stance and responsibility
split (user / plugin author / AI provider / NusaShell) are defined in
[`docs/architecture/security-boundary.md`](./architecture/security-boundary.md).

Host-isolation work (iframe sandbox attributes, install-time permission
prompts, process isolation) may still arrive later as a separate phase. That
phase protects the **host** from a plugin process; it does not police what an
enabled MCP tool or model chooses to do.

## Agent MCP launch overrides

`mcp_enable` lets the agent patch a plugin's launch `args` and `env` while
`command` stays immutable. This enables workspace rebind for static servers
and explicit agent-driven relaunch, but carries residual risk:

- **`npx` argument swap.** A manifest that runs `npx -y @someplugin` can have
  its `args` rewritten to `@devilplugin`, executing a different package. The
  shell does **not** maintain a hard allowlist of args/env. Mitigations today:
  - `command` is frozen; the agent cannot swap the binary itself.
  - Env **values** are redacted in `mcp_list` so secrets are not echoed back,
    but the agent can still set arbitrary env values on respawn.
  - Trusted models are **not** a security boundary (prompt/tool injection can
    drive the agent). Treat launch overrides as a privileged, best-effort path.
  - **Declined (not roadmap):** allowlists of permitted arg/env mutations,
    signed-manifest verification, and user-approval UX before applying
    agent-supplied overrides. Those would be MCP/AI behavioral security
    controls; they are permanently out of scope per
    [`security-boundary.md`](./architecture/security-boundary.md).

- **In-memory state loss on respawn.** A different launchSpec while running
  triggers stop+start. Plugin session state (e.g. open terminal sessions,
  in-memory caches) is lost. This is accepted for MVP; the keyed
  `(pluginId, workspaceId)` pool (Phase 4) is deferred until this hurts UX.

- **No per-call env mutation on a live stdio child.** The shell never mutates
  a running child's env per call; it respawns instead. Per-call env mutation is
  explicitly rejected.

## Roots are advisory, not enforced

MCP Roots are an **interoperability** mechanism, not a security sandbox:

- The spec says servers **SHOULD** respect root boundaries, not **MUST**
  enforce. Roots are advisory.
- Many community servers ignore `roots/list_changed` and read env/args only
  once at startup. For those, workspace changes require a respawn (Phase 3).
- The bundled Files plugin enforces its own root containment
  (`resolvePath` rejects `../` and absolute escapes) regardless of roots, so
  roots update the *scope* but containment remains a hard guard in the plugin.
- Do **not** treat roots as enforced sandbox or trusted-model-as-security.

Draft MCP 2026-07-28 deprecates roots toward tool params + server config. We
still ship Roots for today's servers; the Phase 1 host wrap is future-proof
against that migration.

## Workspace binding scope

- The wrap only rewrites **relative** path/cwd values for the bundled Terminal
  and Files plugins. Absolute paths are preserved, so the model can still
  target OS-absolute locations. Containment is still enforced by each plugin
  server.
- Third-party MCP plugins are passed through unchanged; the shell does not
  mutate their arguments. The model must pass absolute paths for those.
- `NUSASHELL_WORKSPACE` is set at spawn when a workspace is bound. Plugins opt
  in by reading it (Files does, as a fallback after `NUSASHELL_FILES_ROOT`).
