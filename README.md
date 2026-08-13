# NusaShell Light

NusaShell Light is a local, personal AI shell written in Go. A single
self-contained binary serves an embedded vanilla-JS frontend and brokers
multi-conversation chat with **Messages**, **Responses** and **Chat** API-format
providers, backed by skills, memory, docs and MCP servers.

UI/UX mirrors the NusaShell Electron renderer (design tokens, workspace
layouts). There is no security layer by design: no auth, no rate limiting —
run it on localhost or a trusted network only.

## Features

- **Agent** — multi-conversation chat with streaming turns, tool calls,
  stop/interrupt, compaction (auto-summarize long history) and prompt
  caching on Messages providers (`cache_control: ephemeral`).
- **Skills** — reusable markdown instruction packs the agent loads via
  `skill_run`; managed in the Skills workspace.
- **MCP** — stdio MCP servers exposed to the agent as
  `mcp__<server>__<tool>` tools; connections are lazy and cached.
- **Providers** — Messages, Responses and Chat API formats (Anthropic, OpenAI,
  DeepSeek, Ollama, LM Studio, vLLM, …); model import, connectivity test,
  API keys in a SQLite credential store.
- **Logs** — bounded activity log streamed live over SSE/WebSocket.
- **Transports** — HTTP `/rpc` (request/response), SSE `/events` (push),
  WebSocket `/ws` (bidirectional), one shared event vocabulary.

## Repository layout

| Path | Role |
| --- | --- |
| `domain/` | Pure business rules (entities, value objects, policies) |
| `application/` | Use cases, handlers, ports, agent runner, event bus |
| `contracts/` | Wire types, method roster, golden JSON fixtures |
| `infrastructure/` | JSON/JSONL stores, SQLite credentials, AI adapters, MCP client, tools, docs |
| `transport/` | HTTP RPC, WebSocket, SSE, embedded static serving |
| `cmd/nusashell/` | Composition root, configuration, lifecycle, entrypoint |
| `frontend/` | Native JavaScript/HTML/CSS, embedded via `embed.FS` |
| `testdata/` | Fake stdio MCP server used by handler-level tests |

## Development

```bash
make fmt       # gofmt -w .
make fmt-check # fail if any file is not formatted
make test      # go test -race ./...
make vet       # go vet ./...
make build     # go build ./...
make check     # full verification baseline (fmt-check + test + vet + build)
make run       # build and start the server
```

Requirements: Go 1.22+ (uses `net/http` routing patterns).

The frontend E2E smoke test uses Node.js and JSDOM. Install its dev dependency
once with `npm install`, then run `make test-frontend-e2e`. It starts a real Go
server and drives one representative UI flow through RPC, WebSocket events,
application services, and local persistence.

## Configuration (environment)

| Variable | Default | Purpose |
| --- | --- | --- |
| `NUSASHELL_HOST` | `0.0.0.0` | Listen host |
| `NUSASHELL_PORT` | `9999` | Listen port |
| `NUSASHELL_DATA_DIR` | platform config dir + `nusashell-go` | Data directory |
| `NUSASHELL_DEV` | — | Serve `frontend/` from disk (live reload) |

## Serving mode

- **Production:** frontend assets are embedded into the binary (`embed.FS`)
  and served by the Go server. Single self-contained binary; no Node runtime,
  no `node_modules`, no build step.
- **Development:** `NUSASHELL_DEV=1` serves the same tree from disk without
  changing the handler contract.

See `docs/architecture.md` for the dual-protocol (WS/HTTP+SSE) routing matrix
and serving policies.
