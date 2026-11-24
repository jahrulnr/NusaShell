---
name: nusashell
description: Use when configuring or operating NusaShell itself — API providers, ACP agents, projects and workspaces, WhatsApp/Telegram channels, scheduled automations, the `nusashell` CLI, config, or data directory. Also covers how it routes turns and stores history.
metadata:
  short-description: Configure and operate NusaShell
---

# NusaShell

NusaShell is a local, single-binary AI workspace. `nusashell` (or `nusashell serve`) starts a loopback WebUI, usually at `http://127.0.0.1:10994`, where threads run on either an API provider or an ACP agent. The same thread can be followed on the web, relayed to a linked WhatsApp/Telegram chat, or started by a scheduled automation. Projects group threads and bind the folders their tools work in. Conversation history is stored locally under the data directory.

Use the web UI for owner-facing changes when the owner is present; use the CLI for scripted or diagnostic work. The CLI and web UI are the same binary and share one config file and data directory.

## Inspect before changing

Run the read-only commands first and report what you found:

```sh
nusashell version
nusashell serve status
nusashell config path
nusashell provider list
nusashell agent list
nusashell channel list
nusashell automation list
nusashell browser status
```

`channel`, `automation`, `model`, and `conversations` talk to the running server over its loopback API; they need the server up. `provider`, `agent`, `config`, `browser`, and `serve` read or write local state directly. `--addr HOST:PORT` selects the server, else `$NUSASHELL_ADDR`, else the configured port. Where a command accepts `--json`, prefer it when you need to parse the result.

Never print API keys, bot tokens, or other secret values. Provider and agent records store references (`env:NAME` or a vault reference), not the key itself.

## Providers and models

```sh
nusashell provider list
nusashell provider show <id>
nusashell provider add <id> --style completion --endpoint <url> --models a,b --key-stdin
nusashell provider set <id> --key-env OPENROUTER_API_KEY
nusashell provider delete <id>
```

Wire styles: `completion` (OpenAI-shaped `/chat/completions`; also OpenRouter, DeepSeek, Groq, xAI, Mistral, Ollama, and compatible endpoints), `responses` (OpenAI `/responses`), `messages` (Anthropic `/messages`), `generatecontent` (Gemini). Key flags: `--key-stdin` or `--key-env NAME` (preferred), `--key` only when the owner accepts shell-history exposure, `--clear-key` on `set`. A provider whose `env:` variable is unset shows as `broken` and cannot be selected.

A thread binds a provider/model; `nusashell model <thread> <provider>/<model>` rebinds it. The split is at the first `/`.

## ACP agents

```sh
nusashell agent list
nusashell agent detect --add
nusashell agent add <id> --command <cli> --arg ... --policy auto_safe
nusashell agent set <id> --auto-start --default mode=plan
nusashell agent remove <id>
```

The agent CLI must be installed and signed in for the same user that runs NusaShell. Approval policies are `ask`, `auto_safe`, and `auto_all`. `--env NAME=VALUE` vaults a value, `env:VAR` reads the server's environment, and `--auto-start` keeps the process resident. Changes apply on the agent's next turn.

## Thread browser

`nusashell browser status` names the Chrome/Chromium it resolves or lists install options; `nusashell browser install download` fetches Chrome for Testing without root. Threads drive a local tab the owner can watch, with profile folders under `<storage>/browser` (configurable with `browser.dir`) that keep logins across restarts.

## Projects and workspaces

A project groups threads and supplies their workspace: the first folder is the working directory, additional folders are reachable by absolute path. A thread outside a project can bind its own workspace from the composer. The workspace binds file tools, terminal execution, and the `workspace` context record. Use `Settings → Projects` or the thread's project menu; project membership is metadata, not a copy of the files.

## Chat channels

```sh
nusashell channel list
nusashell channel whatsapp show
nusashell channel whatsapp set --allow-add "+62 812 3456 7890" --send-pacing 5
nusashell channel telegram set --group-add -1001234567890
printf '%s' "$TOKEN" | nusashell channel telegram set --token-stdin
nusashell channel whatsapp send --to 6281234567890 --text "Meeting at 9"
```

WhatsApp connects by pairing a device from `Settings → Channels`; Telegram needs a BotFather token and an allowlist. The allowlist is part of the security boundary: do not widen it unless the owner asked. A chat message becomes a turn in the linked thread; the thread's final reply returns to the chat, while the web keeps the full stream. Keep replies to channels light: short paragraphs and simple bullets, not wide tables or deeply nested lists. `send` can deliver text, media, reactions, locations, contacts, events, and polls outside a turn.

## Automations

```sh
nusashell automation list
nusashell automation add --name "Morning brief" --prompt "Summarize what changed" \
  --cron "0 8 * * *" --tz Asia/Jakarta --thread same \
  --provider openrouter --model openrouter/auto --notify-channel telegram --notify-target 42
nusashell automation set <id> --enabled=false
nusashell automation run <id>
nusashell automation history <id>
```

Exactly one of `--cron` (five-field) or `--at` (RFC 3339 or `YYYY-MM-DDTHH:MM` in `--tz`) is required, plus a runtime (`--agent`, or `--provider` and `--model`). `--thread new` opens a fresh thread per run; `same` reuses one. The server must be running when a schedule is due; an overdue run fires once after startup. Automation turns have no person present: do not ask questions in them; decide, state assumptions, and finish or report the blocker.

## Config, data, and secrets

`nusashell config path` prints the config file. An installed binary defaults to `~/.config/nusashell/config.json`; a source build uses `$CONFIG_PATH` or `./data/config.json`. `nusashell config show` prints effective values, `config set <dotted.key> <value>` patches one leaf, and `config validate` checks the file. Unknown keys are refused by the server rather than silently ignored.

The data directory (`storage.dir`, default `./data`; installed default `~/.local/share/nusashell`) holds providers, agents, `conversations/`, logs, `secrets.db`, and `secrets.key`. `secrets.db` is useless without `secrets.key`; back them up together and never expose either. Do not edit the database or JSONL history directly when a CLI or web action exists.

Common sections: `conversation.mailbox_cap`, `conversation.max_turn_steps`, `conversation.compact_budget_pct`, `conversation.compact_keep_tail`, `service.header_timeout`, `service.idle_timeout`, `agents.idle_timeout`, `storage.dir`, `log.max_lines`, `log.max_files`, `websearch.*`. `conversation` and `websearch` are reread per turn; `service`, `storage`, and `log` apply on restart.

## Troubleshooting

- WebUI unreachable: `nusashell serve status`, then the service log (`journalctl --user -u nusashell.service -n 60 --no-pager` on Linux). Check the port and `server.bind`.
- Provider auth or "unknown model": verify the key reference, endpoint, and exact model id.
- Agent not detected: run its command as the NusaShell user; a service may have a different `PATH`.
- Channel silent: check `nusashell channel list`, the allowlist, and whether the sender is blocked.
- Automation did not run: check `enabled`, next run time, time zone, and that the server was running.

## Safety

Ask before deleting a provider, agent, automation, project, or thread, and before changing an allowlist or exposing the listener beyond loopback. Prefer reversible changes. Read `README.md`, `docs/cli-guide.md`, and `docs/tutorials/` in the NusaShell repository when they are available; the running binary is the final authority.
