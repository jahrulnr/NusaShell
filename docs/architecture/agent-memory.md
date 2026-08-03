# Agent Memory

Persistent agent memory for NusaShell — a lightweight, memory
waist that gives the agent cross-conversation recall without adding core
model-tool surface.

## Overview

The agent has two persistent memory targets:

- **`memory`** — personal notes the agent writes about the project, task, or
  working context. Limit: 2200 chars.
- **`user`** — user-profile facts (preferences, habits, environment). Limit:
  1375 chars.

Each target is a single Markdown file (`MEMORY.md` / `USER.md`) stored under
the memory root. Entries are delimited by `§` on its own line.

Optional creation metadata may prefix an entry as
`<!--ns-created:ISO-8601-->` (UTC). It is stripped from the entry `text` used
in prompts, matching, and capacity accounting. Legacy entries without the
prefix load with `createdAt: null`. New `add` writes stamp `createdAt`;
`replace` preserves it. The Learning timeline uses `createdAt` when present
and shows `unknown` for undated legacy entries.

## Architecture

```
packages/application/src/memory/
  ports/memory-store.port.ts   — MemoryStorePort interface
  memory-entries.ts            — pure helpers (split, join, capacity, match)
  index.ts                     — barrel

packages/infrastructure/src/memory/
  filesystem-memory-store.ts   — FilesystemMemoryStore adapter
  index.ts                     — barrel
```

### Port

`MemoryStorePort` defines four operations:

- `loadSnapshot()` — returns both targets with current entries and usage.
- `add(target, content)` — append a new entry.
- `replace(target, oldText, content)` — update a uniquely matched entry.
- `remove(target, oldText)` — delete a uniquely matched entry.

### Pure helpers

`memory-entries.ts` contains all pure logic: splitting/joining, capacity
checking, unique substring matching, and entry mutations. These are fully
unit-tested without any I/O.

### Adapter

`FilesystemMemoryStore` reads and writes `MEMORY.md` / `USER.md` under a
root directory. Writes are atomic (temp file + rename). The root is created
on first use. Capacity is enforced before write — mutations that exceed the
limit throw an error.

## Prompt injection

At the start of each agent turn, `RunAgentTurnHandler` loads a memory
snapshot via `MemoryStorePort`, formats it with `formatMemoryPrompt`, and
injects it as a frozen system message after the developer prompt and before
conversation messages. The snapshot is **frozen for the turn** — the agent
sees the entries that existed when the turn started, not live mutations.

When both targets are empty, no memory block is injected.

## Memory meta-tool

The `memory` meta-tool is always available (like `skill_list`, `docs_search`).
It supports three actions:

- `add` — `action: "add", target: "memory"|"user", content: "text"`
- `replace` — `action: "replace", target, old_text: "unique substring", content: "new text"`
- `remove` — `action: "remove", target, old_text: "unique substring"`

`old_text` must uniquely match one entry. If it matches zero or multiple
entries, the tool returns an error.

## Wiring

- **Backend container** (`apps/backend/src/container.ts`): instantiates
  `FilesystemMemoryStore` with `memoryRoot` (defaults to
  `.nusashell/agent/memory`), passes it to `McpAgentToolGateway` (5th arg)
  and `RunAgentTurnHandler` (last arg).
- **Electron** (`apps/desktop/src/main/index.ts`): sets `memoryRoot` to
  `{userData}/memories/` and passes it through `bootstrap()`.

## What is NOT in this phase

- Skill write-to-model integration
- Background memory review or curator
- Memory UI in the desktop shell
- Cross-project memory sync

These are deferred to later phases.
