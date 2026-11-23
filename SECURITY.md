# Security Policy

## Reporting a vulnerability

Please don't open a public issue for a security problem. Report it privately through
GitHub's private vulnerability reporting: open the **Security** tab of this repository
and choose **Report a vulnerability**.

## Threat model

NusaShell is a personal, single-user tool. It runs with your user's permissions, is built
for one person on one machine, and makes no security guarantees for shared or hosted use.

- **Provider-model tool calls run without approval.** In a room on an API provider,
  tool calls execute automatically during a turn you started. There is no confirmation step
  per call.
- **The agent acts only during a turn.** It never starts work on its own. The only
  background activity is a WebSocket keepalive and a model-list refresh per provider. While
  `nusashell serve` runs, the refresh happens at startup and then hourly. It calls each
  provider's model-list endpoint with that provider's key.
- **`exec` is not sandboxed.** It runs shell commands as your user, with your filesystem
  access and environment, and a default timeout of 120 seconds. The environment includes
  any keys exported to it, including keys referenced with `env:`.
- **Files outside the workspace.** `read_file`, `write_file`, and `apply_patch` are
  confined to the workspace. `read_media` accepts absolute host paths, and `exec` can reach
  anything your user can reach.
- **Prompt injection is best-effort.** Instructions can arrive through user messages, tool
  results, the system prompt, or the provider in use. Tool results are
  wrapped in an `<untrusted_tool>` envelope before the model reads them. That reduces the
  risk but does not remove it. The consequences of injected instructions are outside this
  project's scope.
- **Providers and their upstreams are not trusted.** Requests go to the endpoint you configure,
  which may be a router or proxy. How it handles your data is outside this project's control.
- **Login only when the server is reachable.** By default (`server.auth: auto`) the WebUI
  and API listen on `127.0.0.1` without a login: Host and Origin checks block cross-site
  browser requests, but any process on your machine that can reach the port can call the
  API. When `server.bind` is not a loopback address, or `server.hosts` names a public host
  (a reverse proxy), the owner password is required: bcrypt-hashed, HttpOnly
  `SameSite=Strict` session cookies (only their SHA-256 is stored), a CSRF token on every
  state-changing request, and five failed logins per client per 15 minutes. `server.auth:
  on` always asks; `off` never asks and refuses to start on a reachable server. The server
  never terminates TLS itself — put a TLS reverse proxy in front of it before exposing it.
- **External agents run their own tools.** An ACP agent is a process you configured; it
  executes its own file and shell tools as your user. Its permission requests go through
  the agent's approval policy (`ask`, `auto_safe`, `auto_all`), but an agent that does not
  ask before acting is not stopped by NusaShell.
- **Local data is not encrypted.** Keys you enter directly are stored as plaintext in a
  local SQLite file (mode 0600), and provider records only reference them. Conversation
  records and the diagnostic log (`data/logs`) contain message content, including tool
  results.
