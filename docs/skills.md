# Skill improvement

NusaShell can watch its own finished turns for patterns worth turning into skills. `skills.improvement` selects how far that goes; the default is `off`.

In `suggest`, the loop proposes skill changes and the owner decides each one. The loop never writes a skill file itself: a file is written only when the owner accepts a proposal.

## Modes

The mode is read once, at startup, so a change takes effect after a restart. Only `off`, `observe`, and `suggest` are accepted; any other value is rejected. The modes are exclusive: `suggest` does not also write the `skill_observe` line.

- `off` registers nothing. The server keeps no edit evidence, runs no observer or candidate pass, serves no Skills route, and does not create `<storage.dir>/skills`.
- `observe` registers one finished-turn hook. It writes one log line when a turn passes the gate below and at least one signal fires, and nothing else: no skill file, no model call, nothing in the web UI. The rewrite evidence behind `owner_rewrote` lives in memory for the life of the process.
- `suggest` adds what `observe` does not:
  - The directory `<storage.dir>/skills`, created at startup. It is a sibling of `builtin-skills`, and learned skills live only in it.
  - A proposal store and an ownership store, both in the database. A proposal waits there until the owner decides it. Who may change a learned skill, and the digest of its file, also stay in the database rather than in the file.
  - One candidate pass on each eligible finished turn. The pass may store one proposal; it never writes a skill file. One pass is in flight per room, so a second finish of that room waits for the first. Two rooms finishing together both run; the store still refuses a second pending proposal of the same skill name.
  - The Skills routes below. `off` and `observe` answer 404 for them.

## Skills routes

The routes take an optional `workspace` query. It selects the workspace skill root the catalog joins; without it, that root is absent.

- `GET /api/skills` lists the catalog with each skill's level and how many proposals wait on it. Reading writes nothing.
- `POST /api/skills/:name/level` records who may change one learned skill: `user` keeps it with the owner, `agent` lets the loop change it. The body carries the `digest` the page showed; if the file has moved since, the move is refused. It does not rewrite the file and does not decide a proposal.
- `GET /api/skill-proposals` lists the proposals waiting on the owner, oldest first, as `{"data": [...]}`. Each row has `id`, `op` (`create` or `patch`), `kind` (`tool` or `method`), `name`, `description` (the description the loop proposed), `target` (a patch's target skill; empty for a create), `tool` (the tool a `tool` skill is about), `reason` (why the loop proposed it), `evidence` (`kind` is `quote`, `rewrite`, `repeat`, or `call`; `quote` holds what the proposal rests on; `date` is when that happened, or null when the turn carried no time), `createdAt`, `open`, and `staleCause` (one sentence on why the proposal is no longer open; empty while it is open). Decided proposals are not listed, and no route shows them.
- `POST /api/skill-proposals/:id/decide` records one decision, described below.

## Deciding a proposal

The decision body is JSON with an `action`:

- `{"action":"accept"}` writes `<storage.dir>/skills/<name>/SKILL.md`, then records the decision and the skill's ownership row in one transaction; the file write is not part of that transaction. The optional `level` is `agent` (the default), which lets the loop change the skill later, or `user`, which keeps it with the owner. A patch is accepted only at `agent`.
- `{"action":"dismiss","reason":"..."}` records the decision. The reason is required: a blank reason is refused with 400, because the reason is how the owner teaches the loop. A reason may not begin with `superseded: `, the prefix the pass writes for the proposals it retires. A create whose file holds the proposal's own bytes, which only an accept whose commit failed leaves behind, has that file moved to `<storage.dir>/skills/.archive/` first, so the name is free again; if the move fails, the proposal stays pending.

A successful decision answers 204. Any action other than `accept` or `dismiss` answers 400. A proposal that is already decided answers 409.

An accept is refused with 409 when the proposal no longer matches the files or the rows:

- a create's name is already a skill in some root, or the skill already has an ownership row;
- a patch's target is no longer a learned skill, or its file or ownership row changed since the proposal was read;
- the catalog would go over `skills.catalog_max_bytes`.

A refused accept leaves the proposal pending, so the owner can read it again. A proposal that can no longer be accepted shows `"open": false`. It stays in the queue: dismissing it still works, and a later pass for the same name retires it with its cause. `open: true` does not promise acceptance, because the catalog budget is left out of `open` and checked when the owner accepts. `open` and the name checks read the catalog for the `workspace` you pass, so pass the same value to the queue and to the decision.

If the file is written but the decision cannot be recorded, the server answers 500, logs the failure, and leaves the file in place. For a create, accepting the same proposal again finishes it. A patch cannot be accepted again: its new bytes sit under the old ownership row, so the proposal shows `"open": false`. Dismiss it, then set the skill's level to `agent` (`Loop may propose`) on the Skills page. The page sends the digest it shows, the file's current one, so the row then describes those bytes. Nothing restores the previous bytes.

### What the Skills page does not do yet

The Skills page (Settings → Skills) has no accept or dismiss controls. It shows how many proposals are waiting and how many of them can no longer be accepted, and it moves levels. Until the page has controls, the owner decides over HTTP.

This describes the tree built on top of `a3f9288` (27 Nov 2026). To check it on another build:

- `rg -n "skill-proposals" web/`: no output means the page does not call the decision route.
- `rg -n "skill-proposals" infrastructure/delivery/http/skill/skill.go`: the route table. The decision route is mounted only when the decision store and the writer are wired, which `suggest` does.

The request and response shapes are in `infrastructure/delivery/http/skill/skill.go`. `docs/api/contract.md` and `docs/api/openapi.yaml` list the four routes.

## What writes a learned skill

Only an accept writes a skill file: it writes `<storage.dir>/skills/<name>/SKILL.md`. The candidate pass never writes one. A dismissal and a retirement by the pass move the loop's own leftover of a create into `<storage.dir>/skills/.archive/`, and write nothing. Its YAML frontmatter carries `name` and `description` and no other field; the body is the skill text. Ownership and the digest of that file stay in the database.

## Turn it on

Improvement is off by default. To observe, set the mode and restart the server:

```text
nusashell config set skills.improvement observe
```

To let the loop propose skill changes and decide them over HTTP, set `suggest` and restart:

```text
nusashell config set skills.improvement suggest
```

`suggest` also reads `skills.catalog_max_bytes` (default `32768`, must be positive), the catalog's byte budget. A draft that would take the catalog over it is not stored, and an accept that would is refused.

To turn it back off, run `nusashell config set skills.improvement off` and restart the server.

## What the log shows

This section describes `observe`. `suggest` writes no `skill_observe` lines.

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

In `suggest`, each candidate pass logs `skill_candidate conv=<id> outcome=<outcome>`. The outcome is one of `stored`, `none`, `rejected`, `skipped`, `cooldown`, `unavailable`, or `target-changed`. A failed read of the conversation is logged as `skill_candidate_failed` at WARN level. A turn that failed, or that the gate turns away, writes no line.

## Which turns are taken

The server reports every finished turn to the hook, whether the hub admitted it or a room job ran it, so the hook runs for any room runtime. A turn is eligible when it finished, its reply was not interrupted, it ended without an error, and it started from the web or from a chat channel. Turns that started from an automation, from another conversation, or from a completion are skipped.

`observe` logs a line only when at least one signal fires; a plain send that rewrites nothing writes no line. `suggest` runs the candidate pass on every eligible finish, including one with no signal. The pass may still store nothing: a turn that ends in an unanswered tool failure is skipped before any model call, and a draft the validator refuses is not stored.

## Limits

- Both hooks run after a turn has ended, so neither delays the turn. The observer's read of the conversation is bounded to 30 seconds, and a candidate pass to 2 minutes.
- The edit evidence behind `owner_rewrote` lives in memory, for `observe` and for `suggest`. A restart drops it, and each conversation keeps only its latest rewrite.
- `suggest` keeps at most one pass in flight per room.
- Three proposals the owner can still accept fill the queue. A pass stores nothing more until one of them is decided or can no longer be accepted. Proposals that can no longer be accepted do not count toward the three.
- While a name has an open proposal, the pass does not propose that name again. A proposal the owner can no longer accept is retired first. A proposal of the same operation that the owner decided in the last 30 days also holds its name; a retirement the pass wrote does not.
