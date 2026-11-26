# NusaShell CLI Guide

`nusashell` is one binary: running it (or `nusashell serve`) starts the
loopback WebUI where conversations happen, and its subcommands manage
API providers, ACP agents, configuration, the host service, and
offline conversation repair — no account system or telemetry.

This guide covers installing the binary, wiring a provider or an agent,
and the knobs the config file exposes. For what the engine is doing
underneath, see [Architecture](./architecture.md) and the
[Domain Model](./domain-model.md).

## Install

NusaShell is a single Go module, `github.com/jahrulnr/nusashell`. It has no
cgo dependencies — the secret store uses a pure-Go SQLite
(`modernc.org/sqlite`), so `go build` produces the whole product. Use the
Go version declared in `go.mod` or newer.

```sh
# Build from source — binary lands in ./nusashell
go build -o nusashell ./cmd/nusashell

# Or via the Makefile — binary lands in ./bin/nusashell
make build

# Build and run in one step
make cli

# WebUI (HTTP API + browser UI + WebSocket turns)
make serve
# or: nusashell serve [--port N] [--open]
```

`serve` binds `server.bind` (default `127.0.0.1`) on `server.port`
(default `10994`); `--port` overrides the port for one process. Configured
`server.hosts` values are the accepted `Host`/`Origin` names for HTTP and
the WebSocket upgrade — see [WebUI API contract](./api/contract.md).
`--open` points a desktop browser at the WebUI once the listener is up;
when a NusaShell already holds the port, it just opens that server's URL
instead of failing on the bind.

### Logging in

`server.auth` decides when the WebUI asks for the owner password:

| Value | Login |
|---|---|
| `auto` (default) | required when the server is reachable beyond this machine: `server.bind` is not a loopback address, or `server.hosts` names a host that is not `localhost` or a loopback IP |
| `on` | always required |
| `off` | never required; refused at startup when the server is reachable |

The first start that needs a login and has no password generates one and
prints it once. Set your own with:

```sh
nusashell password
```

It reads the new password twice with echo off (`--stdin` reads one line
from standard input) and logs out every open session. A signed-in page can
change it too, in Settings → General → Account, which asks for the current
one. To serve behind a
TLS reverse proxy, keep `server.bind` on `127.0.0.1` and name the address
the world reaches in `server.public_url` (an `http`/`https` URL with a
host, no credentials): its host is accepted like a `server.hosts` entry,
`serve --open` prefers it, and the login turns on by itself. The setting
opens no listener — it only declares where the proxy answers.

Running from source without building also works:

```sh
go run ./cmd/nusashell
```

Prebuilt Linux and Windows binaries (amd64 and arm64) come from each GitHub
release. The installer downloads the latest one, verifies it against
`SHA256SUMS`, and runs `nusashell serve install` with any arguments it was
given:

```sh
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash
curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash -s -- --no-service
```

```powershell
irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex
powershell -ExecutionPolicy Bypass -File install.ps1 -NoService
```

Install that same binary for the current user. `nusashell serve install`
copies it to `~/.local/bin/nusashell` on Linux and to
`%LOCALAPPDATA%\NusaShell\bin\nusashell.exe` on Windows, then registers a
WebUI service with the host's service manager — a systemd user unit where
systemd is available, a logon Task Scheduler task on Windows. The CLI and
the WebUI are one executable: the service is the binary launched with
`serve`, and it starts at login.

Install and update are the same command: when a service is already
registered, `serve install` replaces the binary and restarts it onto
the new build — no `stop`/`start` round trip needed. A crashed or
stopped service is started rather than restarted.

```sh
make install                            # build, install CLI, start WebUI at login
./nusashell serve install                 # same, from a binary you already built
./nusashell serve install --no-service    # CLI only
./nusashell uninstall                     # remove the binary and the service
```

`~/.local/bin` is already on `PATH` for a login shell once the directory
exists. If the current shell does not see it, install prints the line to add
(`export` on Linux, PowerShell on Windows). It does not edit shell startup
files.

`nusashell serve start`, `stop`, and `status` manage that service through the
same manager port, and `nusashell uninstall` removes it along with the binary.
`--linger` on `serve install` asks the manager to keep the WebUI running
after logout — on systemd that is `loginctl enable-linger`; Task Scheduler has
no equivalent. Without it, the service starts at login and stops when the
session ends. Config and data directories are left in place by uninstall.

The install also writes a per-user menu entry: a
`~/.local/share/applications/nusashell.desktop` launcher on Linux (with the
icon under `~/.local/share/icons/hicolor/256x256/apps/`) and a
`%APPDATA%\Microsoft\Windows\Start Menu\Programs\NusaShell.url` shortcut on
Windows. `uninstall` removes both. There is no system-wide unit. The listener
stays on loopback; open the URL install prints, or use `serve --open`.

On Windows the service is a hidden Task Scheduler logon task:

```powershell
schtasks /Run /TN nusashell     # start now
schtasks /End /TN nusashell     # stop
nusashell serve status          # Task Scheduler state
```

Two soft dependencies are worth knowing about:

- `/diff` shells out to `git`; it only works in a workspace that is a git
  checkout.
- `/copy` uses the OS clipboard through `atotto/clipboard` — on Linux that
  means `xclip`, `xsel`, or `wl-clipboard` must be installed.

## Updates

`nusashell version` prints the build it is running, such as `nusashell 0.10.3`.
A build made without a release version prints `dev`.

The WebUI looks for a newer GitHub release and shows it in three places: a
pill in the conversation header, one toast per release, and the About section
in Settings. The server asks GitHub at most once an hour, and the page reads
that cache every 15 minutes. **Check for updates** in Settings always asks.
Dev builds never check.

**Update now**, in the update dialog or in About, asks **Install update?**
with the target version and then runs the install in the background. It
downloads the release for this platform, checks it against `SHA256SUMS`, and
replaces the binary only once the checksum matches. A progress bar follows
the steps. If the page reloads while an install is still running, the
progress comes back on its own. When the new version answers, the page
reloads.

On Windows the installed `.exe` is locked while the server runs, so the
updater stages `nusashell.exe.new` and a background PowerShell helper swaps it
after the old process exits, then starts the logon task (or the foreground
process). This works both for `serve install` service installs and a
foreground `nusashell serve`.

The restart depends on how the WebUI runs:

- Under a service manager (`nusashell serve install`), the manager restarts the
  unit.
- In a foreground `nusashell serve`, the process replaces itself with the new
  binary, which cuts off any turn still streaming.

There is no rollback. If the new binary does not start, install the previous
release's binary from its GitHub release page with `nusashell serve install`.
In-app install is available for Linux and Windows release builds (the Windows
build downloads `nusashell-windows-<arch>.exe` and restarts the logon task);
a macOS build keeps the check but shows the PowerShell or shell install
command to run in a terminal. **Skip this version** hides the notice for that
release only, and About can show it again.

## Where things live

The client locates one config file and one data directory.

- **Config file** — depends on which binary is running. The copy at
  `~/.local/bin/nusashell` (`nusashell serve install`, `make install`) defaults to
  `~/.config/nusashell/config.json` (`$XDG_CONFIG_HOME/nusashell/config.json`
  when that variable is set), including when a `.env` in the working
  directory names some other file. A source build (`./bin/nusashell`,
  `go run`, `make cli`) still lets that `.env` set `CONFIG_PATH`. An
  exported `CONFIG_PATH` wins over both. The installed file's `storage.dir`
  is `~/.local/share/nusashell` (`$XDG_DATA_HOME/nusashell` when set), so the
  CLI and the login WebUI share one store. When a source build has no
  `CONFIG_PATH` and the user file does not exist yet, the process runs on
  built-in defaults and `nusashell config path` prints `(no config file —
  running on defaults)`. A configured path that does not exist yet is
  written once by `serve` and `config set`: `nusashell: no config
  found; wrote defaults to <path>`. Read-only admin commands never create
  it. On Windows the installed binary defaults to
  `%APPDATA%\nusashell\config.json` and data
  `%LOCALAPPDATA%\nusashell`; the same exported `CONFIG_PATH` override
  applies.
- **`.env`** — before reading `CONFIG_PATH`, the process loads
  `KEY=VALUE` pairs from a `.env` file in the launch directory (comments
  with `#` allowed, quotes stripped). Real environment variables win over
  `.env` entries. Keys that steer child processes or path resolution are
  refused — `LD_*`, `DYLD_*`, `BASH_ENV`, `ENV`, shell-option and
  language-loader hooks (`NODE_OPTIONS`, `PYTHONSTARTUP`, `PERL5OPT`,
  `RUBYOPT`, `*_JAVA_OPTIONS`, …), language library paths (`PYTHONPATH`,
  `NODE_PATH`, `PERL5LIB`, `RUBYLIB`, `GEM_HOME`), `IFS`/`CDPATH`/
  `GLOBIGNORE`, all `XDG_*`, process steering (`PATH`, `HOME`, `TMPDIR`,
  `GIT_SSH_COMMAND`, `SSH_ASKPASS`, `LESSOPEN`, `EDITOR`, `VISUAL`,
  `PAGER`), and the provider-wire controls (`*_PROXY`, `SSL_CERT_*`,
  `*_CA_BUNDLE`, `NODE_EXTRA_CA_CERTS`) — a checkout's `.env` must never
  inject into tool subprocesses, redirect where the binary looks for
  config and data, or sit on the wire the provider keys travel. The shipped
  `.env.example` contains exactly one line: `CONFIG_PATH=./data/config.json`.
- **Data directory** — `storage.dir` in the config file. The tag default
  is `./data`, relative to the working directory, which is what a checkout
  uses when its `.env` points `CONFIG_PATH` at `./data/config.json`. The
  file created by `nusashell serve install` stores an absolute directory instead.
  Created on demand. Everything persistent lives under it:

| Path under `storage.dir` | Contents |
|---|---|
| `providers/<id>.json` | one document per configured provider |
| `secrets.db` | SQLite secret store for vaulted API keys, sealed (mode `0600`) |
| `secrets.key` | the AES-256 key that seals `secrets.db` (mode `0600`) |
| `conversations/<id>/` | `meta.json` plus `active.jsonl` and sealed `N.jsonl` segments |
| `logs/` | provider diagnostics: `active.jsonl` plus rotated segments |

The provider files store credential *references* (`env:VAR` or
`secret:provider/<id>/api_key`), never key material. Vaulted keys sit in
`secrets.db` sealed with AES-256-GCM under `secrets.key`. A copy of the
database alone reveals nothing; whoever can read both files can read the
keys, so protect the directory and back the two files up together.
Losing `secrets.key` loses every vaulted value: set them again.

## First run

The fastest path is the wizard:

```sh
nusashell onboard
```

`onboard` requires an interactive terminal; scripted setups use
`nusashell provider add` directly. The wizard asks for a provider id
(default `openai`), a wire style picked by number, an endpoint (the
default is `https://api.openai.com/v1`, or `https://api.anthropic.com/v1`
when you pick the `messages` style), a comma-separated model list, and an
API key typed on a hidden prompt. It writes exactly what
`provider add` would.

Then start the WebUI and open the address it prints:

```sh
nusashell
```

If only one healthy provider exists, the composer selects it on your first
message; the same goes for a provider with exactly one listed model.
Otherwise pick a model in the composer's model picker, which lists every
provider and every agent side by side.

## Managing providers: `nusashell provider`

A *provider* is a configured API endpoint — OpenRouter, OpenAI,
Anthropic, Gemini, a local server: endpoint, wire style, credential, and a
catalog of model ids. An aggregator provider such as OpenRouter can also
pin the *upstream* that serves a model (DeepInfra, Together, …); the WebUI
picks it per turn. Five subcommands manage them:

```text
nusashell provider list                sanitized table of all providers
nusashell provider show <id>           detail plus the model catalog
nusashell provider add <id> [flags]    register a new provider
nusashell provider set <id> [flags]    edit fields; key flags rotate the credential
nusashell provider delete <id>         remove the provider and its vaulted key
```

`list` prints one row per provider — id, display name, style, model count,
key state, and a `broken` marker when the record can't be used. `show`
adds the endpoint, the stream flag, and one line per model. Neither ever
prints key material; the key column shows `key-set`, `no-key`, or the
stored reference (`env:VAR`, `secret:…`).

### `add` / `set` flags

The flag surface is identical for both commands (`admin/provider.go` is
the source of truth):

| Flag | Meaning |
|---|---|
| `--style <s>` | wire style: `completion`, `responses`, `messages`, or `generatecontent` |
| `--endpoint <url>` | provider base URL — the adapter appends its path |
| `--name <name>` | display name; defaults to the id |
| `--models a,b,c` | replace the whole catalog |
| `--model <m>` | alias for `--models` with a single entry |
| `--stream` / `--no-stream` | toggle streaming (on by default) |
| `--key-env <VAR>` | store an `env:VAR` reference, resolved at send time |
| `--key <literal>` | vault a literal key — visible in `ps` and shell history |
| `--key-stdin` | read one key line from stdin, hidden on a TTY |
| `--clear-key` | drop the credential entirely (`set` only) |

Rules that bite:

- Exactly one key flag per invocation; combining `--key` with
  `--key-env`, for example, exits with a conflict error.
- `add` refuses an existing healthy id — use `set` to edit. It does
  accept an id whose stored record is corrupt, because re-adding is the
  repair path.
- `set` is read-modify-write: flags you pass replace fields, everything
  else keeps its stored value. `--models` swaps the entire catalog rather
  than appending.
- `set` refuses a *broken* record outright — repair it with a full `add`
  or `delete` it.
- On a TTY, `nusashell provider add <id>` with missing fields drops into the
  same prompts the wizard uses, including a hidden API-key prompt.
  Piped stdin requires `--style` and `--endpoint`.
- `delete` also removes the canonical vault entry
  (`secret:provider/<id>/api_key`) and warns when another provider still
  references it.

Example — register OpenRouter with the key vaulted via stdin:

```sh
nusashell provider add openrouter --style completion \
    --endpoint https://openrouter.ai/api/v1 \
    --models "anthropic/claude-sonnet-4,openai/gpt-5" \
    --key-stdin
```

Or keep the key in the environment instead:

```sh
nusashell provider set openrouter --key-env OPENROUTER_API_KEY
```

An `env:` reference is resolved on every send, so rotating the variable
needs no CLI action — but a provider pointing at an unset variable is
marked `broken` in `list` and cannot be selected in the WebUI until the
variable exists.

### Styles, honestly

`--style` accepts `completion`, `responses`, `messages`, and
`generatecontent` — the enum values in
`infrastructure/domain/enum/style.go`, and all four have adapters wired in
`cmd/nusashell/wire.go`. Pick the wire your endpoint speaks:

- `completion` — OpenAI-shaped `POST <endpoint>/chat/completions`; covers
  OpenAI, OpenRouter, DeepSeek, Groq, xAI, Mistral, and the compatible
  long tail.
- `responses` — OpenAI's Responses API at `POST <endpoint>/responses`.
- `messages` — Anthropic at `POST <endpoint>/messages` (`x-api-key` auth).
- `generatecontent` — Gemini at
  `POST <endpoint>/models/{model}:generateContent` (`x-goog-api-key`).

See [Adapters](./adapters.md) for what each wire carries and where new
styles plug in.

## Managing settings: `nusashell config`

The config file holds tunables — never credentials — under these sections:

| Key | Type | Default | Effect |
|---|---|---|---|
| `conversation.mailbox_cap` | int | `128` | queued-job capacity per conversation |
| `conversation.max_turn_steps` | int | `512` | tool-call loop cap per turn |
| `conversation.compact_budget_pct` | int | `80` | percent of the model's input budget that triggers compaction |
| `conversation.compact_keep_tail` | int | `4` | messages kept live across a compaction |
| `service.header_timeout` | duration | `"2m0s"` | deadline for the provider's first response byte |
| `service.idle_timeout` | duration | `"90s"` | per-chunk deadline on a streaming body |
| `agents.idle_timeout` | duration | `"5m0s"` | how long an agent's process may run no turn before the server stops it (at least `1m`); the next message starts it again. Read every minute, so a change needs no restart. An agent set `--auto-start` is never stopped |
| `storage.dir` | string | `"./data"` | the data directory above |
| `log.max_lines` | int | `2000` | rotate `active.jsonl` past this many records |
| `log.max_files` | int | `9` | cap on compressed archives (10 files including active); oldest are pruned |
| `skills.improvement` | `"off"`/`"observe"` | `"off"` | log one `skill_observe` line per eligible finished turn; read at startup, so a change needs a restart. See [Skill improvement](skills.md) |
| `websearch.enabled` | `"on"`/`"off"` | `"on"` | offer `web_search` and `web_fetch` to models; read live |
| `websearch.backend` | string | `"auto"` | who serves searches: `auto` (the thread's provider when it has web search, else the metasearch), `searchwire` (always the metasearch), `provider` (the provider in `websearch.provider`) |
| `websearch.provider` / `websearch.model` | string | `""` | the provider for `backend: provider`, and the model it searches through (OpenCode needs one) |
| `websearch.limit` | int | `0` | results per search, at most 50; `0` is the library's 10 |
| `websearch.timeout` | duration | `"0s"` | per-source timeout, `1s`–`2m`; `0s` is the library's 10 s |
| `websearch.max_response_bytes` | int | `0` | page text a fetch keeps; `0` is 20000 |
| `websearch.max_raw_bytes` | int | `0` | largest download a fetch reads; `0` is 10 MiB |
| `websearch.user_agent` | string | `""` | User-Agent of every request; empty is the library's |
| `websearch.<brave\|serper\|tavily>.enabled` | `"on"`/`"off"` | Brave `"on"`, others `"off"` | use the source; Serper and Tavily do nothing without a key |
| `websearch.<source>.key_ref`, `websearch.github.token_ref` | string | `""` | `env:NAME` or a vault reference — never the key itself; empty reads the source's usual variable |
| `websearch.github.enabled` / `websearch.github.search_issues` | `"on"`/`"off"` | `"on"` | search GitHub repositories, and issues and pull requests too |

A source's key is stored from Settings → Web search, which keeps a pasted
key in the vault and writes only its reference; `config set
websearch.brave.key_ref env:MY_BRAVE_KEY` points at a variable instead.
Startpage and Wikipedia are always searched and take no key. The
`websearch` section is read on every tool call, so its changes need no
restart.

Durations decode with `time.ParseDuration` — `"30s"`, `"5m"`, `"1m30s"`
are all valid; a bare number is raw nanoseconds, which nobody wants, so
quote a string.

Subcommands:

```text
nusashell config show                 effective config — file values over defaults
nusashell config path                 resolved config file path (or "(no config file…)")
nusashell config set <key> <value>    patch one dotted leaf
nusashell config unset <key>          remove an override; defaulted keys reset to their default
nusashell config validate             decode check on the file
```

`set` takes a dotted leaf (`service.idle_timeout`), decodes the value as
a JSON literal (`5`, `true`, `"2m"`), and falls back to a plain string
for bare text — so `nusashell config set service.idle_timeout 2m` stores
`"2m"`. `null` is refused. `set` requires a real leaf in the schema —
typos fail instead of planting dead keys. `unset` removes an existing key
from the input document, including unknown keys left over from removed
fields or comment-as-field additions;
normalization restores its default when one exists. The file is rewritten
atomically as the full normalized document, so what
lands on disk is exactly what the loader computes (durations come back
canonical: `2m` becomes `"2m0s"`). Both `set` and `unset` print
"takes effect on next start"; in practice the server re-reads the
`conversation` section before each turn, while `service`, `storage`, and
`log` bind once at startup.

Strictness is split deliberately: the server refuses to boot on a file that
fails decode — malformed JSON, a mistyped known key, trailing garbage,
**or an unknown key** are errors, not warnings; a typo fails loudly
instead of silently tuning nothing. `config validate` applies the same
strict check and names the offending key. The editor commands are the
repair path: `config set`/`unset` decode the known subset leniently, so a
file carrying stale or comment-as-field keys can still be patched — the
schema-ordered rewrite prunes the unknowns on write.

## Managing agents: `nusashell agent`

An *agent* is an external CLI that speaks the Agent Client Protocol over
stdio and runs a turn with its own tools — Claude Agent, Codex, Gemini
CLI, OpenCode, and others. NusaShell launches one process per agent, keeps
one agent session per conversation, and asks you before the agent runs a
tool unless the agent's approval policy already allows it.

```text
nusashell agent list                   configured agents
nusashell agent show <id>              one agent; environment values stay hidden
nusashell agent detect [--add]         known agent CLIs on PATH; --add registers the new ones
nusashell agent add <id> --command C   register an agent
nusashell agent set <id> [flags]       change fields; unspecified ones keep their value
nusashell agent remove <id>            delete an agent and its vaulted values
```

Flags for `add`/`set`: `--name`, `--command`, `--arg` (repeat; replaces all
arguments on `set`), `--clear-args`, `--env NAME=VALUE` (repeat; the value
is vaulted in the secret store, or `env:VAR` reads the server's
environment), `--env-rm NAME`, `--auto-start` / `--no-auto-start` (keep the
agent's process running from the server's start, or let it stop after a few
idle minutes, the default), `--default ID=VALUE` (repeat; a session option a
thread of this agent starts with unless it picks its own — replaces all on
`set`; `--clear-defaults` drops them; the WebUI lists the options and checks
them), and `--policy ask|auto_safe|auto_all`:

| Policy | Behavior |
|---|---|
| `ask` | every permission request becomes an approval card in the WebUI |
| `auto_safe` | reads, searches, fetches, and thinking run; anything that changes state asks |
| `auto_all` | every request is allowed |

Log in with each agent's own CLI before first use. A running server picks
up changes on the agent's next turn.

## The threads' browser: `nusashell browser`

Threads drive a local Chrome or Chromium. `nusashell browser status` names
the executable that resolves (`browser.exec`, then PATH, standard install
locations, and `<storage>/chrome`) or lists the ways to install one;
`nusashell browser install [method]` runs one: `download` fetches Chrome for
Testing's headless shell into `<storage>/chrome` (no root), `pkg:<manager>`
installs `chromium` with the system package manager. Settings → Tools offers
the same. No server needs to run.

Profile folders live under `<storage>/browser` unless `browser.dir` names
another place — one folder per profile, each keeping its logins across
restarts. Point it elsewhere when the data folder sits on a filesystem that
refuses symlinks (an NTFS mount without the `symlinks` option, for one):
Chrome keeps its process-singleton lock as a symlink and aborts the launch
when it cannot create it, with a message that reads like a missing folder.
An NTFS data folder therefore cannot host the profiles themselves, though
it can host everything else.

```json
"browser": { "dir": "~/.local/share/nusashell/browser" }
```

## Chat channels: `nusashell channel`

WhatsApp and Telegram run inside the server, so `nusashell channel` calls
the running server and its changes apply at once. It finds the server
through `--addr HOST:PORT`, else `$NUSASHELL_ADDR` (the server sets it for
the agents and shells it starts, whatever `--port` it was given), else the
configured port. When the server asks for a login, the command opens a
five-minute session in `nusashell.db` and ends it when done; whoever can
open that database is the owner.

```
nusashell channel list                          # each channel's state
nusashell channel whatsapp show                 # settings (JSON)
nusashell channel whatsapp set --allow-add "+62 812 3456 7890" --send-pacing 5
nusashell channel telegram set --group-add -1001234567890
printf '%s' "$TOKEN" | nusashell channel telegram set --token-stdin
nusashell channel whatsapp groups               # JIDs for --group-add
nusashell channel whatsapp send --to 6281234567890 --text "Rapat jam 9"
nusashell channel whatsapp send --to 120363…@g.us --file report.pdf --caption "Laporan"
echo '{"target":"6281…","type":"poll","text":"Makan di mana?","options":["A","B"]}' \
  | nusashell channel whatsapp send
nusashell channel whatsapp search "rapat" -n 20 --chat 6281…@s.whatsapp.net
nusashell channel whatsapp contacts budi
nusashell channel whatsapp user 6281…@s.whatsapp.net
```

`set` changes only the flags given. `--allow`/`--groups` replace a list,
`--allow-add`/`--allow-remove`/`--group-add`/`--group-remove` change it in
place (numbers compare by their digits, so `+62 812…` removes `62812…`); an
entry that is not a number, `*`, or a group id is refused by name. WhatsApp
also takes `--read-receipts`, `--send-typing`, `--send-pacing N` (least
seconds between two sends to one chat, 0-60), `--queue-cap N` (most
messages waiting for one chat, 1-200), and `--retention-days N` (how long
kept messages stay searchable; 0 keeps them).

`send` delivers one message apart from any room: `text`, `reply`, `edit`,
`revoke`, `reaction`, `image`/`video`/`audio`/`document`, `sticker` (WebP),
`location`, `contact`, `event`, or `poll` (`nusashell channel --help` lists
each type's fields). A `path` is read by the command and sent as data.
Sends to one chat are paced like a room's replies. Votes on a poll sent
this way reach the chat's room as text (`[Poll vote: …]`), not as an
answer to a room's question.

Every message the WhatsApp account sees or sends — including chats that
are not allowlisted, and the history WhatsApp syncs when the device pairs —
is kept in `nusashell.db` for `search`; keeping a message neither runs a
room nor applies the allowlist. A message deleted for everyone is cleared,
an edit replaces the text. `contacts` searches what the linked device
synced and works while disconnected; `send`, `groups`, and `user` need the
connection.

## Automation, model, and cross-thread CLI

These commands talk to the running server over its loopback API; they never
write the database directly. `--addr HOST:PORT` chooses the server (default
`$NUSASHELL_ADDR`, else the configured port), and `--json` prints the raw
response where the command supports it.

### `nusashell automation`

Scheduled prompts: `list`, `get <id>`, `add`, `set <id>`, `pause <id>`,
`resume <id>`, `remove <id>`, `history <id>`, and `run <id>`.

`add` requires `--name`, `--prompt`, exactly one of `--cron`/`--at`, and a
runtime (`--agent A`, or `--provider P --model M`). Optional flags:
`--tz ZONE` (IANA zone; default the server's), `--thread new|same`,
`--project P`, `--expires TIME`, `--notify-channel C --notify-target T`, and
`--enabled=false`. `TIME` is RFC 3339 or `YYYY-MM-DDTHH:MM` in `--tz`.

`set` reads the automation first, changes only the flags given, and writes it
back with its `updatedAt`; it never clears an unmentioned field. `pause` and
`resume` change only `enabled`; `run` fires immediately and prints the run id.

### `nusashell model`

`nusashell model <thread>` prints the thread's runtime binding.
`nusashell model <thread> <selector>` rebinds it:

- `agent:<id>` — run on that agent (its own default model);
- `agent:<id>/<model>` — run on that agent and the model it reports;
- `<provider>/<model>` — run on that provider's model. The split is at the
  first `/`, so `openrouter/openrouter/auto` means provider `openrouter`,
  model `openrouter/auto`.

This is a runtime binding command, not NusaMation's ACP session-option
selector (`value[k=v,...]`); session options stay in Settings → Agents.

### `nusashell conversations`

A terminal client for the built-in `conversation` MCP tools — useful for an
agent whose only interface is a shell, and for debugging. It calls
`POST <url>/api/mcp/conversation` with a thread token:

- URL: `--url`, else `$NUSASHELL_MCP_URL`, else the configured loopback
  address;
- token: `--token`, else `$NUSASHELL_MCP_TOKEN`.

The token is minted per thread and exported on the **MCP child** NusaShell
starts (`nusashell mcp run …`), not on an arbitrary shell; pass `--token` (or
export it) when the shell did not inherit it. `--json` prints the raw MCP
result.

```sh
nusashell conversations list --limit 20
nusashell conversations read c_abc123 --limit 20
nusashell conversations wait c_abc123 --timeout 30
nusashell conversations send c_abc123 "check the failing test"
nusashell conversations spawn "summarize today's changes" --title "Daily summary"
nusashell conversations export --project nusashell
```

The commands mirror the tools: `list`, `projects`, `search`, `export`,
`read`, `wait`, `send`, `stop`, `spawn`, and `report`. `send` does not wait
for the reply; follow it with `wait`. `export` writes JSONL into the calling
thread's tool-output folder and prints the path.

## Conversations

A turn persists your message first, then runs on the room's runtime. On a
provider, the agent loop runs — provider call, tool calls if the model
requests them, another provider call — until the model stops calling tools
or the step cap (`conversation.max_turn_steps`) hits. On an agent, the
agent runs its own loop and NusaShell records what it did. Text sent while
a turn runs queues and drains in order; steering lands in the running turn
at the next safe point. If the estimate reaches `compact_budget_pct` of the
model's input allowance, or the provider answers 413, the turn compacts
history once and retries rather than failing. Agents manage their own
context, so agent rooms do not compact.

A room can switch between a provider and an agent at any turn from the
model picker. A provider sees the agent's tool work as text; an agent that
missed turns starts a fresh session with a recap of the conversation.

### Conversations on disk

Conversations persist under `storage.dir/conversations/<id>/` — a
`meta.json` (title, provider or agent, model, usage counters) plus
append-only JSONL history. Persistence is per-message, so a crash or
restart loses nothing already streamed. See [Persistence](./persistence.md) for the segment format
compaction produces.

If an interrupted compaction leaves one extra sealed segment, stop the app,
back up the affected conversation directory, then use
`nusashell conversation repair <id> --discard-duplicate` when its messages
still lead `active.jsonl`, or `--commit` when the active rewrite removed
them. The command checks the chosen case and recalculates counters when
committing. A missing segment needs restoration from backup; see the
[recovery procedure](./persistence.md#compact-sealing-history).

An edit interrupted mid-commit is a different window: the merged stream
stays staged as `active.jsonl.rewrite` and every read fails naming the
fix. A crash there can leave a mix of files — the sealed segments are
parked as `*.jsonl.rewrite-old` in ascending order from `0.jsonl`, so
the archive shows a parked prefix plus any numbered suffix, or, once the
publish landed, a payload already equal to `active.jsonl`. Only that
layout is diagnosed as an interrupted edit: stop the app and run
`nusashell conversation repair <id> --finish-rewrite`; it validates the
staged stream, publishes it, and removes the leftovers. Any other
mismatch — a parked name with a gap below it, a numbered segment the
parked set has passed, an extra or missing numbered file — keeps the
mismatch's own guidance instead, never the repair verb. The
[state machine](./persistence.md#rewritefrom-editing-a-message) covers
what the crash can and cannot leave behind.

### Tools the model can call

Settings → Tools lists the registered tools with their permission. The model
is offered the `agent` and `general` ones and runs them without a per-call
approval step; `system` entries are harness-side and never offered to the
model. The current wiring (`cmd/nusashell/wire.go`) registers:

| Tool | Permission | Scope |
|---|---|---|
| `read_file` | `agent` | read a UTF-8 text file under the workspace |
| `write_file` | `agent` | create or overwrite a text file under the workspace (dirs `0700`, files `0600`) |
| `apply_patch` | `agent` | literal search-and-replace inside a workspace file |
| `read_media` | `agent` | load a non-text media file so the model can inspect it — workspace-relative or absolute host path |
| `exec` | `agent` | run a shell command, merged stdout/stderr — **unsandboxed** |
| `exec_session` | `agent` | open a persistent terminal session — returns output or a session id |
| `write_stdin` | `agent` | write to / poll a session opened by `exec_session` |
| `skills` | `general` | report the skills available to the bound workspace; hydrated before provider calls, callable to refresh the catalog |
| `read_skill` | `agent` | read the full `SKILL.md` of a listed skill by name |
| `web_search` | `agent` | search the web: the metasearch (Brave, Startpage, Wikipedia, GitHub, and Serper or Tavily with a key) or the provider's own search; see `websearch.*` |
| `web_fetch` | `agent` | fetch one URL and return its readable text |
| `workspace` | `system` | harness-side record that pins the turn's working directory into history |
| `custom_tools` | `system` | harness-side report of custom executable directories on `PATH` |

`summary` (`agent`) is offered only to the compaction request, so ordinary
turns never see it. Custom executables on `PATH` are not tools of their own:
the agent runs them through `exec` or `exec_session`, guided by their
companion skills.

The file tools resolve a relative path inside the workspace and accept an
absolute path anywhere on disk (a plan under `~/.memory`, a file in
`/tmp`), so nothing they reach is off-limits to the agent; `read_media`
takes absolute host paths the same way (the returned reference is still
content-pinned — send-time resolution re-reads the file and verifies the
sha256 the tool recorded). `exec` is not confined at all — it runs in
the launch environment with a default 120 s timeout (the model may ask
for up to 600 s) and spills oversized output to
`<storage.dir>/conversations/<id>/tool-output/`. Spill files remain available
until the user deletes the conversation; deleting the conversation removes
them together with its history. `exec` also accepts a `stdin` field —
one-shot input piped to the command, useful for prompts that read
scripted answers (`sudo -S`, `read`-driven installers).

For actual back-and-forth, `exec_session` opens a persistent terminal
session: the tool waits `yield_time_ms`, then reports either the exit
code or `Process running with session ID N`. `write_stdin` continues
that session — `chars` feeds stdin (an empty `chars` polls output; a
single `\u0003` is Ctrl-C on either backend), so the model can answer prompts,
drive installers, or keep a dev server running across tool calls.
Interactive stdin requires `tty:true` — the default pipes backend gives
the child a closed stdin, and a write attempt fails with guidance to
respawn with `tty`. Sessions
survive turn interruption and die only with their conversation (room
delete or shutdown reaps them); `tty:true` upgrades the backend to a real
pseudo-terminal (Linux) for programs that probe `isatty` — password
prompts reading `/dev/tty`, REPLs. One-shot `exec` stays the right tool
for commands that just run to completion.
`serve install` pins the installer's login `PATH` (plus `~/.local/bin`,
`~/bin`, `~/go/bin`) into the service unit, so the daemon — and every
command `exec` spawns — sees the same toolchain your shell does.
`/help` says it plainly: *exec is
unsandboxed — review command activity.* Watch the `exec <tool>` phase in
the status line.

## Usage, logs, and diagnostics

Token accounting rides in three places: a dim `↳ N in · M out · T total`
line after each turn (cache counters appear when nonzero), the footer's
`~estimate/total` context gauge against the model's input allowance, and
`/usage` for cumulative per-conversation counters.

Provider diagnostics land in `storage.dir/logs/` as JSONL. `active.jsonl`
rotates every 2000 records into numbered `.jsonl.gz` archives, with at most
9 archives retained by default (10 log files total). Request, response, error, and stream bodies
are omitted; metadata and event counts remain without duplicating conversation
content in logs.

## Exit codes and errors

`0` clean, `1` an operation failed, `2` usage error (bad flags, unknown
subcommand, `onboard` without a TTY). Errors print the public message
plus the underlying cause — `invalid provider` always says *why*. In the
WebUI, a failed turn shows a card with a retry; a failed turn
still leaves your user message in history, and the room keeps its
provider/model binding so a retry needs no flags.

## See also

- [../README.md](../README.md) — the pitch and a sixty-second start
- [./architecture.md](./architecture.md) — layers and boundaries
- [./domain-model.md](./domain-model.md) — conversations, providers, models, tools
- [./persistence.md](./persistence.md) — on-disk formats and compaction
- [./adapters.md](./adapters.md) — wire styles and provider translation
- [./extending.md](./extending.md) — adding commands, tools, and adapters
