# Settings

NusaShell settings control the runtime, AI provider, plugins path, and context limits.

## Environment variables

- `NUSASHELL_PORT`: HTTP/WebSocket port (default `9130`).
- `NUSASHELL_HOST`: bind address (default `0.0.0.0`).
- `NUSASHELL_PLUGINS_ROOT`: folder where plugin folders are located.
- `NUSASHELL_DB_PATH`: SQLite path for persistent plugin metadata.
- `NUSASHELL_AI_PROVIDER`: name of the AI provider.
- `NUSASHELL_AI_BASE_URL`: base URL of an OpenAI-compatible API.
- `NUSASHELL_AI_API_KEY`: API key.
- `NUSASHELL_AI_MODEL`: model name.
- `NUSASHELL_AI_STUB`: set to `true` to use the static stub provider.
- `NUSASHELL_AI_STREAM`: set to `false` to disable streaming.
- `NUSASHELL_AI_VISION`: `on`, `off`, or `auto`.

## Context limits

`NUSASHELL_AI_CONTEXT_MAX_INPUT_TOKENS`, `RESERVE_TOKENS`, `RECENT_TURNS`, and `SUMMARY_MAX_CHARS` tune compaction. Increase `MAX_INPUT_TOKENS` when the provider supports a larger window.

## Defaults

If no environment variables are set, the backend starts on `0.0.0.0:9130` with an in-memory plugin repository and a stub AI provider.
