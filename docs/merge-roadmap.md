# NusaShell merge roadmap

NusaShell merges two projects into one binary:

- **NusaLLM** (`llm-base`): the base. It provides the code layout
  (`infrastructure/domain` → `application` → `infrastructure/repository|service|delivery`,
  wired with DI in `cmd/nusashell`), the API turn engine (providers, four
  wire styles, tools, compaction), and the WebUI look.
- **NusaMation**: the feature donor. It contributes the ACP runtime,
  WhatsApp and Telegram channels, projects (multiple workspaces), automations,
  browser use, auth, notifications, and the control-center sidebar.

Both repositories belong to the same owner. Code is copied in and reshaped to
fit the NusaLLM layers, not imported as a dependency. The license is GPLv3.

## Decisions

| Topic | Decision |
|---|---|
| Base architecture | NusaLLM layering. Each NusaMation rule goes to its layer owner; `internal/*` is not copied as-is. |
| ACP vs API | Two **in-process runtime drivers** behind one domain port. ACP agents are external processes driven over stdio. The LLM runtime is the existing NusaLLM job/turn engine. |
| Model picker | One list: each provider (API) and each agent (ACP) is a provider with its models underneath. A room can switch between them at any turn. |
| First run | One binary, one shell: providers and agents always show side by side. With neither configured, the home view is a setup wizard that connects a provider (presets for OpenRouter, OpenAI, Anthropic, Gemini, Ollama, or any endpoint) or an agent CLI (detected on PATH, or a command by hand). The earlier product modes were dropped. |
| UI | NusaLLM visual language (tokens, transcript, tool cards, composer), with the NusaMation sidebar and panels (projects, channels, automations, browser). |
| Terminal REPL | Dropped for now. The CLI keeps only admin commands (`onboard`, `provider`, `config`, `serve`, …). |
| Extra tooling | MCP client, memory, and similar tools come after the merge works. |

## Verification

`make check` is the read-only gate: gofmt, the WebUI syntax check, the host
build, vet, the tests, and a cross-compile of every package for `windows`
and `darwin`. `make test` is the fixing variant — it runs `go fix` and
rewrites sources before verifying them, so it cannot report on what it was
handed. Run `make check` before a commit, and before calling a merge item
done on the strength of "it builds".

## Phase 0: base (done)

- NusaLLM source copied. Module, binary, and env names are now `nusashell`.
- REPL removed: `delivery/cli/{tui,commands,session}`, `application/command`,
  `service/command`, `repository/history`, `service/workspace`, the slash-command
  domain types, and the TUI dependencies. A bare `nusashell` now runs `serve`.
- `docs/cli-guide.md` and the other guides were rewritten for the admin CLI;
  no page still describes the dropped REPL or its slash commands (only the
  changelog and README keep the historical note).

## Phase 1: runtime port (done)

- Domain: `Conversation` binds either a provider or an agent
  (`BindAgent` / `UnbindAgent`, `Runtime()`); `modelID` holds the agent's
  model in agent rooms. The `Agent` aggregate carries command, args, env
  (refs only), and an `ApprovalPolicy`. `ToolAction`, `ApprovalChoice`,
  `ApprovalRequest`, and the `approval` / `approval_resolved` turn events
  are provider-neutral.
- Ports: `repository.Agent`, `service.AgentRuntime` (Prompt, Models,
  Release, Reload, Shutdown), `service.AgentDetector`. Like
  `TextModelService`, one `Prompt` appends exactly one assistant message.
- `infrastructure/service/agent/acp` is NusaMation's ACP framing and process
  code. `infrastructure/service/agent` runs one process per agent and one
  session per conversation, maps `session/update` onto turn events, and
  routes `session/request_permission` to the application's approver.
  `infrastructure/repository/agent` keeps one JSON file per agent with env
  values in the secret store.
- `application/job` branches Send/Resume on the room's runtime. Agent turns
  persist the reply, answer permissions by policy or through the
  `Approvals` broker, and fold steering into a follow-up prompt at turn end.
  Compaction is refused (409) because an agent manages its own context; an
  edit is served by dropping the later turns and the agent's session and
  answering the edited line in a fresh one. Retry re-sends an unanswered
  message.
- Switching runtime inside a room is allowed. Model runtimes replay agent
  tool calls as text (`Conversation.ModelReplay`); an agent starts a fresh
  session with a transcript recap when it did not see the latest turns.
- Delivery: `/api/agents` (CRUD, detect, import, models),
  `/api/conversations/{id}/approvals`, `turn.send` `payload.agent`,
  `turn.approval*` frames, `nusashell agent …`. WebUI: one picker for
  providers (API) and agents (ACP), approval cards, Settings → Agents.

Open items from Phase 1:

- Done in Phase 7, item 7: `fs/*` and `terminal/*`, plans, available
  commands, config options and modes, sign-in, steering, approvals in
  history; MCP servers in `session/new` came with Phase 5.
- Done: an edit in an agent room drops the later turns and the agent's
  session (`Conversation.RewriteFrom` forgets it); the agent answers the
  edited line in a fresh session with a recap.

## Phase 2: storage, auth, and naming (done)

- Naming: one vocabulary in code, API, UI, and docs — provider (API
  endpoint), model, upstream (backend behind an aggregator), effort, agent
  (ACP CLI), runtime (`model` / `agent`). `Gateway` and `route` are gone;
  the unused `Model.route` field was removed. See the table in
  [architecture.md](architecture.md).
- Persistence home: transcripts stay in per-conversation files; relational
  state goes to `nusashell.db`, opened by `repository/store` with embedded
  `migration/NNNN_*.sql` files (`PRAGMA user_version`). Providers and agents
  stay as JSON records with secrets in `secrets.db`.
- Auth: `server.bind`, `server.auth` (`auto` / `on` / `off`), owner password
  (bcrypt) in `owner`, sessions in `sessions` (token SHA-256, CSRF token,
  30-day sliding expiry), login throttling, `/api/auth/{status,login,logout}`,
  middleware with CSRF on unsafe methods, https origins for reverse-proxy
  hosts, WebUI login screen, `nusashell password`.

Open items from Phase 2:

- Agent and provider records could move into `nusashell.db` once a feature
  needs to query them relationally.
- Done: values in `secrets.db` are sealed with AES-256-GCM under
  `secrets.key` (the entry name is associated data). A value that does not
  open reads as absent, so values stored before sealing are set again.

## Phase 3: projects and sidebar (done)

- Domain: `Project` aggregate (name, folders with the first as primary,
  pin, timestamps; stale edits refused with `ErrProjectChanged`);
  `Conversation` gained `projectID` and `pinned`.
- Storage: `migration/0002_projects.sql` in `nusashell.db`; membership and
  pin live on each conversation's `meta.json`.
- Application: `appproject` (create, full edit with `updatedAt`, pin,
  delete that releases member rooms); the `Annotate` job replaced `Rename`
  and sets title, project, and pin on the room's worker. A room in a project
  works in the project's primary folder for every runtime; agents also get
  the other folders as ACP `additionalDirectories`, and an agent session is
  restored under the new folders when they change.
- Delivery: `/api/projects`, `PATCH /api/conversations/{id}` with
  `projectId`/`pinned`, `turn.send` `payload.projectId`.
- WebUI: new sidebar — search, Projects (pinned first, foldable, per-project
  New thread, actions menu), Threads grouped Pinned/Today/Yesterday/Earlier
  with a menu to pin, move between projects, or delete; a project dialog
  (sidebar + and menu, composer chip) with the folder editor; the composer's folder chip names the project.

Open items from Phase 3:

- Done: the model runtime's file tools take an absolute path inside any of
  the project's folders (`dservice.ExtraFolders` on the turn; the
  workspace pin lists them).
- Room labels (`@handle` names) are a channel feature and move to Phase 4.
- Done: a project lists all its threads (up to 100) whatever the main
  list paged in: `GET /api/conversations?projectId=`, and the project
  view carries `threads`.
- Undo for project deletion (NusaMation's restore): done in Phase 7, item 10.

## Phase 4: channels (done)

- Domain: `ChatRef` (channel, chat, thread), `Routing` (`replyToOrigin`,
  `mirrorFinal`, `notifyApprovals`), room labels (`NormalizeLabel`,
  `ValidLabel`, `PickLabel` — animal names), `ChannelLink`; ports
  `ChannelLink`, `InboundLog`, `ChannelSettings`, and the `Channel`
  service port (start/stop, send Markdown, typing, pairing, settings).
- Storage: `migration/0003_channels.sql` (links, inbound dedupe, settings);
  label and routing on each conversation's `meta.json`.
- Application: `appchannel` takes inbound messages, dedupes them, runs
  commands (`/help`, `/new`, `/stop`, `/status`, `/room @label`, `/use`,
  `/approve`, `/deny`), steers a busy room, and otherwise starts a turn
  through the same hub the WebUI uses, so the turn is live on the web too.
  A fresh chat gets a room on `models.chat` (an agent, or a provider +
  model; or its `/use` choice). Final replies go to the origin
  chat and, with `mirrorFinal`, to every linked chat; web-started turns
  mirror through the hub's finished hook. Approval requests ping the chat.
- Adapters: `service/channel/telegram` (Bot API long polling, HTML
  rendering with a plain fallback, 4096-unit splits) and
  `service/channel/whatsapp` (whatsmeow, QR pairing, LID aliases, mention
  handling, group names). Both fail closed: an empty allowlist admits no
  one, and refused senders show in a blocked list. `service/channel/markup`
  renders Markdown per surface, so agents need no channel instructions.
- Delivery: `/api/channels…` (status, settings, connect, disconnect,
  logout, QR, WhatsApp groups) and `/api/conversations/{id}/links`;
  `PATCH /api/conversations/{id}` takes `label` and `routing`.
- WebUI: Settings → Channels (WhatsApp pairing card, Telegram token card,
  allowlists, blocked senders), Settings → Models chat channels role,
  and the thread header's `@label` chip and linked-chats popover with the
  routing toggles.

Open items from Phase 4:

- Media, polls, Telegram inline mode, and per-chat command menus came
  later (Phase 7, item 9). **Done:** WhatsApp delivered/read receipts
  and the files a reply sent now show under the reply in the web
  transcript (`delivery` patch lines, `conversation.updated` notice).
- Done: `nusashell channel` (list, show, set, groups, send, search,
  contacts, user) through the running server, with NusaMation's WhatsApp
  message index (`chat_messages` + FTS5, history sync, retention) and
  send pacing (`sendPacing`, `queueCap`).
- Done: the Telegram token is sealed with the rest of `secrets.db` (see Phase 2).

## Phase 5: automations, browser, notifications, update

- Automations: domain `Automation` aggregate (runtime, schedule, thread
  mode, project, result chat, expiry, `Claim` of the due firing),
  `AutomationRun` entity, and the `Schedule` value object — once at a
  moment or five-field cron (`robfig/cron`) in an IANA zone; `time/tzdata`
  is embedded so zones resolve on hosts without a database. Storage:
  `migration/0004_automations.sql`. `appautomation` ticks every 5 s,
  claims due automations with an optimistic `version` check (one running
  run per automation), and runs each firing through the hub's `Run`, so
  the turn is live on the web. `updated_at` changes only on owner edits
  and is the edit form's conflict token.
  Runs left `running` by a previous process are marked `interrupted` at
  start. Delivery: `/api/automations…` and `/api/chats`. WebUI: Settings →
  Automations (list, run history, form with schedule presets).
- Desktop notifications: `dservice.Notifier` port with
  `service/notify` backends (notify-send / gdbus, osascript, PowerShell)
  detected at start; `appnotify` decides what notifies (web turn
  finished/failed, approval requests, automation runs) and reads
  `notifications.desktop` on each send. `appupdate.Watch` checks for a
  release every hour from the server and notifies once per version.
  Delivery: `/api/notifications` and `/api/notifications/test`; Settings →
  General toggle.
- MCP: `infrastructure/service/mcp` speaks the protocol (server, client,
  in-process pipe). Two built-in servers in `infrastructure/delivery/mcp`
  serve use cases as tools: `conversation` (`application/control`) and
  `browser` (`application/browser` over the `dservice.Browser` port,
  implemented with chromedp in `infrastructure/service/browser`). The
  model runtime is an MCP client (`infrastructure/service/tool/mcp`):
  each server's tools join the catalog and each thread gets its own
  in-process connection. ACP agents get the servers in `session/new`
  (`AgentPrompt.MCPServers`, from `job.Deps.MCPServers`): the agent runs
  `nusashell mcp run <server>`, which relays over loopback HTTP
  (`/api/mcp/{server}`) with a per-thread token (`deliverymcp.Grants`).
- Browser: one headless browser and profile, one tab per thread, idle
  sessions closed after 30 minutes and a thread's session closed with the
  thread. Live view over MJPEG in a thread panel.
- Self-update already exists in both projects; keep the NusaLLM one.

Open items from Phase 5:

- Done: NusaMation's installer — Chrome for Testing into `<storage>/chrome`
  or a package manager (`dservice.BrowserInstaller`), from Settings →
  Tools, `GET/POST /api/browser/install`, or `nusashell browser
  status|install`; proot runs the browser without its sandbox.

- A run that waits on an agent's permission request stays `running` until
  the request is answered in its thread (a desktop notification and the
  thread's approval card ask for it).

## Phase 6: modes and polish

- Done, then replaced: product modes (`product.mode`) hid one runtime in
  the WebUI. They are gone; the first-run step is now a setup wizard on
  the home view (`web/components/setup.js`), shown while no provider and
  no agent exist. It saves through the ordinary provider and agent APIs
  and seeds the composer's model choice.
- Windows support: NusaMation builds for Windows (`procsys`). `make check`
  cross-compiles every package for `GOOS=windows` and `GOOS=darwin` on every
  run, so a ported package that stops building for either platform fails the
  gate instead of resting on a claim.

Open items from Phase 6:

- Windows builds but has not been run end to end (agent processes,
  notifications through PowerShell, the data folder). The build half is now
  gated by `make check`; the run half still needs a Windows host.
  Found by inspection and fixed, still unrun: the ACP terminal child was
  started with a Unix-only, case-sensitive environment allowlist (no
  `SYSTEMROOT`, `COMSPEC`, `PATHEXT`, or even `PATH` on Windows), and
  stopping an agent killed only the direct child, leaving its own children
  running. Both are `infrastructure/service/agent`.

## Phase 7: remaining NusaMation features

The merge is done when NusaShell covers what NusaMation does. Improvements
wait until then. Still to port, in order:

1. Done: MCP servers the owner configures (stdio, HTTP, SSE):
   `aggregate.MCPServer`, `migration/0005_mcp_servers.sql` with values in
   the secret store under `mcp/<id>`, `appmcp`, `/api/mcp-servers`,
   `nusashell mcp …`, Settings → Tools. The model runtime's
   `toolmcp.Registry` (an `EnvironmentBundle.Extra` tool source) holds one
   shared connection per server; `mcp.Spawn` and `mcp.Dial` are the stdio
   and HTTP/SSE transports.
2. Done: conversation tools `search` (text and tool calls across the 300
   most recent threads), `export` (JSONL into the caller's tool output),
   `report` (a note to the chat the running turn came from, once a
   minute; `appchannel.Origin` tracks it).
3. Done: the live view is a WebSocket (`delivery/websocket/browser.go`)
   with takeover input under a lease (`BrowserLease`, `browser_lock`),
   device emulation and presets, and named profiles — one browser
   process per profile, `SwitchProfile` reopens the page and carries
   routes and scripts.
4. Done: request routes, WebSocket routes, init scripts (NusaMation's
   `routes.go`, `ws_routes.go`, `init_scripts.go`), the network log, and
   `browser_wait_for_response`. The URL glob is `valueobject.GlobURL`.
5. Done: `[[env:NAME]]` secrets in typed text (resolved from the thread's
   agent environment, typed through CDP input, masked back in results);
   the login vault — `aggregate.BrowserLogin`,
   `migration/0006_browser_logins.sql` with passwords in the secret store
   under `browser-login/<id>`, the save-login offer in the live view,
   owner autofill, `browser_vault_fill`, Settings → Tools list.
6. Done: browser recordings — NusaMation's recorder and store in
   `infrastructure/repository/recording` (the `drepo.Recordings` port),
   capture in the browser adapter feeding a `dservice.RecordingSink`
   (page events through the in-page tap, navigations, network lines,
   downloads), the agent's browser tool calls on the timeline, live-view
   record/pause/resume/stop, summaries, and the read tools. Page events
   that start a navigation are sent at once. Annotations (elements picked
   in the live view) move to item 8 with the composer.
7. Done: agent session surface. ACP `fs/*` and `terminal/*` client
   methods (inside the session's folders; secrets and `.git/hooks`
   refused), session config options and legacy modes (kept per room in
   `agentOptions`), available commands (the composer's `/` menu), plans,
   approvals recorded in history, approval rules
   (`aggregate.ApprovalRule`, `migration/0007_approval_rules.sql`),
   elicitations (`valueobject.Elicitation` validates answers; the
   `Elicitations` broker mirrors `Approvals`), agent status, stderr logs,
   sign-in and sign-out (`AgentRuntime.Status`, `Authenticate`,
   `Logout`), and `prompt/system/agent-session.md` appended as a system
   prompt when the agent takes one. Steering reaches a running agent turn
   through `AgentRuntime.Steer` (`_session/steering` or a mid-turn prompt)
   and is kept as a `steer` part of the reply; agents without either get
   it as a follow-up prompt. Agent requests now start only after
   the updates sent before them are handled, so a question never shows
   above the text that leads to it.
8. Done: composer and thread. `@` and `/` references
   (`valueobject.Reference`, byte offsets checked against the text, kept
   on the message and spelled out by `AsModelReplay`; candidates from
   `ConversationApp.ReferenceCandidates` over `Workspace.Entries`, which
   honors `.gitignore`), uploads (`drepo.Uploads`, `uploads/`) for model
   and agent turns, retry, JSON export, the side pane's Files tab and
   file preview (`/api/files`), the owner terminal (`dservice.Shells`,
   `infrastructure/system/pty` shared with the tool terminal), and
   live-view annotations: NusaMation's pick pipeline in the browser
   adapter (`BrowserSession.Annotate`), picks kept by `drepo.Picks`
   (`picks/`), and `valueobject.Annotation` on the message with its
   `<annotation-context>` note and screenshots.
9. Done: channels. Media in: adapters download photos, files, voice
   notes, video, and stickers (`InboundMessage.Files`; Telegram buffers an
   album into one message); `appchannel` keeps up to six through
   `drepo.Uploads` and they ride the turn as attachments (`Runner.Run`
   takes them; a message with files queues its own turn instead of
   steering). Media out: a reply's markdown links and `file://` URIs to
   files inside the room's folders (`Rooms.Roots`, read through
   `drepo.Workspace`) go to a chat that is a `dservice.FileSender`, and
   their links leave the text. Polls: a chat that is a `PollSender`
   (WhatsApp) gets the turn's approvals and one-question choices
   (`Elicitation.Choice`) as native polls; only the sender of the turn's
   message answers, a multi-answer poll settles after 15 s of quiet, and
   polls retire when the request is answered elsewhere or the turn ends.
   Replies: a message that replies to another carries what it quotes
   (`InboundMessage.Quote`: the quoted text, or a label such as "an
   image"); `appchannel` puts it on one line ahead of the text, `[In reply
   to: …]` after the sender's name in a group, so a short "yes" still means
   something to the model, and the web shows it as a quote.
   Telegram inline mode (NusaMation's "inline keyboards"): typing
   `@bot <word>` lists rooms and projects (`InboundHandler.Mentions`), and
   a pick's `@tokens` become references (`Project.Handle` for projects).
   Command menus: a chat that is a `CommandMenu` (Telegram) gets the room's
   agent commands after the channel's own, per chat, after each turn and on
   `/room`; `/new` puts the default menu back.
10. Done: projects, undo a deletion. Deleting a project shows an Undo
   toast; `POST /api/projects/{id}/restore` (`ProjectApp.Restore`)
   recreates it under its old id with name, folders, and pin, and the
   threads the delete released rejoin it while they are still outside
   any project. The move is a compare-and-set on the room's worker
   (`Annotation.FromProjectID`, 409 when the room moved), which `Delete`
   also uses so it never releases a room that joined another project.

## Phase 8: gaps found by a second audit

A route-by-route and tool-by-tool comparison against NusaMation found
features Phase 7 left out.

1. Done: thread drafts and the side pane kept on the server
   (`drepo.ThreadViews`, `migration/0011_thread_views.sql`,
   `/api/conversations/{id}/view`, `web/lib/views.js`) — NusaMation's
   conversation meta.
2. Done: an agent's terminal sign-in in a WebUI terminal
   (`AgentRuntime.SignInCommand`, `dservice.ShellCommand`,
   `/api/agents/{id}/sign-in`).
3. Done: changing the owner password from the WebUI
   (`AuthApp.ChangePassword`, `POST /api/owner/password`).
4. Done: what a new thread starts on, kept for every device — the
   `models.threads` role (Settings → Models → New threads). The agent's
   session options were already per-agent `defaults`.
5. Done: the browser panel's console (the page's log and errors, and
   JavaScript run in the page: `/api/conversations/{id}/browser/console`,
   `…/browser/eval`), address-bar suggestions from each profile's history
   (`drepo.BrowserHistory`, `migration/0012_browser_history.sql`,
   `Browser.OnLoad`, `/api/browser/history`), and the agent's pointer on
   the live view (`BrowserState.agentPointer`). NusaMation's proxied
   DevTools frontend was left out: the console covers what the owner used
   it for, without exposing the browser's debugging port.
6. Done: attach a browser recording to a message and the
   `browser_get_annotation` tool. `valueobject.RecordingRef` rides
   `UserInput`/`Message`; `turn.send` takes `payload.recordingId` and
   `ConversationApp.Recorded` resolves it at send time (a live recording
   gets a meta brief); the message stores the summary and the model/agent
   reads a bounded `<recording-context>` note. The composer, kept views,
   and transcript carry the recording chip; a recording alone is a
   message and `turn.steer` refuses one. `browser_get_annotation` resolves
   a pick by id to its descriptor JSON and marked screenshot.
7. Done: CLI `automation` (list/get/add/set/pause/resume/remove/history/
   run; `set` merges with `updatedAt`), `model <thread> [selector]`
   (`agent:<id>[/<model>]` or `<provider>/<model>`), and `conversations`
   (the conversation MCP tools over `--url`/`--token`, with the per-thread
   token from the MCP child).
8. Done: Windows install (`scripts/install.ps1`, hidden logon Task
   Scheduler task, Start Menu `.url`,
   `%LOCALAPPDATA%\NusaShell\bin\nusashell.exe`) and Windows release
   assets; Linux `serve install`
   writes a per-user `nusashell.desktop` launcher and icon. The Task
   Scheduler adapter renders UTF-16 task XML, keeps `Spec.Env` out of the
   XML (the installed config path and logon environment are the service's
   environment), and asks PowerShell for locale-free status.
9. Done: delete every thread at once (`DELETE /api/conversations` with
   `{"confirm":"DELETE"}`) preempts running turns and then purges the
   artifacts rooms own — uploads, picks, recordings (live ones finalized),
   and saved views, fresh-room view included; a failed room aborts the
   global purge and names the ids. `server.public_url` plus
   `serve --open` (existing server detected), and the
   `conversation-control` activity ring (`GET
   /api/conversation-control/activity`, Settings → Tools).

## Later

- Memory and more tools.
- CLI chat client against the running server, if wanted.
