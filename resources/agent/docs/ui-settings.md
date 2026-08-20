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

Default model for new conversations, the maximum tool rounds per turn (default 8), and the max parallel tools per round (default 6, range 1–64). Existing conversations keep their selected model.

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

## Embeddings

Embedding model used by skill and memory search. Models tagged as embedding-capable appear here after import. Ollama models (e.g. nomic-embed-text) work locally without an API key. The learning review threshold controls how many turns accumulate before extracting learnings (default 10, 0 disables turn-based review).

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

## Vision fallback

When the active chat model cannot see images, NusaShell can describe attached images using a separate vision-capable model so the conversation continues without errors. Pick any model that supports image input. Leave disabled if you always use vision-capable chat models.

- **Vision fallback title** (`#settings-vision-title`):
  - Section: Settings
  - Type: text

- **Vision fallback model** (`#settings-vision-model`):
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

- **Transports** (`#settings-transports`):
  - Section: Settings
  - Type: text
