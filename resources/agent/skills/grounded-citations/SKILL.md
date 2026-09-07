---
name: grounded-citations
description: Ground answers and documents in cited, verifiable sources: a url→[n] ledger populated at retrieval time (never from memory), inline per-sentence citations, a mechanically rendered Sources block, and a fact-check mode with verbatim quotes and [unverified] markers. Use when an answer or document rests on fetched information — research, comparisons, news summaries, reports, briefs, multi-source synthesis, or any deliverable where the reader will want to check your work.
metadata:
  source: "hermes-agent skills/research/grounded-citations v1.1 (MIT, Hermes Agent + Teknium) — adapted"
  version: "2"
---

# Grounded citations

Every claim taken from an outside source gets an inline numbered citation and a `Sources:` list. A ledger owns the `url → [n]` mapping so the numbers and URLs come from retrieval, never from memory — you only ever emit small integers the ledger handed you.

For high-stakes work the same ledger doubles as a fact-checking chain: verbatim quotes are attached to each source (rejected unless they literally appear in the fetched page text), model-knowledge claims are flagged `[unverified]`, and verification fails any draft whose cited sources carry no evidence.

## When to use

- Answers or artifacts resting on fetched information: research, comparisons, news summaries, "what is the current state of X".
- Any deliverable written to disk that quotes, paraphrases, or reports outside facts: reports, briefs, docs, decks.
- Multi-source synthesis where conflicting sources must be attributed.
- Fact-finding where the user will want to check your work.

Skip inline citations when retrieval is incidental to another task: a quick syntax/version lookup mid-coding, casual conversation, creative writing. Mention a URL only if the user would plausibly want the link.

## The ledger

Location: `<working-dir>/.citations/ledger.json` (override per task via the `CITATION_LEDGER` environment variable). Script: `scripts/citations.py` (stdlib Python 3; run with `python3`, or `python` on Windows).

```
python3 scripts/citations.py reset                          # clean ledger
python3 scripts/citations.py add https://example.com/a --title "A"   # prints: [1]
python3 scripts/citations.py add https://example.com/b --title "B"   # prints: [2]
python3 scripts/citations.py list                          # ledger table
python3 scripts/citations.py quote 1 --text "..." --from page.txt
python3 scripts/citations.py render --cited-in draft.md    # Sources block
python3 scripts/citations.py verify draft.md [--strict] [--min-coverage 0.6] [--evidence]
```

`add` is idempotent and URL-normalized: the same page always returns the same id within a ledger, so ids stay stable across many search/extract rounds.

## Procedure

① **Reset the ledger** at the start of a task that will produce a grounded answer or document. Skip the reset when continuing work whose ids are already in a draft — reusing the ledger keeps the numbering stable.

② **Register every source at retrieval time.** After each `web_search` / `web_fetch` / fetch, pass the URLs to `citations.py add` (or `ingest` raw JSON output). Do this BEFORE writing prose. Registering later, from memory, is the failure mode this skill exists to prevent.

③ **Cite while drafting.** Place the bracketed id(s) immediately after each sentence the source supports:

```
Ice floats because it is less dense than liquid water.[1]
```

- No space before the bracket; each id in its own brackets. Max 3 ids per sentence. Cite per sentence, not one dump at the end.
- Only ids the ledger returned. Never invent an id or a URL.
- Claims from your own knowledge get no citation.
- Conflicting sources: present both readings, each with its own id.
- Quote exact figures, dates, and names as the source states them; flag gaps explicitly ("no source found for X") instead of smoothing them over.

④ **Append the Sources block** with `render --cited-in <draft>` so the id → URL mapping is generated mechanically from the ledger, not retyped.

⑤ **Verify before delivering** — `citations.py verify <draft>` exits non-zero on unknown ids, on a Sources block that disagrees with the ledger, or (with `--min-coverage`) on prose that is too thinly cited. Fix and re-run.

⑥ **Chat answers** follow the same steps with the draft in your reply: register sources, cite inline, end with the rendered `Sources:` list. For a short answer you may render from `render --only <ids>` instead of writing a file.

## Fact-checking mode

For work where the reader must be able to check the chain — medical, legal, financial, safety, disputed claims, or when the user asks for fact-checking — upgrade from citations to evidence:

① **Attach a verbatim quote per source.** After extracting a page, save its text to a file and attach the sentence(s) that carry each claim:

```
python3 scripts/citations.py quote 1 --text "..." --from page1.txt
```

The quote is rejected unless it appears verbatim in the evidence text (insensitive to whitespace, case, and markdown markup), so a paraphrase or misremembered figure cannot masquerade as evidence. Copy-paste from the fetched text; never retype.

② **Flag model-knowledge claims with `[unverified]`:**

```
The refactor likely predates the 2.0 release.[unverified]
```

`verify --min-coverage` counts `[unverified]` sentences as covered — the goal is declared provenance for every claim, not a citation on every sentence. If a key claim can be checked, check it; `[unverified]` is for what genuinely cannot be.

③ **Cross-check disputed facts against a second independent source.** When two sources disagree, cite both readings with their own ids and quotes, and say which you weight and why. One source is reporting; two independent sources are corroboration.

④ **Evidence gate**: `citations.py verify draft.md --evidence --min-coverage 0.5` fails if any cited source has no attached quote. `render --style evidence --replace-in draft.md` prints each source's quotes beneath its URL.

**What `--min-coverage` counts.** Coverage is `sentences with declared provenance / prose sentences`. A prose sentence is a non-empty line fragment of 4+ words after the Sources block, headings, table rows, and fenced code are dropped. Provenance is a `[n]` citation or an `[unverified]` marker. Run `verify` without a threshold first and read the `info: stats:` line before picking a number.

## Pitfalls

- **Registering after writing.** The ledger must be populated from tool output, not reconstructed from memory.
- **Fake citations.** Ids only come from the ledger; if the ledger is empty for a claim, use `[unverified]` or find the source.
- **Hand-typed Sources blocks.** Always render mechanically; hand-typing drifts from the ledger.
- **Non-verbatim quotes.** Paraphrases are rejected by the matcher — if a quote is rejected, the text really is not in the page; re-verify the claim.