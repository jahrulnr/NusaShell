# NusaShell Automation Capabilities — Built-in Tools and MCP Providers

Status: Implemented with the Automation Engine

## 1. Model

A workflow names logical capabilities (`email.read`, `filesystem.read`, `email.received`). It does not name MCP servers. The capability registry binds those names to:

- built-in providers (`filesystem.*`, `workflow.wait_until`, `http.request` stub)
- MCP plugin tools (`plugin:<id>`), using `tool_name` with `_` mapped to `.`

```text
Automation Engine → Capability Registry → builtin | MCP
```

## 2. Availability vs validity

| Verdict | Meaning |
| --- | --- |
| INVALID | Syntax error or unknown capability name |
| BLOCKED | Known provider is disabled, not running, or auto-start refused |
| OK | Runnable |

A disabled MCP provider blocks dependent automations. It does not fail in-flight jobs as a generic error and does not rewrite the workflow. Re-enabling the provider restores them.

Event types on `when:` triggers may be ingested via `automation.ingest` even when no MCP source is installed. Action `uses:` names must resolve or validation is INVALID.

## 3. Auto-start

MCP providers follow plugin auto-start plus per-trigger `auto_start` (`inherit`, `allow_auto_start`, `always_require_active`). Default is allow auto-start for automations.

## 4. Uninstall

`automation.dependents` lists workflows that bind a provider. Deleting a plugin disables that provider id so remaining automations surface as blocked/missing rather than silently succeeding.
