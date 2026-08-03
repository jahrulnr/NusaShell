# Security Policy

NusaShell is a broker and host for AI tools — **not** a security product that
certifies MCP servers or AI models. Before reporting, please read the
responsibility split below so your report lands in the right place.

## Reporting a vulnerability

Report vulnerabilities in NusaShell itself through **GitHub private
vulnerability reporting**:
[Security tab → Report a vulnerability](https://github.com/jahrulnr/NusaShell/security/advisories/new).

Please include:

- The affected version (`VERSION` file) and platform (Linux / macOS / Windows).
- Steps to reproduce, including whether it needs an installed plugin, a
  specific AI provider, or only the shell itself.
- The impact — what an attacker gains (e.g. host process compromise, cross-
  plugin data access, escape from a structural guard listed below).

Do not open public issues for unpatched vulnerabilities. If private reporting
is unavailable to you, open an issue with only a minimal description and ask
for a private channel — leave exploit details out.

We aim to acknowledge reports within a few days and will coordinate a fix and
disclosure through the security advisory.

## Supported versions

NusaShell is pre-1.0 and ships rolling releases from `master`. Only the
**latest release** (tagged `v$(cat VERSION)`) receives security fixes; older
tags are not patched.

## What is in scope

NusaShell owns its **structural platform guarantees**. Vulnerabilities here
are treated as security bugs in NusaShell:

- **Broker isolation** — plugin UI and MCP peer-connecting or bypassing the
  shell broker.
- **Process lifecycle correctness** — `PluginRuntimeManager` losing track of
  plugin processes in a way that is exploitable.
- **Files / Terminal path containment** — relative-path escapes (`../`
  traversal) past the declared root in the bundled Files/Terminal plugins.
- **Install-time path checks** — manifest `ui.entry` or file icons resolving
  outside the plugin folder.
- **Credential handling by the shell itself** — e.g. provider API keys or
  plugin credentials leaking through NusaShell-owned logs, IPC, or storage.

## What is out of scope

Per [`docs/architecture/security-boundary.md`](./docs/architecture/security-boundary.md),
the following belong to users, plugin authors, or AI providers — reports here
will be redirected, not patched in NusaShell:

- Behavior of an installed MCP server the user chose to enable (what its tools
  do on the host).
- AI model behavior, tool-call decisions, or prompt-injection resistance in
  content the model receives (beyond the existing `data_is_untrusted` label).
- Destructive or unexpected actions taken by an enabled plugin or model.
- Missing tool-call approval gates, injection filters, or plugin allowlists —
  deliberately not product work.

Residual risks that exist *because* of this boundary are tracked openly in
[`docs/RISK.md`](./docs/RISK.md).
