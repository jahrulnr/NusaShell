# Settings

Instance settings grouped behind an eight-item rail (Overview, Models, Agent, Context & memory, Media, Web, Interface, Remote access). Fields render into the panels with ids of the form settings-field-<key>; a dirty state opens a review bar with per-change revert before saving.

**How to open:** Click the Settings item in the left rail, or open #settings.

## Header and save bar

Refresh and sync state sit in the header; edits mark fields dirty and open the save bar with a change count, a review dialog with per-change revert, discard, and save. The Fix action jumps to the first invalid field.

- **Settings sync state** (`#settings-sync-state`):

- **Refresh settings** (`#settings-refresh`):

- **Save bar** (`#settings-savebar`):

- **Dirty change count** (`#settings-dirty-count`):
  - Type: status

- **Review changes** (`#settings-review-open`):

- **Discard changes** (`#settings-discard`):

- **Save settings** (`#settings-save`):
  - Type: button
  - Action: Persists runtime settings.

- **Change review** (`#settings-review`):

- **Save error** (`settings-save-error`):

- **Fix fields** (`settings-fix`):

## Section rail

The rail jumps to a settings section; search filters fields across all sections and shows an empty state when nothing matches. The disk banner warns about low space when reported.

- **Settings rail** (`#settings-rail`):
  - Type: navigation
  - Notes: Vertical (horizontal on mobile) section navigator.

- **Overview section** (`#settings-jump-overview`):
  - Type: button
  - Action: Scrolls to the overview settings panel.

- **Models section** (`#settings-jump-models`):
  - Type: button
  - Action: Scrolls to the models settings panel.

- **Agent section** (`#settings-jump-agent`):
  - Type: button
  - Action: Scrolls to the agent settings panel.

- **Context section** (`#settings-jump-context`):
  - Type: button
  - Action: Scrolls to the context settings panel.

- **Media section** (`#settings-jump-media`):
  - Type: button
  - Action: Scrolls to the media settings panel.

- **Web section** (`#settings-jump-web`):
  - Type: button
  - Action: Scrolls to the web settings panel.

- **Interface section** (`#settings-jump-interface`):
  - Type: button
  - Action: Scrolls to the interface settings panel.

- **Remote section** (`#settings-jump-remote`):
  - Type: button
  - Action: Scrolls to the remote settings panel.

- **Settings search** (`#settings-search`):
  - Type: search
  - Action: Filters fields across all sections.
  - Shortcut: Ctrl+K / ⌘K, or / when not typing

- **Search empty state** (`settings-search-empty`):

- **Disk space banner** (`#settings-disk-banner`):

- **Settings panels container** (`#settings-panels`):

## Overview

The overview panel summarizes the instance with a tile per capability (state on/install/off), delegate/tool-round/compaction defaults, and a responsive status panel for version, data directory with Copy, and remote-access state. Clicking a tile scrolls to its card.

- **Overview tiles** (`settings-overview-tiles`):

- **Overview summary** (`settings-overview-summary`):
  - Notes: Shows delegate/tool-round/compaction defaults and a responsive version, data-directory copy, and remote-access status panel.

- **Data directory** (`settings-data-dir`):
  - Type: text

- **Copy data dir** (`settings-copy-data-dir`):

## Settings fields

Every settings field renders with id settings-field-<key> inside its section panel; these ids are generated per field at render time. Model fields use styled selects; secrets never echo stored values.

- **Delegate model** (`#settings-field-delegate_model`):
  - Type: select
  - Notes: Model used for delegated subagent turns. (id generated at render time)

- **Review model** (`#settings-field-review_model`):
  - Type: select
  - Notes: Model used by the review/learning passes. (id generated at render time)

- **Compaction model** (`#settings-field-compaction_model`):
  - Type: select
  - Notes: Model used for context compaction summaries. (id generated at render time)

- **Embedding model** (`#settings-field-embedding_model`):
  - Type: select
  - Notes: Model used for memory embeddings. (id generated at render time)

- **User instructions** (`#settings-field-user_prompt`):
  - Type: textarea
  - Notes: Custom instructions appended to the system prompt. (id generated at render time)

- **Max tool rounds** (`#settings-field-max_tool_rounds`):
  - Type: number
  - Notes: Tool-call rounds allowed per turn. (id generated at render time)

- **Repeated tool limit** (`#settings-field-repeated_tool_limit`):
  - Type: number
  - Notes: Identical tool calls allowed before the turn stops. (id generated at render time)

- **Max parallel tools** (`#settings-field-max_parallel_tools`):
  - Type: number
  - Notes: Tool calls allowed to run concurrently. (id generated at render time)

- **Auto continues** (`#settings-field-max_auto_continues`):
  - Type: number
  - Notes: Automatic continuations allowed per turn. (id generated at render time)

- **Slow down** (`#settings-field-slow_down`):
  - Type: number
  - Notes: Seconds to wait before every round of every conversation; 0 disables. (id generated at render time)

- **Max output tokens** (`#settings-field-max_output_tokens`):
  - Type: number
  - Notes: Default completion token limit when the model does not advertise one. (id generated at render time)

- **Temperature** (`#settings-field-temperature`):
  - Type: number
  - Notes: Sampling temperature sent to the provider. (id generated at render time)

- **Top P** (`#settings-field-top_p`):
  - Type: number
  - Notes: Nucleus sampling cutoff sent to the provider. (id generated at render time)

- **Top K** (`#settings-field-top_k`):
  - Type: number
  - Notes: Highest-probability token cutoff for Anthropic models. (id generated at render time)

- **Frequency penalty** (`#settings-field-frequency_penalty`):
  - Type: number
  - Notes: Penalty for tokens the model has already used. (id generated at render time)

- **Presence penalty** (`#settings-field-presence_penalty`):
  - Type: number
  - Notes: Penalty for tokens that have appeared at least once. (id generated at render time)

- **Plugin contract mode** (`#settings-field-plugin_contract_mode`):
  - Type: select
  - Notes: How strictly plugin tool contracts are enforced. (id generated at render time)

- **Compaction workflow** (`#settings-field-compaction_workflow`):
  - Type: select
  - Notes: Which compaction pipeline summarizes context. (id generated at render time)

- **Max input tokens** (`#settings-field-max_input_tokens`):
  - Type: number
  - Notes: Context budget that triggers compaction. (id generated at render time)

- **Compaction threshold** (`#settings-field-compaction_threshold`):
  - Type: number
  - Notes: Fraction of the context window that triggers compaction. (id generated at render time)

- **Summary max tokens** (`#settings-field-compaction_summary_max_tokens`):
  - Type: number
  - Notes: Cap on compaction summary output length. (id generated at render time)

- **Summary min chars** (`#settings-field-compaction_summary_min_chars`):
  - Type: number
  - Notes: Minimum summary length accepted before retry. (id generated at render time)

- **Prompt caching** (`#settings-field-prompt_caching`):
  - Type: toggle
  - Notes: Enables provider prompt caching where supported. (id generated at render time)

- **Learner nudge interval** (`#settings-field-learner_nudge_interval`):
  - Type: number
  - Notes: Rounds between learning nudges. (id generated at render time)

- **Project memory base** (`#settings-field-project_memory_base`):
  - Type: select
  - Notes: How much project memory seeds new rooms. (id generated at render time)

- **Vision fallback model** (`#settings-field-vision_model`):
  - Type: select
  - Notes: Model used when the active model lacks vision. (id generated at render time)

- **Audio fallback model** (`#settings-field-audio_model`):
  - Type: select
  - Notes: Model used for audio inputs. (id generated at render time)

- **Video fallback model** (`#settings-field-video_model`):
  - Type: select
  - Notes: Model used for video inputs. (id generated at render time)

- **Offline STT model** (`#settings-field-stt_offline_model`):
  - Type: select
  - Notes: Whisper model for offline speech-to-text. (id generated at render time)

- **Offline STT language** (`#settings-field-stt_offline_language`):
  - Type: select
  - Notes: Language hint for offline transcription. (id generated at render time)

- **Image generation model** (`#settings-field-image_model`):
  - Type: select
  - Notes: Model used for image generation. (id generated at render time)

- **Video generation model** (`#settings-field-video_gen_model`):
  - Type: select
  - Notes: Model used for video generation. (id generated at render time)

- **Speech model** (`#settings-field-tts_model`):
  - Type: select
  - Notes: Model used for text-to-speech. (id generated at render time)

- **Web search strategy** (`#settings-field-web_search_strategy`):
  - Type: select
  - Notes: Provider used for web search. (id generated at render time)

- **Brave API key** (`#settings-field-web_search_brave_api_key`):
  - Type: secret
  - Notes: Stored Brave search key; never echoed. (id generated at render time)

- **Serper API key** (`#settings-field-web_search_serper_api_key`):
  - Type: secret
  - Notes: Stored Serper key; never echoed. (id generated at render time)

- **Tavily API key** (`#settings-field-web_search_tavily_api_key`):
  - Type: secret
  - Notes: Stored Tavily key; never echoed. (id generated at render time)

- **Web answer provider** (`#settings-field-web_answer_provider`):
  - Type: select
  - Notes: Provider used for web answers. (id generated at render time)

- **Web answer API key** (`#settings-field-web_answer_api_key`):
  - Type: secret
  - Notes: Stored key for the web answer provider. (id generated at render time)

- **Web answer model** (`#settings-field-web_answer_model`):
  - Type: select
  - Notes: Model used for web answers. (id generated at render time)

- **Sound notifications** (`#settings-field-sound_notifications`):
  - Type: toggle
  - Notes: Plays a sound when an agent turn completes. (id generated at render time)

- **Pet auto start** (`#settings-field-pets_auto_start`):
  - Type: toggle
  - Notes: Launches the desktop pet automatically on start. (id generated at render time)

- **Allow remote access** (`#settings-field-remote_access_enabled`):
  - Type: toggle
  - Notes: Enables remote pairing; mirrored by the remote panel toggle. (id generated at render time)

- **Remote access addresses** (`#settings-field-remote_access_addresses`):
  - Type: textarea
  - Notes: Addresses remote devices can use to reach this instance. (id generated at render time)

## Special cards

The media section adds install/status cards for offline STT and TTS (model/voice pickers, install + stop actions, progress, per-OS install guides) and the desktop pet card; Interface adds the font segment picker with a live specimen and the compact-sidebar toggle; Models adds the preferred model select.

- **Preferred model** (`settings-preferred-model`):
  - Type: select
  - Action: Default model applied to new conversations.

- **Offline STT card** (`settings-stt-card`):
  - Type: container
  - Notes: Install/status card for offline speech-to-text: status copy, checklist, model select, Install/Stop actions, progress, and per-OS install guides.

- **STT status** (`settings-stt-status`):
  - Type: status

- **STT model select** (`settings-stt-install-model`):
  - Type: select

- **STT install progress** (`settings-stt-progress`):
  - Type: status

- **Offline TTS card** (`settings-tts-card`):
  - Type: container
  - Notes: Install/status card for offline text-to-speech: status copy, voice select, Install action, progress.

- **TTS status** (`settings-tts-status`):
  - Type: status

- **TTS voice select** (`settings-tts-install-voice`):
  - Type: select

- **TTS install progress** (`#settings-tts-progress`):
  - Type: status

- **Pet status** (`settings-pet-status`):
  - Type: status

- **Pet install/launch** (`settings-pet-action`):
  - Type: button
  - Action: Installs or launches the desktop pet.

- **Font specimen** (`settings-font-specimen`):
  - Type: text
  - Notes: Shows letters, numbers, symbols, and emoji using the active interface font stack.

- **Interface font options** (`[data-font-id]`):
  - Type: button group
  - Action: Chooses the UI font (stored locally, not synced).

- **Use icon-only sidebar** (`settings-sidebar-compact`):
  - Type: checkbox
  - Notes: Browser-only preference.

- **STT install guide** (`[data-stt-guide]`):
  - Type: tab group
  - Action: Per-OS install command panels (linux/windows/macos) shown when the platform is unsupported.

- **STT requirements checklist** (`settings-checklist`):
  - Type: list

- **Install progress** (`#settings-progress`):
  - Type: status
  - Action: Progress bar, phase, and byte counter for install flows.
  - Notes: Rendered as settings-<kind>-progress inside STT/TTS cards.

## Remote access

The remote panel toggles remote access and drives device pairing: generate a link, show the QR, list paired sessions, revoke all, and approve or reject pending devices through the approval overlay.

- **Remote access section** (`#settings-remote-group`):
  - Section: Settings — Remote access
  - Type: container
  - Notes: Whole section; hidden together with its nav jump link on a paired remote client because pairing management is host-only.

- **Remote access** (`#settings-group-remote`):
  - Type: group
  - Notes: Groups device-pairing controls.

- **Allow remote access** (`#settings-remote-access-enabled`):
  - Section: Settings — Remote access
  - Type: checkbox
  - Action: Enables or disables non-loopback access; saving the toggle restarts the backend automatically.

- **Device pairing** (`#pairing-panel`):
  - Section: Settings — Remote access
  - Type: container
  - Notes: Host-side pairing management card.

- **Generate pairing link** (`#pairing-create-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Creates a one-time pairing challenge and opens the pairing dialog, which renders the QR, the pairing link, and the manual code.

- **Pairing summary** (`#pairing-panel-status`):
  - Section: Settings — Remote access
  - Type: status
  - Notes: Persistent state summary of the active challenge (waiting, approved, rejected, expired, paired). It is the live region while the dialog is closed and goes quiet (aria-live=off) while the dialog's own status announces.

- **Show pairing QR** (`#pairing-show-qr-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Reopens the pairing dialog for the active challenge. Hidden until a challenge exists and once it is no longer pending, so a dead link is never offered again.

- **Approve pairing** (`#pairing-approve-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Approves the pending pairing challenge so the remote device can exchange for a session.

- **Reject pairing** (`#pairing-reject-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Rejects the pending pairing challenge.

- **Device label** (`#pairing-device-label`):
  - Section: Settings — Remote access
  - Type: input
  - Notes: Optional label attached to the approved challenge so the issued session row names the device.

- **Pairing status (dialog)** (`#pairing-host-status`):
  - Section: Settings — Remote access
  - Type: status
  - Notes: Live region announcing the challenge state inside the pairing dialog (waiting, approved, rejected, expired, paired). While the dialog is open it is the only live pairing status region.

- **Pairing QR** (`#pairing-qr-display`):
  - Section: Settings — Remote access
  - Type: container
  - Notes: QR encoding the pairing URL, rendered client-side. It lives inside the pairing dialog because the dialog's overlay covers the Settings panel; the panel only offers Show pairing QR to bring it back.

- **Pairing QR image** (`#pairing-qr-img`):
  - Section: Settings — Remote access
  - Type: image
  - Notes: Client-side rendered data-URL QR; its alt text names the encoded pairing URL. Reopening the dialog reuses the identical QR for the same challenge.

- **QR status** (`#pairing-qr-status`):
  - Section: Settings — Remote access
  - Type: text
  - Notes: Tells the host to scan or copy the link/code, flags a configured address that may be unreachable from the remote device, or reports that QR rendering failed and the manual link/code must be used. Static per challenge, so it is not a second live region next to the challenge status.

- **Additional pairing QR links** (`#pairing-qr-alternatives`):
  - Section: Settings — Remote access
  - Type: container
  - Notes: Renders one additional QR/link card inside the pairing dialog for each configured address after the first address.

- **Remote address** (`#pairing-remote-address`):
  - Section: Settings — Remote access
  - Type: textarea
  - Notes: One absolute HTTP(S) address per line. Persisted in settings and encoded into one QR/link per address; does not change where the core listens.

- **Remote address error** (`#pairing-address-error`):
  - Section: Settings — Remote access
  - Type: alert
  - Notes: Inline address feedback: a malformed URL is a blocking error, while a link that cannot be reachable (loopback address, or a non-loopback link while the core listens on loopback only, port/host mismatch vs listen_addr) is a non-blocking warning.

- **Pairing link** (`#pairing-link-display`):
  - Section: Settings — Remote access
  - Type: text
  - Notes: The one-time pairing URL for the first configured address, shown inside the pairing dialog next to Copy link.

- **Copy pairing link** (`#pairing-copy-link-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Copies the pairing URL to the clipboard.

- **Pairing code** (`#pairing-code-display`):
  - Section: Settings — Remote access
  - Type: text
  - Notes: The one-time manual code for the challenge, shown inside the pairing dialog next to Copy code.

- **Copy pairing code** (`#pairing-copy-code-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Copies the one-time code to the clipboard.

- **Paired devices** (`#pairing-sessions-list`):
  - Section: Settings — Remote access
  - Type: list
  - Notes: Active pairing sessions with per-device revoke; revoked sessions are omitted.

- **No paired devices** (`#pairing-sessions-empty`):
  - Section: Settings — Remote access
  - Type: text

- **Revoke all sessions** (`#pairing-revoke-all-btn`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Revokes every paired device session and removes them from the list.

- **Pairing dialog** (`#pairing-approval-overlay`):
  - Section: Settings — Remote access
  - Type: dialog
  - Notes: Body-level host dialog that opens when a challenge is created. It owns the scan surface: QR, pairing link, manual code, the extra-address QR cards, the challenge status, the device label, and the Approve/Reject decision. Closing it leaves the challenge pending and returns the QR to the panel summary's Show pairing QR control.

- **Pairing dialog message** (`#pairing-approval-message`):
  - Section: Settings — Remote access
  - Type: text
  - Notes: Tells the host to scan the QR with the remote device (or open the link on it) and approve the request.

- **Close pairing dialog** (`#pairing-approval-close`):
  - Section: Settings — Remote access
  - Type: button
  - Action: Closes the dialog while the host continues polling the pending challenge; the panel summary then offers Show pairing QR.
