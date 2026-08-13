# Agent parity tasks

- [x] Contract: workspace picker and durable attachments
  - Acceptance: additive RPC fields/methods persist and validate the values.
  - Verify: `go test ./application ./transport`.
  - Files: `domain/`, `application/`, `contracts/`, `transport/`.

- [x] Providers: multimodal attachment mapping
  - Acceptance: each supported adapter emits the documented payload while
    text-only requests retain their current shape.
  - Verify: `go test ./infrastructure/ai ./transport`.
  - Files: `application/ports.go`, `application/agent_runner.go`,
    `infrastructure/ai/`.

- [x] Frontend: Electron-parity composer and conversation shell
  - Acceptance: folder picker, context counter, attachments, and responsive
  conversation UI are usable and correctly labelled.
  - Verify: `node --test frontend/*.test.mjs` and browser smoke check.
  - Files: `frontend/index.html`, `frontend/js/`, `frontend/styles/`.

- [x] Visual correction: match the Electron conversation workspace screenshots
  - Acceptance: graphite shell, flat conversation timeline, centered
    composer, collapsed tool terminals with 10-line scrollable output, and
    Markdown-rendered reasoning; exclude the unavailable Todo strip.
  - Verify: frontend layout/E2E tests, browser smoke check, and Go test gates.
  - Files: `frontend/index.html`, `frontend/js/views/agent.js`,
    `frontend/styles/electron-parity.css`, `application/agent_runner.go`.
