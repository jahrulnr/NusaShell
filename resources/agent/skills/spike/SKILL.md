---
name: spike
description: Run throwaway experiments to validate an idea before committing to a real build: decompose into 2-5 feasibility questions, brief research, build an observable prototype, then deliver a VALIDATED/PARTIAL/INVALIDATED verdict. Use when the user says "try it out", "see if X works", "spike this", "quick prototype", "is this even possible", "compare A vs B", or before committing to an approach.
metadata:
  source: "hermes-agent skills/software-development/spike v1.0 (MIT, adapted from gsd-build/get-shit-done) — adapted"
  version: "2"
---

# Spike

Feel out an idea before committing to a real build — validating feasibility, comparing approaches, or surfacing unknowns that no amount of research will answer. Spikes are disposable by design. Throw them away once they have paid their debt.

## When NOT to use

- The answer is knowable from docs or reading code — just research, do not build.
- The work is production path — use planning (`todo.brief`) instead.
- The idea is already validated — jump straight to implementation.

## Core method

```
decompose → research → build → verdict
```

### 1. Decompose

Break the idea into **2-5 independent feasibility questions**. One question = one spike. Present as a table with Given/When/Then framing:

| # | Spike | Validates (Given/When/Then) | Risk |
|---|---|---|---|
| 001 | websocket-streaming | Given a WS connection, when the LLM streams tokens, then the client receives chunks < 100ms | High |
| 002a | pdf-parse-pdfjs | Given a multi-page PDF, when parsed with pdfjs, then structured text is extractable | Medium |
| 002b | pdf-parse-camelot | Given a multi-page PDF, when parsed with camelot, then structured text is extractable | Medium |

Types: **standard** (one approach answering one question) or **comparison** (same question, different approaches — same number, letter suffix a/b/c).

Good spike questions: specific feasibility with observable output. Bad: too broad, no observable output, or just "read the docs about X".

**Order by risk.** The spike most likely to kill the idea runs first. Skip decomposition only if the user already knows exactly what they want to spike.

### 2. Align

Present the spike table. Ask: "Build all in this order, or adjust?" Let the user drop, reorder, or reframe before any code is written.

### 3. Research (per spike, before building)

1. **Brief it.** 2-3 sentences: what this spike is, why it matters, the key risk.
2. **Surface competing approaches** if there is a real choice — table: Approach | Tool/Library | Pros | Cons | Status (maintained/abandoned/beta).
3. **Pick one.** State why. If 2+ are credible, build quick variants inside the spike.
4. **Skip research** for pure logic with no external dependencies.

Use `web_search` to find candidates, `web_fetch` to read actual docs, and `exec` to check installed versions (`pip show x`, `npm ls x`).

### 4. Build

One directory per spike, self-contained:

```
spikes/
├── 001-websocket-streaming/
│   ├── README.md
│   └── main.py
├── 002a-pdf-parse-pdfjs/ ...
└── 002b-pdf-parse-camelot/ ...
```

**Bias toward something the user can interact with.** Spikes fail when the only output is a log line that says "it works". Default choices, in order of preference:

1. A runnable CLI that takes input and prints observable output
2. A minimal HTML page that demonstrates the behavior
3. A small web server with one endpoint
4. A unit test with recognizable assertions

**Depth over speed.** Never declare "it works" after one happy-path run. Test edge cases. The verdict is only trustworthy when the investigation was honest.

**Avoid** unless the spike specifically requires it: complex package management, build tools/bundlers, Docker, env files, config systems. Hardcode everything — it is a spike.

**Parallel comparison spikes (002a/002b) — delegate.** When two approaches can run in parallel and both need real engineering, fan out with `delegate` (one subagent per approach; self-contained brief: goal, Given/When/Then question, expected output, target folder). Each subagent returns its own verdict; you write the head-to-head.

### 5. Verdict

Close each spike's `README.md` with:

```markdown
## Verdict: VALIDATED | PARTIAL | INVALIDATED

### What worked
### What didn't
### Surprises
### Recommendation for the real build
```

- **VALIDATED** = the core question was answered yes, with evidence.
- **PARTIAL** = it works under constraints X, Y, Z — document them.
- **INVALIDATED** = it does not work, for this reason. This is a successful spike.

## Comparison spikes

After two approaches answer the same question, write a head-to-head:

| Dimension | pdfjs (002a) | camelot (002b) |
|---|---|---|
| Extraction quality | 9/10 structured | 7/10 table-only |
| Setup complexity | npm install, 1 line | pip + ghostscript |
| Perf on 100-page PDF | 3s | 18s |
| Handles rotated text | no | yes |

**Winner:** X for our use case. Y if we need table-first extraction later.

## Frontier mode (what to spike next)

If spikes already exist and the user asks "what should I spike next?", walk the existing directories and look for:

- **Integration risks** — two validated spikes touching the same resource but tested independently
- **Data handoffs** — spike A's output assumed compatible with spike B's input; never proven
- **Gaps in the vision** — capabilities assumed but unproven
- **Alternative approaches** — different angles for PARTIAL or INVALIDATED spikes

Propose 2-4 candidates as Given/When/Then. Let the user pick.

## Output

- Create `spikes/` in the repo/workspace root.
- One folder per spike: `NNN-descriptive-name/`.
- `README.md` per spike captures question, approach, results, verdict.
- Keep the code throwaway — a spike that takes 2 days to "clean up for production" was a bad spike.