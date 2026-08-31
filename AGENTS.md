# AGENTS.md - NusaShell

Instructions for humans and coding agents working in this repository.

## Agent behavior

- Always acting as a Senior fullstack developer.
- Take end-to-end ownership of the work: understand the architecture, clarify boundaries, implement the correct solution with the smallest maintainable surface (see KISS), test it, and verify non-functional behavior.
- Make decisions grounded in repository evidence and established project constraints; do not hide uncertainty or invent completed work.
- When backend has changed, makesure frontend is working. Refactor frontend behavior if backend have some breaking changes.

## Architecture principles

- **TDD:** follow red → green → refactor. Write or extend a failing test for behavior before implementation; make the smallest change that passes; refactor only while tests are green.
- **Clean Architecture:** keep business rules independent from delivery, persistence, frameworks, and external services. Dependencies point inward.
- **Dependency rule:** `domain` imports no application, infrastructure, transport, HTTP, WebSocket, SSE, database, filesystem, MCP SDK, or UI code. Outer layers may depend on inner layers, never the reverse.
- **KISS:** "simple" does not mean writing the thinnest or least code — it means writing the most maintainable, readable, reuse existing struct, method or function effectively based on boundary, and idiomatic code that solves the requirement without bloat. Follow best practices for the language and codebase (named abstractions at real boundaries, explicit error handling, no speculative generality), and keep the code from growing unchecked by cycling red → green → refactor (RGR): write a failing test, make it pass, then refactor while green so the code stays clean.
- **SOLID:** keep responsibilities focused, depend on small interfaces at boundaries, preserve substitutability, and avoid forcing consumers to depend on unused APIs.
- **Testability:** keep I/O at adapters and inject clocks, filesystem, network clients, process runners, and other effects. Code must support deterministic unit tests and isolated integration tests.
- **Non-functional testing:** support tests for concurrency and race safety, cancellation, timeouts, ordering, backpressure, resource cleanup, startup/shutdown, compatibility, observability, and performance where the contract requires it.
- **Logging:** use structured logging with context, include request IDs, and log at appropriate levels (debug, info, warn, error), traceable from who to whom like pirate find the treasure.

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

## Provider adapters (core port)

The ported chat provider wire packages under `infrastructure/ai/` are
ported from the core provider tree and must stay structurally close to
upstream. This rule applies to the wire implementations, not to every
infrastructure adapter or the AI composition root:

- `infrastructure/ai/core/` — core Blocks-based types (`Request`,
  `Message`, `Block`, `Thinking`, `Tool`, `Response`, `Stream`, error
  model). This is the shared contract between NusaShell and the providers.
- `infrastructure/ai/anthropic/`, `infrastructure/ai/openai/`,
  `infrastructure/ai/openrouter/`, `infrastructure/ai/compat/` — the
  ported providers. They only import the core package, never
  `nusashell/application` or `nusashell/domain`.
- `infrastructure/ai/adapter.go` — the single thin adapter implementing
  `application.AIProvider`: translates `application.ChatRequest` →
  `core.Request` (blocks, attachments, effort, strip params, prompt
  cache) and maps core errors → `application.UpstreamError`. It is the
  only request/response translation bridge between the application chat
  contract and the core provider contract.
- `infrastructure/ai/factory.go`, `handler.go`, `models.go`, `internal/`,
  and the media client packages (`imagegen`, `stt`, `tts`, `videogen`) are
  outer adapters/composition helpers, not ported wire implementations. They
  may import application ports and domain entities when constructing or
  adapting those ports. Likewise, infrastructure adapters such as
  `acpruntime`, `tools`, and `journal` may import application interfaces and
  domain entities; this is the intended adapter → inner-layer direction, not
  a dependency-rule exception. They must not import concrete application
  services or make domain depend on infrastructure.

Supported provider kinds are `messages`, `responses`, and `chat`
(OpenRouter hosts are auto-detected by Base URL). There is intentionally
no fallback layering: stream-unsupported, empty-stream, and image-strip
retries were removed — errors surface explicitly to the retry loop.

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

## Removing old code and breaking changes

- **Dead or superseded code is deleted, not kept.** If a component, adapter, fallback path, or compatibility shim is no longer reachable or no longer reflects how the system works, remove it in the same change instead of leaving it behind "just in case". Unused code is a maintenance tax: it rots, misleads readers, and inflates the surface the next change must consider.
- **Breaking changes are confirmed with the user first.** Anything that alters persisted data, wire contracts, public RPC methods, provider semantics, or user-visible behavior must be surfaced to the user before implementation — do not silently break old behavior.
- **Fallbacks to old code are opt-in, not automatic.** Before adding a fallback layer (old path + new path), ask the user whether they need it. Fallback is a complexity multiplier: duplicated logic that must be maintained twice, diverging behavior, and silent masking of errors. The default is one correct path, with the old code deleted — not a fallback ladder.

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
  `.experimental/sse-multiline/`, `.experimental/agent-round-spike/`).
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
    personal `omniroute` / `9router` tokens, local OAuth fixtures tied
    to a single machine. These may be read from
    the user's home directory at runtime and quoted in experiment output
    because exposure to the internet or another git checkout does not let
    anyone else use them.
  - **Forbidden:** credentials that would let a third party impersonate the
    user or bill against an account from anywhere — e.g. raw OpenAI API
    keys, Anthropic API keys, production OAuth client secrets, anything
    that works outside the originating machine. Never commit these to
    `.experimental/`; load them from the environment or a git-ignored file
    under the user's home directory instead.
  - When in doubt, prefer reading from `~/.config/...` or an env var over writing the value into a file under
    `.experimental/`.
  - If a credential must live in a file under `.experimental/` (e.g. a
    fixture JSON), add it to `.gitignore` so it never gets committed.
- Reference experiments in PRs or decisions when they informed the final
  implementation, but do not depend on them at runtime.
