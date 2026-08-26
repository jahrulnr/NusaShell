# Settings

Control how this local NusaShell instance runs agent turns, manages context, picks embedding models, and presents the workspace. Browser-only preferences stay in local storage.

**How to open:** Click the settings icon in the title bar, or press the Settings shortcut.

## Header

View title and a Save settings button with a live status indicator.

- **Save settings** (`#settings-save-btn`):
  - Section: Settings
  - Type: button
  - Action: Persists runtime settings.

- **Save status** (`#settings-save-status`):
  - Section: Settings
  - Type: status

## Agent runtime

Default model for new conversations, the maximum tool rounds per turn (default 8), the max parallel tools per round (default 6, range 1–64), and the plugin usage-contract enforcement mode. Existing conversations keep their selected model.

- **Agent runtime title** (`#settings-runtime-title`):
  - Section: Settings
  - Type: text

- **Default model** (`#settings-preferred-model`):
  - Section: Settings
  - Type: select
  - Notes: Used by new conversations.

- **Maximum tool rounds** (`#settings-max-tool-rounds`):
  - Section: Settings
  - Type: number
  - Notes: 1–10000, default 8.

- **Max parallel tools** (`#settings-max-parallel-tools`):
  - Section: Settings
  - Type: number
  - Notes: 1–64, default 6. Bounds concurrent tool calls per assistant round.

- **Plugin usage contracts** (`#settings-plugin-contract-mode`):
  - Section: Settings
  - Type: select
  - Notes: How agents are nudged toward a plugin's usage contract (read via contract_read). Empty = factory default (hint, advisory); off never notifies; hint attaches an advisory note until the contract is read; require blocks MCP calls until the contract is read.

## Embeddings

Embedding model used by skill and memory search. Models tagged as embedding-capable appear here after import. The learning review threshold controls how many turns accumulate before extracting learnings (default 10, 0 disables turn-based review). The review model routes background autolearn reviews to a cheaper or faster model (default uses the conversation's active model).

- **Embeddings title** (`#settings-embedding-title`):
  - Section: Settings
  - Type: text

- **Embedding model** (`#settings-embedding-model`):
  - Section: Settings
  - Type: select

- **Learning review threshold** (`#settings-learning-threshold`):
  - Section: Settings
  - Type: number
  - Notes: Turns before review; 0 disables turn-based review.

- **Skill review threshold** (`#settings-skill-nudge-interval`):
  - Section: Settings
  - Type: number
  - Notes: Tool calls before skill review; 0 disables tool-based review.

- **Review model** (`#settings-review-model`):
  - Section: Settings
  - Type: select
  - Notes: Routes background autolearn reviews to a dedicated model; empty uses the conversation's active model.

## Vision fallback

When the active chat model cannot see images, NusaShell can describe attached images using a separate vision-capable model so the conversation continues without errors. Pick any model that supports image input. Leave disabled if you always use vision-capable chat models.

- **Vision fallback title** (`#settings-vision-title`):
  - Section: Settings
  - Type: text

- **Vision fallback model** (`#settings-vision-model`):
  - Section: Settings
  - Type: select

## Image generation

Pick the model used by the generate_image tool. This is separate from the chat model — any chat model can orchestrate, and the image backend is resolved here. OpenAI Images and OpenRouter Image API are supported. Import models on an OpenAI or OpenRouter provider. Leave disabled until you want the agent to be able to generate images.

- **Image generation title** (`#settings-image-title`):
  - Section: Settings
  - Type: text

- **Image generation model** (`#settings-image-model`):
  - Section: Settings
  - Type: select

## Audio fallback

When the active chat model cannot hear audio, NusaShell can transcribe or describe attached audio files using a separate audio-capable model so the conversation continues without errors. Pick any model that supports audio input. Leave disabled if you always use audio-capable chat models.

- **Audio fallback title** (`#settings-audio-title`):
  - Section: Settings
  - Type: text

- **Audio fallback model** (`#settings-audio-model`):
  - Section: Settings
  - Type: select

## Speech generation

Pick the model used by the generate_speech tool (text-to-speech). Online models use an OpenAI-compatible /audio/speech endpoint. The offline piper engine can be installed with one click: the install button opens a dialog with a voice picker (Bahasa Indonesia news_tts medium, English US lessac high), downloads the piper binary plus the selected voice model into the data directory with live progress, and the installed voice then appears as an option in the model picker (provider “piper”) so it can be selected directly — the automatic fallback works as soon as install finishes, no offline-mode checkbox needed. The old manual checkbox stays for PIPER_BIN/PATH setups.

- **`#settings-tts-title`** (missing map entry)

- **`#settings-tts-model`** (missing map entry)

- **`#settings-tts-offline`** (missing map entry)

- **`#settings-tts-install-btn`** (missing map entry)

- **`#settings-tts-install-status`** (missing map entry)

- **`#tts-install-overlay`** (missing map entry)

- **`#tts-install-title`** (missing map entry)

- **`#tts-install-close`** (missing map entry)

- **`#tts-install-error`** (missing map entry)

- **`#tts-install-voice`** (missing map entry)

- **`#tts-install-progress`** (missing map entry)

- **`#tts-install-phase`** (missing map entry)

- **`#tts-install-bar-track`** (missing map entry)

- **`#tts-install-bar`** (missing map entry)

- **`#tts-install-bytes`** (missing map entry)

- **`#tts-install-cancel`** (missing map entry)

- **`#tts-install-confirm`** (missing map entry)

## Offline speech-to-text

Lets read_media transcribe audio locally with whisper.cpp (whisper-cli) — no API key, no network. The engine binary is downloaded once during install; models land under models/stt/ in the data directory. The install button opens a requirements dialog: a checklist (platform support, whisper.cpp engine, GGML model, free disk), a per-OS guide (Linux/Windows auto-install from official releases, macOS via brew install whisper-cpp), a model picker over the multilingual catalog (tiny/base/small-q5_1/small default/large-v3-turbo variants; Indonesian and English supported; .en variants excluded), and live download progress with speed. After install the model appears in the Offline STT model select and read_media uses it automatically.

- **`#settings-stt-title`** (missing map entry)

- **`#settings-stt-model`** (missing map entry)

- **Audio language** (`#settings-stt-language`):
  - Section: Settings
  - Type: select
  - Notes: Offline STT language: auto-detect, Bahasa Indonesia, or English.

- **`#settings-stt-install-btn`** (missing map entry)

- **`#settings-stt-install-status`** (missing map entry)

- **`#stt-requirements-overlay`** (missing map entry)

- **`#stt-requirements-title`** (missing map entry)

- **`#stt-requirements-close`** (missing map entry)

- **`#stt-requirements-error`** (missing map entry)

- **`#stt-req-checklist`** (missing map entry)

- **`#stt-guide`** (missing map entry)

- **`#stt-guide-tab-linux`** (missing map entry)

- **`#stt-guide-tab-windows`** (missing map entry)

- **`#stt-guide-tab-macos`** (missing map entry)

- **`#stt-install-model`** (missing map entry)

- **`#stt-install-progress`** (missing map entry)

- **`#stt-install-phase`** (missing map entry)

- **`#stt-install-bar-track`** (missing map entry)

- **`#stt-install-bar`** (missing map entry)

- **`#stt-install-bytes`** (missing map entry)

- **`#stt-install-stop`** (missing map entry)

- **`#stt-requirements-cancel`** (missing map entry)

- **`#stt-install-confirm`** (missing map entry)

## Video fallback

When the active chat model cannot see video, NusaShell can describe attached video files using a separate video-capable model so the conversation continues without errors. Pick any model that supports video input. Leave disabled if you always use video-capable chat models.

- **Video fallback title** (`#settings-video-title`):
  - Section: Settings
  - Type: text

- **Video fallback model** (`#settings-video-model`):
  - Section: Settings
  - Type: select

## Context compaction

Toggle compaction, set max input tokens (fallback context window, default 200000), max output tokens (default 65536), pick an optional compaction model (default uses the active chat model), prompt caching, and optional sampling parameters (temperature, top-p, top-k, frequency penalty, presence penalty).

- **Context compaction title** (`#settings-context-title`):
  - Section: Settings
  - Type: text

- **Compact long conversations** (`#settings-compaction-enabled`):
  - Section: Settings
  - Type: checkbox

- **Compaction model** (`#settings-compaction-model`):
  - Section: Settings
  - Type: select
  - Notes: Route compaction summarization to a cheaper or faster model. Default uses the conversation's active model.

- **Max input tokens** (`#settings-max-input-tokens`):
  - Section: Settings
  - Type: number
  - Notes: Fallback context window, default 200000.

- **Max output tokens** (`#settings-max-output-tokens`):
  - Section: Settings
  - Type: number
  - Notes: Default 65536.

- **Prompt caching** (`#settings-prompt-caching`):
  - Section: Settings
  - Type: checkbox

- **Sound notifications** (`#settings-sound-notifications`):
  - Section: Settings
  - Type: checkbox
  - Notes: Play a sound when an agent turn completes or fails. Default on.

- **Temperature** (`#settings-temperature`):
  - Section: Settings
  - Type: number

- **Top P** (`#settings-top-p`):
  - Section: Settings
  - Type: number

- **Top K** (`#settings-top-k`):
  - Section: Settings
  - Type: number
  - Notes: Anthropic only.

- **Frequency penalty** (`#settings-frequency-penalty`):
  - Section: Settings
  - Type: number
  - Notes: OpenAI only.

- **Presence penalty** (`#settings-presence-penalty`):
  - Section: Settings
  - Type: number
  - Notes: OpenAI only.

## Appearance

Browser-only preferences stored locally, not sent to the agent.

- **Appearance title** (`#settings-appearance-title`):
  - Section: Settings
  - Type: text

- **Use icon-only sidebar** (`#settings-sidebar-compact`):
  - Section: Settings
  - Type: checkbox
  - Notes: Browser-only preference.

## Connection

Backend status, automatic reconnect toggle, and a Check connection button. The Go shell uses HTTP RPC for commands and WebSocket for live events.

- **Connection title** (`#settings-connection-title`):
  - Section: Settings
  - Type: text

- **Backend status orb** (`#settings-conn-fill`):
  - Section: Settings
  - Type: indicator

- **Backend status label** (`#settings-conn-label`):
  - Section: Settings
  - Type: text

- **Reconnect automatically** (`#settings-auto-reconnect`):
  - Section: Settings
  - Type: checkbox

- **Check connection** (`#settings-check-connection-btn`):
  - Section: Settings
  - Type: button
  - Action: Probes the backend.

- **Connection status** (`#settings-connection-status`):
  - Section: Settings
  - Type: status

## System

Local diagnostics: version, data directory, and transports. This browser version does not manage desktop tray or login startup.

- **System title** (`#settings-system-title`):
  - Section: Settings
  - Type: text

- **Version** (`#settings-version`):
  - Section: Settings
  - Type: text

- **Data directory** (`#settings-data-dir`):
  - Section: Settings
  - Type: text
