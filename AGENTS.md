# AGENTS.md - NusaShell

Instructions for humans and coding agents working in this repository.

## Agent behavior

- Always acting as a Senior fullstack developer.
- Take end-to-end ownership of the work: understand the architecture, clarify boundaries, implement the smallest correct solution, test it, and verify non-functional behavior.
- Make decisions grounded in repository evidence and established project constraints; do not hide uncertainty or invent completed work.

## Architecture principles

- **TDD:** follow red → green → refactor. Write or extend a failing test for behavior before implementation; make the smallest change that passes; refactor only while tests are green.
- **Clean Architecture:** keep business rules independent from delivery, persistence, frameworks, and external services. Dependencies point inward.
- **Dependency rule:** `domain` imports no application, infrastructure, transport, HTTP, WebSocket, SSE, database, filesystem, MCP SDK, or UI code. Outer layers may depend on inner layers, never the reverse.
- **KISS:** prefer the smallest explicit design that solves the current requirement. Avoid speculative abstractions, framework-heavy solutions, and premature optimization.
- **SOLID:** keep responsibilities focused, depend on small interfaces at boundaries, preserve substitutability, and avoid forcing consumers to depend on unused APIs.
- **Testability:** keep I/O at adapters and inject clocks, filesystem, network clients, process runners, and other effects. Code must support deterministic unit tests and isolated integration tests.
- **Non-functional testing:** support tests for concurrency and race safety, cancellation, timeouts, ordering, backpressure, resource cleanup, startup/shutdown, compatibility, observability, and performance where the contract requires it.

## Layer responsibilities

- `domain/`: pure entities, value objects, policies, and domain services.
- `application/`: use cases, handlers, ports, orchestration, and application events.
- `contracts/`: wire types, serialization, protocol versions, and boundary validation.
- `infrastructure/`: implementations of application ports and external adapters; no business rules.
- `transport/`: HTTP/RPC, WebSocket, SSE, session/subscription handling, and protocol mapping.
- `cmd/nusashell/`: composition root, configuration, lifecycle, embedded frontend serving, and process entrypoint.
- `frontend/`: native JavaScript, HTML, CSS, and static assets. Use browser APIs and ES modules; do not require a production Node build.
- `testdata/`: stable fixtures, golden files, compatibility samples, and non-secret test assets.

## Protocol and frontend rules

- WebSocket is for event-driven updates, subscriptions, and real-time/agent streaming.
- HTTP request/response is for predictable commands, queries, polling, health, and read models.
- SSE is for reconnect-friendly server-to-client streams where bidirectional WebSocket behavior is unnecessary.
- Document the routing matrix, event IDs, sequence/order, deduplication, replay/cursor, reconnect, timeout, cancellation, and backpressure policies.
- Keep wire compatibility intentional: test JSON field names, casing, null/omission behavior, enum values, and errors with golden fixtures.
- Embed production frontend assets with Go (`embed.FS` or equivalent). Test MIME types, module imports, deep links, cache behavior, and 404 handling.
- Do not hide frontend traceability behind a required bundle. If an exception is approved, document source mapping and debugging procedure.

## Frontend style

- `frontend/` is native JavaScript, HTML, and CSS. Use browser APIs and ES
  modules; do not require a production Node build.
- Do not render visible native browser controls or dialogs (`<select>`
  option menus, `alert()`, `confirm()`, `prompt()`). Use a styled select
  library (e.g. Slim Select) or custom components that match the existing
  visual language. Native controls should only appear as a last resort.

## Verification baseline

Before considering a change complete, run the narrowest relevant tests first, then the repository gates:

```text
gofmt
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Do not weaken or delete tests merely to make a suite pass. Do not commit secrets, generated binaries, or production credentials. Keep changes small and focused; do not auto-commit or auto-push.

## Change documentation

When a behavior or public wire contract changes, update the relevant package documentation and golden fixtures. Record intentional compatibility breaks and non-functional trade-offs explicitly.

## Documentation sync (required)

The agent's product knowledge comes from the embedded corpus in
`resources/agent/docs/*.md` (surfaced via the `docs` dispatcher tool,
op="search" / op="read")
and the system prompt in `application/prompts.go`. Outdated docs make the
agent hallucinate capabilities, misdescribe the UI, or give wrong answers.
**Any change that affects user-visible behavior, agent capabilities, or the
UI must update the matching documentation in the same change.**

When adding, renaming, removing, or changing:

- **Agent tools or built-in tool list** → update `resources/agent/docs/tools.md`
  and the tool advertisement in `application/prompts.go` in the same change.
- **Provider kinds, auth model, base URL rules, or model import behavior** →
  update `resources/agent/docs/providers.md`.
- **Automations, pipelines, CI runs, scheduling, or webhooks** → update
  `resources/agent/docs/automation.md`.
- **Plugins / MCP servers, tool discovery, install/register/enable flows** →
  update `resources/agent/docs/mcp.md`.
- **ACP subagent delegation, async completion, or permissions** → update
  `resources/agent/docs/agent-subagents.md`.
- **Image/audio/video/document attachments, vision fallback, read_media,
  or folder attachments** → update
  `resources/agent/docs/agent-attachments.md`.
- **Data files, data directory layout, or persisted artifacts** → update
  `resources/agent/docs/data-locations.md`.
- **Skills, memory, or learning subsystem behavior** → update the matching
  `resources/agent/docs/skills.md` / `resources/agent/docs/memory.md`.
- **System prompt rules or identity** → update `application/prompts.go` and
  the matching `resources/agent/prompts/*.md` file.

Docs under `resources/agent/docs/*.md` are **agent work guidance**: each
workflow doc must include concrete good/bad tool-call examples so the agent
uses tools precisely instead of guessing. Non-workflow facts (single tool
use, UI mechanics that do not change tool-calling) belong in the system
prompt, not in the docs corpus.

A change is not complete until the corpus reflects the new behavior. CI does
not yet gate non-UI docs for drift, so the agent author is responsible for
keeping them in sync. When in doubt, search the corpus for the changed
concept (`docs` tool, op="search") and update every page that mentions it.

## UI knowledge docs (required)

When changing launcher or view UI:

- Update `resources/agent/docs/ui-source/ui-map.json` and regenerate
  `resources/agent/docs/ui-*.md` by running `make scan-ui-docs` whenever
  a `data-view`, view control, button, modal, or interaction in `frontend/`
  is added, renamed, removed, or changed.
- The CI `test-backend` job runs `go run ./cmd/scan-ui-docs -check` and fails
  if any view is undocumented or a mapped control ID is missing from source,
  or if committed `ui-*.md` differ from generated content (drift gate).
- The CI `build` job regenerates `ui-*.md` before `go build` so the embedded
  corpus is always fresh.
- Do **not** edit `resources/agent/docs/ui-*.md` files manually; they
  are generated from the UI map.

## Experiments (`.experimental/`)

`.experimental/` is the approved scratch space for proving a theory before it
lands in NusaShell. Use it for: testing logic, testing upstream behavior
(provider quirks, SSE edge cases, OAuth flows), spike implementations,
side-by-side comparisons with other projects, and any proof-of-concept that
should not touch the production tree yet.

- Create a new subfolder under `.experimental/` per experiment (e.g.
  `.experimental/sse-multiline/`, `.experimental/codex-oauth-flow/`).
  Create the folder if it does not exist.
- One experiment per folder; keep experiments isolated and self-contained.
- Experiments are not part of the build: they must not be imported by
  `cmd/nusashell`, `application`, `domain`, `contracts`, `infrastructure`, or
  `transport`. They are not covered by `go test ./...` from the repo root
  unless they carry their own `go.mod` or are explicitly wired in.
- Treat an experiment as throwaway evidence, not a permanent feature. Once
  the theory is proven, port the minimal correct version into the real
  package and delete or archive the experiment folder.
- Credentials policy is scoped to `.experimental/` only:
  - **Allowed:** local-only credentials that are useless if leaked — e.g.
    personal `omniroute` / `9router` tokens, `~/.codex/auth.json` reads,
    local OAuth fixtures tied to a single machine. These may be read from
    the user's home directory at runtime and quoted in experiment output
    because exposure to the internet or another git checkout does not let
    anyone else use them.
  - **Forbidden:** credentials that would let a third party impersonate the
    user or bill against an account from anywhere — e.g. raw OpenAI API
    keys, Anthropic API keys, production OAuth client secrets, anything
    that works outside the originating machine. Never commit these to
    `.experimental/`; load them from the environment or a git-ignored file
    under the user's home directory instead.
  - When in doubt, prefer reading from `~/.config/...`, `~/.codex/...`, or
    an env var over writing the value into a file under
    `.experimental/`.
  - If a credential must live in a file under `.experimental/` (e.g. a
    fixture JSON), add it to `.gitignore` so it never gets committed.
- Reference experiments in PRs or decisions when they informed the final
  implementation, but do not depend on them at runtime.
