# Changelog

All notable changes to the Go core will be documented in this file. The Electron wrapper and pets keep their own changelogs in [`apps/electron/CHANGELOG.md`](apps/electron/CHANGELOG.md) and [`apps/pets/CHANGELOG.md`](apps/pets/CHANGELOG.md).

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.10.4] - 2026-10-27

### Fixed

- ACP agents that migrated to configOptions-only `session/new` responses (Devin's `sessionDefaultConfigOptions` generation sends `modes: null`/`models: null`) had their mode/model catalogs frozen at whatever was cached before the migration: Refresh catalog ran but the configOptions gap-fill was skipped by the stale non-empty cache. The guard now compares the current session's payloads, so Refresh catalog replaces the catalog (e.g. Devin's `swe-2-*` models appear again). Mode/model selection also follows the advertised shape — `session/set_config_option` for configOptions-only sessions (Devin no longer serves `session/set_model`), legacy `session/set_mode`/`session/set_model` otherwise — and `config_option_update` notifications now update a live run's mode/model.

## [0.10.3] - 2026-10-25

### Changed

- The provider editor now asks only for a protocol (Chat, Responses, Messages, Gemini, Codex) instead of vendor × protocol profiles; the implementation driver is derived from the protocol and base URL (Chat always uses the OpenRouter-compatible wire; Messages/Responses use the direct Anthropic/OpenAI wire unless the base URL is openrouter.ai); the `driver` field is removed from `ai.providers.save` and the provider DTO.

- Providers, Logs, and Learning now use the same desktop workbench layout as Skills and Plugins: a single toolbar band and edge-to-edge split panes that fill the window. Logs merges its filter and action strips into one band, Learning's overview fills the full height with the knowledge map beside the brief and recent learning, and Providers and Learning share one tab style. Mobile layouts are unchanged.

### Fixed

- Opening a room, starting a new chat, or opening the model picker no longer stalls for seconds on large catalogs and transcripts — the model select renders options lazily in a capped window that grows on scroll (instead of rebuilding ~2,900 option nodes on every room/lifecycle sync), room rows reconcile in place with the icon pass scoped to the list, tool-call arguments parse lazily when a widget actually reads them, model labels resolve through a cached catalog index instead of a per-event linear scan, and the transcript mounts its first widgets immediately while the rest of the window appends across frames.

## [0.10.2] - 2026-10-16

### Removed

- The pre-0.10.0 providers view (`initProviders` and its render tree in `js/views/providers.js`, plus the orphaned `styles/providers.css`) was deleted — unreachable since the widget-shell rewrite; the file now only holds the cache-TTL/reasoning-summary helpers the live page imports.

### Fixed

- The conversation transcript no longer shows a phantom "Load older · N" sentinel after the first exchange: the tail window now grows by the live and steer rows that commit into history instead of counting them as hidden.

- Turn-failed cards no longer duplicate when the round stream and the turn-error lifecycle event both settle the same round — the second mount adopts the already-committed row instead of appending a twin.

- The composer model picker search now matches the model id (and provider prefix), not only the display name — an id fragment like `gemini-3.1` finds `Gemini 3.1 Pro`.

- Provider cache TTL surfaces no longer append a "via upstream" qualifier to the sendable value.

## [0.10.1] - 2026-10-16

### Added

- Conversations can be compacted on demand from the room menu or the Context dock. The request uses the existing compaction pass, refuses while a turn is running, and reports when a model compacts on the provider instead of through a separate summary call. A room deleted during that pass is not written back, a rename made while the summary runs is kept, and a panic still emits `agent.compaction.failed` so the composer does not stay stuck.

### Fixed

- Settings → Remote access never populated the pairing panel: the settings page was mounted with a wrapped RPC client that defeated the `rpcClient === defaultRPC` guard, so `refreshPairingPanel` never ran. The refresh is now injected (`pairingRefresh` option); the remote-access toggle reveals the panel and wires its controls via `populate: false` without querying sessions mid-restart; typed remote addresses now stage as `remote_access_addresses` edits; a paired remote client keeps the section suppressed across re-renders once `PAIRING_UNAUTHORIZED` marks it host-only; and "Revoke all sessions" unhides while paired sessions exist.

- Expired pairing sessions are no longer invisible state: Paired devices lists them dimmed with an "Expired" badge (revoking is still the cleanup path), and a remote device whose session lapsed now gets `PAIRING_SESSION_EXPIRED` with a distinct re-pair gate message instead of the generic `PAIRING_REQUIRED` copy shown to never-paired devices.

- File tool cards no longer offer a "Preview file" action for targets that can never resolve in `/local-file`: directory tools (`file_list`, `file_mkdir`), deleted paths (`file_delete`), and `file_info` results reporting `exists: false` or `dir: true` — the button was a guaranteed 404.

- "Open subagents" on a delegation card now selects that run in the dock — the card emits the child run id recovered from the spawn result (yamlmd `runs:` block or the brief terminal line) instead of an args `id` that never exists — instead of just opening the panel on whatever run was already active.

## [0.10.0] - 2026-10-16

### Changed

- Replace the native web frontend with the widget-based shell, responsive views, streaming tool widgets, model capability picker, and per-account quota display.

### Fixed

- Preserve Codex usage-window durations in account snapshots so clients can label session, weekly, and monthly quota accurately.

- **An applied steer stays between the round it followed and the reply it prompted.** When the next round opened before `agent.steer.applied` arrived, the live thread mounted the steer below that reply; the live order now matches the persisted transcript after a reload.

- Add Codex device-code start/poll RPCs for sign-in from remote and mobile browsers; retain the explicit browser PKCE flow and persist approved accounts through the existing credential store.

- **The experimental Context dock is now an inspector that separates
  estimated from measured numbers.** The fill meter keeps its used/window
  reading with an explicit source label, the preflight event renders as a
  "Next request · estimated" group (total plus system, conversation, and
  tool-definition rows with share meters), and "Latest usage · measured"
  shows the whole-turn `turn.done` usage — or, after a reload, the last saved
  response — with cache read/write, the model that ran, and a catalog-priced
  cost. Pricing resolves against the provider that served the run; a same-id
  model on another provider never lends its rates, an ambiguous bare id stays
  unpriced, and costs that exclude cache-write tokens (which have no catalog
  rate) are labelled "excl. cache writes". Unknown fill or window now renders
  as an unavailable dash in the topbar meter and dock instead of a fabricated
  0%.

- **Anthropic thinking signatures and redacted thinking now replay through
  provider switching.** The Messages adapter captures signature and
  `redacted_thinking` data into the persisted opaque reasoning payload
  (including signatures that stream in a separate delta), stamps each stored
  payload with the provider family that produced it, and echoes it back as a
  signed thinking block on the next turn — so tool loops and
  `display: "omitted"` replies no longer 400 for missing signatures or a
  missing leading thinking block. Persisted reasoning extras now replay only
  to the provider family that produced them: plaintext-only or
  foreign-provider reasoning is dropped instead of reaching Anthropic as an
  unsigned thinking block, and extras captured before the producer marker
  existed keep their previous per-kind behavior.

- **Opaque reasoning replay is now scoped to the serving provider instance
  and upstream route.** Assistant messages record the configured provider
  that served the turn, the gateway-reported upstream (OpenRouter's
  top-level `provider` name), and the served model slug behind routing
  aliases like `openrouter/auto` or `openrouter/free`. On replay an
  encrypted/opaque reasoning payload is dropped when it was produced by a
  different provider instance (opaque blobs are credential/account scoped),
  or when a pinned OpenRouter provider route disagrees with the upstream
  that produced it. OpenRouter `reasoning_details` items declaring a
  `format` (`anthropic-claude-v1`, `openai-responses-v1`,
  `azure-openai-responses-v1`, `xai-responses-v1`, `google-gemini-v1`,
  `meta-responses-v1`) are additionally checked against the requested
  model's vendor — a mismatched format is dropped instead of failing the
  upstream, while `unknown` or format-less items replay unchanged. Plaintext
  thinking always survives; only the opaque payload is stripped. The room's
  model badge still shows the requested model — the served slug is kept
  purely as provenance.

## Older releases

- [0.9.x](docs/changelogs/0.9.md)
- [0.8.x](docs/changelogs/0.8.md)
- [0.7.x](docs/changelogs/0.7.md)
- [0.6.x](docs/changelogs/0.6.md)
- [0.5.x](docs/changelogs/0.5.md)
- [0.4.x](docs/changelogs/0.4.md)
- [0.1.x](docs/changelogs/0.1.md)
