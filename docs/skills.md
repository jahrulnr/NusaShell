# Skill improvement

NusaShell can watch its own finished turns for patterns worth turning into skills. The first phase only observes. It logs one line for each turn that shows a signal. It writes no skill files, calls no model, keeps nothing between restarts, and shows nothing in the web UI.

## Current state

The server reports every finished turn to the observer, whether the hub admitted it or a room job ran it, so the observer runs for any room runtime. A turn is logged only when it passes the gate below and at least one signal fires: a plain send that rewrites nothing and fires no signal writes no line. A turn that failed or was stopped writes no line.

## Turn it on

Observation is off by default. To turn it on, set the mode and restart the server:

```text
nusashell config set skills.improvement observe
```

The mode is read once, at startup, so the change takes effect after a restart. `off` registers nothing: the server keeps no edit evidence and runs no observer. Only `off` and `observe` are accepted; any other value is rejected.

To turn it back off, run `nusashell config set skills.improvement off` and restart the server.

## What the log shows

A line is written only when a turn passes the gate below and at least one signal fires. It goes to the server's standard error, with its other log lines, and not to the `logs/` directory. Under systemd, read it with `journalctl --user -u nusashell`; when run in a terminal, it appears where `nusashell serve` runs.

```text
skill_observe conv=c_example kind=send signals=long,recovered window_msgs=5 window_runes=1840 owner="fix the parser in the lexer" elapsed_ms=12
```

| Field | Meaning |
|---|---|
| `conv` | conversation id |
| `kind` | command that opened the turn: `send`, `edit`, or `resume` |
| `signals` | fired signals, comma-separated, in this order: `long`, `owner_rewrote`, `recovered` |
| `window_msgs` | messages in the turn's bounded window |
| `window_runes` | size of the window's text, in runes |
| `owner` | the owner's message that opened the turn, redacted for known secret patterns and then cut to 120 characters |
| `elapsed_ms` | how long the observation took |

The signals:

- `owner_rewrote`: the owner rewrote the message that opened this turn.
- `recovered`: a tool call the model made failed, and a later call to the same tool in the same turn succeeded.
- `long`: the model called tools in at least eight rounds of this turn.

The line never carries tool arguments, tool output, or reply text.

A failed read of the conversation is logged as `skill_observe_failed conv=<id> err="<message>"` at WARN level, and that turn is skipped.

## Which turns are observed

A turn is observed when it finished, its reply was not interrupted, it ended without an error, and it started from the web or from a chat channel. Turns that started from an automation, from another conversation, or from a completion are skipped.

## Limits

- The edit evidence behind `owner_rewrote` lives in memory. A restart drops it, and each conversation keeps only its latest rewrite.
- Observation runs after a turn has ended, so it does not delay the turn. Its read of the conversation is bounded to 30 seconds.
