# Data locations

All application data lives under a single data directory. The default is the
platform config directory plus `nusashell-go`:

- Linux:   `~/.config/nusashell-go`
- macOS:   `~/Library/Application Support/nusashell-go`
- Windows: `%APPDATA%\nusashell-go`

Override with the `NUSASHELL_DATA_DIR` environment variable.

## Layout

| Path | Content | Format |
| --- | --- | --- |
| `conversations/*.json` | one file per agent conversation | JSON |
| `providers.json` | provider configs (no keys) | JSON |
| `skills.json` | skill library | JSON |
| `mcp-servers.json` | MCP server definitions | JSON |
| `memories.jsonl` | agent memory entries | JSONL |
| `logs.jsonl` | activity log (bounded ring) | JSONL |
| `settings.json` | compaction / caching settings | JSON |
| `credentials.db` | API keys per provider | SQLite |

Credentials never appear in the JSON/JSONL files. Deleting the data
directory removes everything, including stored keys.
