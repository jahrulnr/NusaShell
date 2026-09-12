# OpenClaw hooks

Four surfaces that share the word “hooks”. Do not mix them.

| Surface | Register | Use for |
|---------|----------|---------|
| **Typed plugin hooks** | `api.on(name, handler, opts?)` | Policy, mutate, claim, ordered middleware |
| **Internal / HOOK.md** | Hook dirs + `openclaw hooks enable`; plugins via `api.registerHook` | Operator automation (`/new`, gateway startup, …) |
| **Legacy `registerHook`** | `api.registerHook(events, …)` | Internal bus only — **not** typed policy |
| **HTTP Gateway hooks** | `hooks.enabled` + token | External `POST /hooks/*` wake/agent (separate product) |

## Typed catalog (by group)

**Agent turn:** `before_model_resolve`, `agent_turn_prepare`, `before_prompt_build`,
**`before_agent_run`**, **`before_agent_reply`**, **`before_agent_finalize`**,
`agent_end`, `heartbeat_prompt_contribution`.

**Conversation observe:** `model_call_started` / `model_call_ended`, `llm_input`,
`llm_output`.

**Tools:** **`before_tool_call`**, `after_tool_call`, `resolve_exec_env`,
**`tool_result_persist`** (sync), **`before_message_write`** (sync).

**Messages / delivery:** **`inbound_claim`**, `channel_pairing_requested`,
`message_received`, **`message_sending`**, **`reply_payload_sending`**,
`message_sent`, **`before_dispatch`**, **`reply_dispatch`**.

**Sessions:** `session_start` / `session_end`, `before_compaction` /
`after_compaction`, `before_reset`.

**Subagents:** `subagent_spawned` / `subagent_ended` / `subagent_progress`,
`subagent_delivery_target` (prefer over deprecated `subagent_spawning`).

**Lifecycle:** `gateway_start` / `gateway_stop`, `cron_reconciled` /
`cron_changed`, **`before_install`**, **`skill_proposal_evaluate`**,
`skill_proposal_changed`, `skill_changed`.

Bold names accept decisions (block / cancel / override / requireApproval).

### Registration options

| Option | Effect |
|--------|--------|
| `priority` | Higher runs first on **decision** paths only |
| `matcher` | Canonical tool ids for tool hooks (`exec`, `apply_patch`, …) |
| `timeoutMs` | Per-handler await budget (does **not** cancel work) |
| `registrationId` | Stable id (skill evaluators) |
| `eligibleTriggers` | `before_agent_reply` only: `cron` \| `heartbeat` \| `user` |

Security: prompt-injection hooks need `allowPromptInjection`; conversation
content hooks need `allowConversationAccess` for non-bundled plugins.

## Dispatch

1. Decision / mutate / claim → **sequential**, priority desc, then registration order.
2. Observation (void) → **parallel** — do not order via priority.
3. Claim hooks → first `{ handled: true }` wins (`inbound_claim`, `before_agent_reply`, …).
4. Sync-only: `tool_result_persist`, `before_message_write`.

### Fail policy

| Hook | Timeout / error |
|------|-----------------|
| `before_tool_call`, `before_install`, `before_agent_run` | **Fail-closed** (deny) |
| Most others | Fail-open (log + continue) |

Default budgets (override via plugin config): policy / outbound mutate **15s**;
`gateway_stop` **5s**; compaction / `agent_end` **30s**;
`skill_proposal_evaluate` **120s**. Cap 600000 ms.

## Internal HOOK.md events

Known keys: `command:new|reset|stop`, `session:auto-reset`,
`session:compact:before|after`, `session:patch`, `agent:bootstrap`,
`gateway:startup|shutdown|pre-restart`,
`message:received|transcribed|preprocessed|sent`, plus bare families
`command` | `session` | `agent` | `gateway` | `message`.

Unknown names stay silently dead (loader warns). Handlers may push
user-visible lines into `event.messages`. Sequential; errors isolated.

**Must not** own long-lived timers/sockets — use plugin services + typed
`gateway_start` / `gateway_stop`.

## `api.on` vs `registerHook`

| | `api.on` | `registerHook` |
|--|----------|----------------|
| Bus | Typed runner | Internal event bus |
| Policy / mutate | Yes | No |
| Typed name registered here | Correct | **Warns; never invoked by typed runner** |

## Edge cases

- Timeout stops awaiting only; the handler may keep running afterward.
- Shutdown / restart `session_end`: short **shared** drain across all sessions/handlers (~2s).
- `inbound_claim` is binding-owner targeted, not a global broadcast.
- Block `reason` is internal — do not log/expose verbatim to users.
- Prompt mutations change the submitted turn; only compaction rewrites history.
- HTTP `/hooks` ingress ≠ typed hooks; dedicated token and session-key rules apply.
- Telemetry-only → diagnostic events, not hooks.

## Best practices

1. Policy → `api.on`; operator scripts → `HOOK.md`; telemetry → diagnostics; external wake → HTTP `/hooks`.
2. Never put typed names on `registerHook`.
3. Use `matcher` with canonical OpenClaw tool ids.
4. Own cancellation for long work; do not rely on hook timeout.
5. Keep shutdown finalizers crash-consistent under the shared drain budget.
6. Prefer existing catalog names; closed set is intentional.
