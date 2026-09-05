# Settings

Control how this local NusaShell instance runs agent turns, manages context, stores memory, picks models, and presents the workspace. Cards are grouped by boundary: Agent, Context, Memory & search, Media understanding, Media generation, Web, and Workspace. Browser-only preferences stay in local storage; the rest save with the instance.

**How to open:** Click Settings in the sidebar, or press the Settings shortcut.

## Header

View title and a Save settings button with a live status indicator.

- **Save settings** (`#settings-save-btn`):
  - Section: Settings
  - Type: button
  - Action: Persists runtime settings.

- **Save status** (`#settings-save-status`):
  - Section: Settings
  - Type: status

## Card groups

Settings cards are clustered under labeled groups so unrelated controls are not mixed. Agent: runtime, instructions, plugins. Context: compaction and prompt caching. Memory & search: learning job model, project memory directory, embeddings. Media understanding: vision/audio/video fallback and offline STT. Media generation: image, video, speech. Web: Web Search (provider strategy + per-provider API keys) and Web Answer. Workspace: appearance, connection, system.

- **Agent group** (`#settings-group-agent`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for runtime, instructions, and plugin contracts.

- **Context group** (`#settings-group-context`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for compaction and prompt caching.

- **Memory & search group** (`#settings-group-memory`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for learning jobs, project memory, and embeddings.

- **Media understanding group** (`#settings-group-understand`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for vision/audio/video fallback and offline STT.

- **Media generation group** (`#settings-group-generate`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for image, video, and speech generation.

- **Web group** (`#settings-group-web`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for Web Answer.

- **Workspace group** (`#settings-group-workspace`):
  - Section: Settings
  - Type: heading
  - Notes: Card group for appearance, connection, and system diagnostics.

## Agent runtime

Default model for new conversations, the model used by the internal delegate agent, tool-round guards (max rounds, repeated-call limit, max parallel tools), max auto-continues after a successful turn with open todos, an optional per-round slow-down pacing delay, max output tokens, and optional sampling parameters. Existing conversations keep their selected model. The delegate model defaults to the active conversation model.

- **Agent runtime title** (`#settings-runtime-title`):
  - Section: Settings
  - Type: text

- **Default model** (`#settings-preferred-model`):
  - Section: Settings
  - Type: select
  - Notes: Used by new conversations.

- **Internal delegate model** (`#settings-delegate-model`):
  - Section: Settings
  - Type: select
  - Notes: Model for the internal headless delegate agent; empty inherits the active conversation model.

- **Maximum tool rounds** (`#settings-max-tool-rounds`):
  - Section: Settings
  - Type: number
  - Notes: 1–10000, default 8.

- **Repeated tool call limit** (`#settings-repeated-tool-limit`):
  - Section: Settings
  - Type: number
  - Notes: Breaks identical tool loops; default 3, 0 disables.

- **Max parallel tools** (`#settings-max-parallel-tools`):
  - Section: Settings
  - Type: number
  - Notes: 1–64, default 6. Bounds concurrent tool calls per assistant round; calls above the limit wait in a queue and are not dropped.

- **Max auto-continues** (`#settings-auto-continues`):
  - Section: Settings
  - Type: number
  - Notes: Auto-starts the next turn while todos remain; default 10, 0 unlimited.

- **Slow Down per round (seconds)** (`#settings-slow-down`):
  - Section: Settings
  - Type: number
  - Notes: Artificial per-round delay before every agent round in every conversation; default 0 (off), range 0-60. Saved values apply instantly to running conversations - an in-flight delay shrinks or cancels without stopping the turn.

- **Max output tokens** (`#settings-max-output-tokens`):
  - Section: Settings
  - Type: number
  - Notes: Default 65536.

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

## Instructions

Custom instructions injected into every agent turn inside user_instructions tags. This is agent identity, not compaction or search. Editing it busts the prompt cache until a new shard stabilizes.

- **Instructions title** (`#settings-instructions-title`):
  - Section: Settings
  - Type: text

- **User prompt** (`#settings-user-prompt`):
  - Section: Settings
  - Type: textarea
  - Notes: Custom instructions injected into every agent turn.

## Plugins

How agents treat MCP plugin usage contracts. Install and enable plugins on the Plugins view. Empty = factory default (hint, advisory); off never notifies; hint attaches an advisory note until the contract is read; require blocks MCP calls until the contract is read.

- **Plugins title** (`#settings-plugins-title`):
  - Section: Settings
  - Type: text

- **Plugin usage contracts** (`#settings-plugin-contract-mode`):
  - Section: Settings
  - Type: select
  - Notes: How agents are nudged toward a plugin's usage contract (read via contract_read). Empty = factory default (hint, advisory); off never notifies; hint attaches an advisory note until the contract is read; require blocks MCP calls until the contract is read.

## Context compaction

Toggle compaction, set max input tokens (fallback context window, default 200000), compaction threshold and summary quality knobs, pick an optional compaction model, and enable provider prompt caching. Cache TTL is chosen per provider on the Providers detail pane. Completion limits and sampling are under Agent runtime.

- **Context compaction title** (`#settings-context-title`):
  - Section: Settings
  - Type: text

- **Compact long conversations** (`#settings-compaction-enabled`):
  - Section: Settings
  - Type: checkbox

- **Max input tokens** (`#settings-max-input-tokens`):
  - Section: Settings
  - Type: number
  - Notes: Fallback context window, default 200000.

- **Compaction threshold** (`#settings-compaction-threshold`):
  - Section: Settings
  - Type: number
  - Notes: Token count that triggers compaction. 0 = auto (80% of context window).

- **Compaction summary max tokens** (`#settings-compaction-summary-max-tokens`):
  - Section: Settings
  - Type: number
  - Notes: Max output tokens for the compaction summarization call. 0 = default 64000.

- **Compaction summary min chars** (`#settings-compaction-summary-min-chars`):
  - Section: Settings
  - Type: number
  - Notes: Minimum summary length for the compaction quality guard. 0 = default 200.

- **Compaction model** (`#settings-compaction-model`):
  - Section: Settings
  - Type: select
  - Notes: Route compaction summarization to a cheaper or faster model. Default uses the conversation's active model.

- **Prompt caching** (`#settings-prompt-caching`):
  - Section: Settings
  - Type: checkbox
  - Notes: Master switch for provider-side prompt caching. The cache duration is selected per provider (Cache TTL chips on the Providers detail pane).

## Learning

Background jobs consolidate recorded experience into memory records and skills. The review model routes those jobs to a cheaper or faster model (default uses the conversation's active model).

- **Learning title** (`#settings-learning-title`):
  - Section: Settings
  - Type: text

- **Review model** (`#settings-review-model`):
  - Section: Settings
  - Type: select
  - Notes: Routes background learning jobs to a dedicated model; empty uses the conversation's active model.

## Project memory

Per-workspace durable notes (index.md, guardrails.md, and the rest of the skill-compatible layout). Separate from user memory on the Learning view and from the embedding model. Empty uses {dataDir}/memory_project/{key}/. An absolute path such as ~/.memory shares the layout with other agents.

- **Project memory title** (`#settings-project-memory-title`):
  - Section: Settings
  - Type: text

- **Project memory directory** (`#settings-project-memory-base`):
  - Section: Settings
  - Type: text
  - Notes: Absolute directory for {key}/{kind}.md project memory. Empty uses {dataDir}/memory_project. ~/.memory shares the skill-compatible layout.

## Embeddings

Embedding model used by skill and memory search. Models tagged as embedding-capable appear here after import.

- **Embeddings title** (`#settings-embedding-title`):
  - Section: Settings
  - Type: text

- **Embedding model** (`#settings-embedding-model`):
  - Section: Settings
  - Type: select

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

## Offline speech-to-text

Lets read_media transcribe audio locally with whisper.cpp (whisper-cli) — no API key, no network. The engine binary is downloaded once during install; models land under models/stt/ in the data directory. The install button opens a requirements dialog: a checklist (platform support, whisper.cpp engine, GGML model, free disk), a per-OS guide (Linux/Windows auto-install from official releases, macOS via brew install whisper-cpp), a model picker over the multilingual catalog (tiny/base/small-q5_1/small default/large-v3-turbo variants; Indonesian and English supported; .en variants excluded), and live download progress with speed. After install the model appears in the Offline STT model select and read_media uses it automatically.

- **Offline speech-to-text title** (`#settings-stt-title`):
  - Section: Settings
  - Type: text

- **Offline STT model** (`#settings-stt-model`):
  - Section: Settings
  - Type: select

- **Audio language** (`#settings-stt-language`):
  - Section: Settings
  - Type: select
  - Notes: Offline STT language: auto-detect, Bahasa Indonesia, or English.

- **Install offline speech-to-text** (`#settings-stt-install-btn`):
  - Section: Settings
  - Type: button
  - Action: Opens the whisper.cpp install dialog.

- **STT install status** (`#settings-stt-install-status`):
  - Section: Settings
  - Type: status

- **STT install dialog** (`#stt-requirements-overlay`):
  - Section: Settings
  - Type: dialog

- **STT install title** (`#stt-requirements-title`):
  - Section: Settings
  - Type: heading

- **Close STT install** (`#stt-requirements-close`):
  - Section: Settings
  - Type: button

- **STT install error** (`#stt-requirements-error`):
  - Section: Settings
  - Type: status

- **STT requirements checklist** (`#stt-req-checklist`):
  - Section: Settings
  - Type: list

- **STT install guide** (`#stt-guide`):
  - Section: Settings
  - Type: panel

- **STT Linux guide tab** (`#stt-guide-tab-linux`):
  - Section: Settings
  - Type: tab

- **STT Windows guide tab** (`#stt-guide-tab-windows`):
  - Section: Settings
  - Type: tab

- **STT macOS guide tab** (`#stt-guide-tab-macos`):
  - Section: Settings
  - Type: tab

- **STT model picker** (`#stt-install-model`):
  - Section: Settings
  - Type: select

- **STT install progress** (`#stt-install-progress`):
  - Section: Settings
  - Type: status

- **STT install phase** (`#stt-install-phase`):
  - Section: Settings
  - Type: text

- **STT install bar track** (`#stt-install-bar-track`):
  - Section: Settings
  - Type: progress

- **STT install bar** (`#stt-install-bar`):
  - Section: Settings
  - Type: progress

- **STT install bytes** (`#stt-install-bytes`):
  - Section: Settings
  - Type: text

- **Stop STT install** (`#stt-install-stop`):
  - Section: Settings
  - Type: button

- **Cancel STT install** (`#stt-requirements-cancel`):
  - Section: Settings
  - Type: button

- **Confirm STT install** (`#stt-install-confirm`):
  - Section: Settings
  - Type: button
  - Action: Starts the whisper.cpp download.

## Image generation

Pick the model used by the generate_image tool. This is separate from the chat model — any chat model can orchestrate, and the image backend is resolved here. OpenAI Images, OpenRouter Image API, and a signed-in Codex ChatGPT plan are supported. Import models on an OpenAI or OpenRouter provider. Leave disabled until you want the agent to be able to generate images.

- **Image generation title** (`#settings-image-title`):
  - Section: Settings
  - Type: text

- **Image generation model** (`#settings-image-model`):
  - Section: Settings
  - Type: select

## Video generation

Pick the model used by the generate_video tool. This is separate from the chat model and the video fallback — any chat model can orchestrate, and the video backend is resolved here. OpenRouter's async /videos API is supported. Leave disabled until you want the agent to be able to generate videos.

- **Video generation title** (`#settings-video-gen-title`):
  - Section: Settings
  - Type: text

- **Video generation model** (`#settings-video-gen-model`):
  - Section: Settings
  - Type: select
  - Notes: Auxiliary model for generate_video. Empty disables the tool.

## Speech generation

Pick the model used by the generate_speech tool (text-to-speech). Online models use an OpenAI-compatible /audio/speech endpoint. The offline piper engine can be installed with one click: the install button opens a dialog with a voice picker (Bahasa Indonesia news_tts medium, English US lessac high), downloads the piper binary plus the selected voice model into the data directory with live progress, and the installed voice then appears as an option in the model picker (provider “piper”) so it can be selected directly — the automatic fallback works as soon as install finishes.

- **Speech generation title** (`#settings-tts-title`):
  - Section: Settings
  - Type: text

- **Speech generation model** (`#settings-tts-model`):
  - Section: Settings
  - Type: select

- **Install offline text-to-speech** (`#settings-tts-install-btn`):
  - Section: Settings
  - Type: button
  - Action: Opens the piper install dialog.

- **TTS install status** (`#settings-tts-install-status`):
  - Section: Settings
  - Type: status

- **TTS install dialog** (`#tts-install-overlay`):
  - Section: Settings
  - Type: dialog

- **TTS install title** (`#tts-install-title`):
  - Section: Settings
  - Type: heading

- **Close TTS install** (`#tts-install-close`):
  - Section: Settings
  - Type: button
  - Action: Closes the TTS installer.

- **TTS install error** (`#tts-install-error`):
  - Section: Settings
  - Type: status

- **TTS voice picker** (`#tts-install-voice`):
  - Section: Settings
  - Type: select

- **TTS install progress** (`#tts-install-progress`):
  - Section: Settings
  - Type: status

- **TTS install phase** (`#tts-install-phase`):
  - Section: Settings
  - Type: text

- **TTS install bar track** (`#tts-install-bar-track`):
  - Section: Settings
  - Type: progress

- **TTS install bar** (`#tts-install-bar`):
  - Section: Settings
  - Type: progress

- **TTS install bytes** (`#tts-install-bytes`):
  - Section: Settings
  - Type: text

- **Cancel TTS install** (`#tts-install-cancel`):
  - Section: Settings
  - Type: button

- **Confirm TTS install** (`#tts-install-confirm`):
  - Section: Settings
  - Type: button
  - Action: Starts the piper download.

## Web Search

Provider strategy for the web_search tool: auto merges all searchwire sources (default), round robin rotates one API-keyed provider (Brave, Serper, Tavily) per query, random picks one at random, and a bare source name pins every query to that source. Per-provider API keys are write-only, stored in the credential store; each input falls back to its standard environment variable (BRAVE_SEARCH_API_KEY, SERPER_API_KEY, TAVILY_API_KEY) when left blank.

- **Web Search title** (`#settings-web-search-title`):
  - Section: Settings
  - Type: heading
  - Notes: Web Search card heading.

- **Provider strategy** (`#settings-web-search-strategy`):
  - Section: Settings
  - Type: select
  - Notes: web_search routing: auto, round_robin, random, or a bare source name (brave, serper, tavily, startpage, wikipedia, github).

- **Brave API key** (`#settings-web-search-brave-api-key`):
  - Section: Settings
  - Type: password
  - Notes: Stored in the credential store (web_search_brave). Empty falls back to BRAVE_SEARCH_API_KEY; without a key Brave uses public HTML results.

- **Serper API key** (`#settings-web-search-serper-api-key`):
  - Section: Settings
  - Type: password
  - Notes: Stored in the credential store (web_search_serper). Empty falls back to SERPER_API_KEY; required to register the Serper source.

- **Tavily API key** (`#settings-web-search-tavily-api-key`):
  - Section: Settings
  - Type: password
  - Notes: Stored in the credential store (web_search_tavily). Empty falls back to TAVILY_API_KEY; required to register the Tavily source.

## Web Answer

Configure a web-grounded answer provider for the web_answer tool. This is separate from chat providers — pick a supported vendor and enter its API key. The key is stored in the credential store, not in settings JSON.

- **Web Answer title** (`#settings-web-answer-title`):
  - Section: Settings
  - Type: text

- **Answer provider** (`#settings-web-answer-provider`):
  - Section: Settings
  - Type: select
  - Notes: Vendor for web_answer. Empty disables the tool.

- **Web Answer API key** (`#settings-web-answer-api-key`):
  - Section: Settings
  - Type: password
  - Notes: Stored in the credential store. Leave blank while saving to keep the existing key.

- **Web Answer model** (`#settings-web-answer-model`):
  - Section: Settings
  - Type: text
  - Notes: Optional model or preset; blank uses the vendor default.

## Appearance

Choose a bundled interface font for this browser; the font applies immediately and is stored locally. Symbol and emoji coverage uses local fallbacks. Sidebar layout is also stored in this browser. Sound notifications save with the NusaShell instance and play when an agent turn completes or fails.

- **Appearance title** (`#settings-appearance-title`):
  - Section: Settings
  - Type: text

- **Interface font** (`#settings-font-family`):
  - Section: Settings
  - Type: select
  - Notes: Browser-only preference. Selects one of the bundled UI families; Noto Sans Symbols 2 and Noto Color Emoji remain fallback coverage faces.

- **Font preview** (`#settings-font-preview`):
  - Section: Settings
  - Type: text
  - Notes: Shows letters, numbers, symbols, and emoji using the active interface font stack.

- **Use icon-only sidebar** (`#settings-sidebar-compact`):
  - Section: Settings
  - Type: checkbox
  - Notes: Browser-only preference.

- **Sound notifications** (`#settings-sound-notifications`):
  - Section: Settings
  - Type: checkbox
  - Notes: Play a sound when an agent turn completes or fails. Default on.

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

Local diagnostics: version and data directory. This browser version does not manage desktop tray or login startup.

- **System title** (`#settings-system-title`):
  - Section: Settings
  - Type: text

- **Version** (`#settings-version`):
  - Section: Settings
  - Type: text

- **Data directory** (`#settings-data-dir`):
  - Section: Settings
  - Type: text
