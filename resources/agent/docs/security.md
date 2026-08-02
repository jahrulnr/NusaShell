# Security & responsibility

NusaShell is an MCP-first shell for AI tools. It brokers plugin UIs, MCP
servers, and AI providers. It is **not** a security product that decides
whether a plugin or model is "safe."

## Who is responsible for what

| Party | Responsibility |
| --- | --- |
| **You (the user)** | Which plugins you install and enable; which AI provider and model you use. Enabling a tool or model means accepting that it can take destructive or unexpected actions. |
| **Plugin authors** | How their MCP server behaves on your machine (what tools do, credentials, path rules inside that server). |
| **AI providers** | How the model behaves, which tools it chooses to call, and how it handles prompt or tool injection in content it receives. |
| **NusaShell** | Routing and lifecycle: keep UI and MCP from talking peer-to-peer, start/stop/crash-detect MCP processes, apply path containment for the bundled Files and Terminal plugins, and mark external tool/docs content as untrusted data for the model. |

## What NusaShell does not do

- It does not audit or certify third-party MCP servers.
- It does not block or moderate what an AI model writes or which tools it calls.
- It does not filter prompt injection beyond labeling external content as
  untrusted (`data_is_untrusted`).
- It does not require signed manifests or a user approval dialog before every
  agent-driven MCP launch override.
- It is not liable for damage caused by a plugin or model you chose to run.

## If something destructive happens

Treat it as an outcome of the enabled plugin and/or model, not as a NusaShell
"security failure." Disable or uninstall the plugin, change providers, or
tighten your own environment. Ask NusaShell for broker/lifecycle help
(start/stop plugins, list tools, read product docs) — not for behavioral
sandboxing of MCP or AI.

## Related docs

- Architecture stance: `docs/architecture/security-boundary.md` (in the
  repository; also summarized here for the in-app docs corpus).
- Residual risks: `docs/RISK.md`.
