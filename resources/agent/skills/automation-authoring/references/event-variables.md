# Event variables

An `agent:` prompt may contain `${event.<key>}`. The renderer looks up
top-level event fields first, then direct or dotted keys in `Attributes`.
Missing values render as an empty string. Values are stringified; the syntax
does not evaluate expressions or shell variables.

| Variable | Meaning | Available from |
| --- | --- | --- |
| `${event.type}` | normalized event type, for example `telegram.message` | every event |
| `${event.source}` | event source/server identifier | every event |
| `${event.subject}` | display subject, sender, or chat label | every event |
| `${event.chat_id}` | destination/chat identifier | Telegram message events |
| `${event.message_id}` | source message identifier | Telegram message events |
| `${event.chat_type}` | `dm`, `group`, `channel`, or empty when unknown | Telegram message events |
| `${event.text}` | truncated inbound message text | Telegram message events |
| `${event.from_me}` | whether the message came from the bot | Telegram message events |
| `${event.<custom>}` | a custom attribute supplied by an event publisher | custom events |
| `${event.<nested.path>}` | a nested custom attribute | custom events |

The Telegram notification bridge emits `telegram.message` only when the
payload has a plugin, event name, and chat ID. It ignores `from_me: true` so a
bot's own reply cannot recursively trigger the same workflow. Event delivery
is deduplicated by event ID, trigger ID, and workflow ID.

Use trigger `where` for cheap matching before starting an agent:

```yaml
triggers:
  - when:
      event: telegram.message
      where:
        chat_type: dm
        subject_contains: Jahrul
```

Use the exact `chat_id` and `message_id` in the agent step for the action. Do
not use `unread_count` as a substitute: reading a message can clear unread
state before the workflow runs, while the event identity remains stable.
