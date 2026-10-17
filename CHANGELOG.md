# Changelog

All notable changes to the Go core will be documented in this file. The Electron wrapper and pets keep their own changelogs in [`apps/electron/CHANGELOG.md`](apps/electron/CHANGELOG.md) and [`apps/pets/CHANGELOG.md`](apps/pets/CHANGELOG.md).

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Conversations can be compacted on demand from the room menu or the Context dock. The request uses the existing compaction pass, refuses while a turn is running, and reports when a model compacts on the provider instead of through a separate summary call. A room deleted during that pass is not written back, a rename made while the summary runs is kept, and a panic still emits `agent.compaction.failed` so the composer does not stay stuck.

## [0.10.1] - 2026-10-16

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
