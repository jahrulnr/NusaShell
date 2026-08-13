# Spec and implementation plan: Electron-parity agent workspace

## Objective

Bring the Go port's Agent view to the Electron conversation experience for the
requested scope. A conversation has a persistent workspace chosen through the
host folder picker, an honest context-usage counter, and durable image, PDF,
and UTF-8 text attachments. The composer and conversation list use the
Electron renderer's labels, controls, interaction, and visual hierarchy.

## Stack and commands

- Go 1.26 with the repository's HTTP/RPC contracts and embedded native JS UI.
- Node's built-in test runner plus JSDOM for frontend behavioural tests.
- Build: `go build ./...`
- Focused Go tests: `go test ./application ./infrastructure/ai ./transport`
- Frontend tests: `node --test frontend/*.test.mjs`
- Repository gates: `gofmt -w .`, `go test ./...`, `go test -race ./...`,
  `go vet ./...`, `go build ./...`

## Project structure and style

- `domain/` owns conversations and attachment values without transport types.
- `application/` owns the picker port, turn validation, and provider-neutral
  attachment mapping.
- `contracts/` adds optional, additive RPC fields and methods.
- `infrastructure/` implements host folder selection; `transport/` tests RPC
  behaviour; `frontend/` renders the composition and attachment controls.
- Follow existing Go formatting, explicit JSON fields, and native ES modules.

## Contract decisions

1. Workspace remains a per-conversation optional absolute path. Choosing it
   calls a host picker and returns the updated conversation; cancel is a
   successful no-op. The selected path is included in the model system prompt.
2. Attachments are persisted with the user message. Image/PDF data is a data
   URL; UTF-8 text is persisted as text. The backend permits at most four
   attachments and 4 MiB per encoded attachment, matching Electron's limits.
3. The existing text-only provider payloads remain byte-for-byte compatible.
   A user message gains a multimodal content array only when it has
   attachments. Supported adapters map it to their documented wire forms.
4. Context usage is an estimate of conversation context versus the selected
   model's advertised window. If the model has no window, the compaction
   threshold is shown as the conservative effective window.

## Boundaries

- Always: tests first, preserve existing JSON fields, use the host folder
  picker, and keep the frontend unbundled.
- Ask first: adding third-party dependencies, changing provider configuration,
  or extending workspace into new filesystem tools.
- Never: expose a workspace picker by accepting arbitrary browser-supplied
  filesystem paths, weaken attachment limits, or remove existing tests.

## Task list

### Phase 1: contract and persistence

1. Add domain/RPC models and picker port for workspace plus attachments.
   Verify focused application and transport tests, including cancellation and
   persisted JSON.
2. Map durable attachment values to the neutral provider request and the
   OpenAI-compatible, Responses, and Anthropic payloads. Verify request-shape
   tests and existing adapter integration tests.

### Phase 2: Electron-parity composer

3. Add Electron-equivalent composer markup, folder/attachment controls, drag
   and drop, chips, per-conversation workspace label, and context counter.
   Verify frontend unit tests and the browser smoke flow.
4. Rework the conversation shell styles and responsive rules to the Electron
   hierarchy without changing unrelated views. Verify screenshots and
   accessibility labels in a real browser.

### Checkpoints

- After Phase 1: workspace and attachment RPC paths are red-green covered.
- After Phase 2: a folder can be selected, attachments survive reload and are
  sent to the provider, and the context counter updates on model/thread state.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| A browser cannot disclose a usable absolute directory path | Invoke the local host folder picker through the Go composition root; do not rely on browser directory handles. |
| Provider wire formats differ | Keep text-only requests unchanged and assert each multimodal shape with adapter tests. |
| Folder dialog is unavailable | Return a clear, typed RPC error; do not persist an invented path. |
| Large binary data bloats persistence | Enforce Electron's four-item and 4 MiB limits at the frontend and server boundary. |

## Success criteria

- Conversation rows, search empty state, composer footer, and workspace label
  match Electron's requested Agent UX.
- Folder picker persists an actual chosen path for only that conversation.
- The UI accepts/removes/drops up to four supported attachments and rendering
  still works after the conversation is reopened.
- The provider receives attachments in its documented multimodal payload.
- The counter reads `<estimated>/<effective window> context` and refreshes on
  opening a conversation, model changes, compaction, and turn completion.

## Completion record

Implemented and verified on 2026-08-13. Focused contract and adapter tests,
the frontend JSDOM smoke test, a real-browser attachment-only composer flow,
`go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...`
all passed.
