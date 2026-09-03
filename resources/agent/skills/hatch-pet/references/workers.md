# Isolated Review Workers

## Responsibilities split

- **Parent owns**: manifest updates, output copies, all `generate_media`
  calls, deterministic scripts, assembly, despill, packaging, cleanup, and
  severity resolution.
- **Workers own independent visual judgment**: blind direction QA (mandatory,
  exactly three isolated workers), final visual QA (mandatory, independent),
  brand discovery (optional), row sanity (optional).

Spawn workers with `subagent` (ACP agents such as Codex, Cursor, or Devin;
pass a self-contained brief with absolute paths) or `delegate` (internal
headless agent with the standard toolbox). Both are context-isolated.

## Isolation rules

- A blind QA worker must never have seen the labeled direction sheet, the
  atlas, direction prompts, the degree order, the answer key, or another
  worker's verdicts. Spawn each of the three fresh; never reuse one.
- Label-conditioned classification is not independent evidence. Never use a
  worker that has reviewed labeled material for the blind pass.
- Review workers read files and inspect images only; briefs must forbid
  editing files, queueing repairs, packaging, or cleanup.
- Model choice: prefer a smaller capable model (model override) for review
  and research workers; use the parent/default model for orchestration or
  when no override is available.
- If no worker platform is available, ask the user to inspect the sheets
  instead; never let the parent self-approve its own repaired look row.

## Blind direction QA worker (spawn 3, fresh each)

Give each worker only this brief, with a distinct output number N (1, 2, 3):

```text
Classify one required gaze axis in an unlabeled hatch-pet A/B challenge.

Blind sheet: <RUN_DIR>/qa/direction-blind-pairs.png

Inspect only this sheet (read it as an image). Do not open the atlas, labeled
direction sheet, prompts, prior QA, degree order, answer key, or any other
file.

Each row contains two normal-size pet cells labeled A and B and identifies
the axis to judge. For a horizontal row, classify each cell as exactly
`screen-left`, `screen-right`, or `ambiguous`. For a vertical row, classify
each cell as exactly `up`, `down`, or `ambiguous`.

Judge only what is readable at the displayed pet size. Use visible landmarks
such as pupils, nose tip relative to head center, face surface, head turn,
eyelids, or the pet's natural aiming feature. If the requested axis is not
definite without enlarging or guessing, classify it as `ambiguous`; do not
invent confidence.

Do not infer from A/B order. If A and B point the same way, report the same
classification; do not force one left and one right.

Write your result to <RUN_DIR>/qa/direction-blind-verdicts-<N>.json and
return exactly one JSON object and nothing else:
{"pairs":[{"pair":"horizontal-1|vertical-1","A":"screen-left|screen-right|up|down|ambiguous","B":"screen-left|screen-right|up|down|ambiguous","reason":"short landmark evidence"}]}

Include every pair shown in the sheet.
```

The parent verifies each verdict file exists and parses; if a worker could
not write the file, the parent writes it from the returned JSON exactly as
returned.

## Final visual QA worker (single, independent)

```text
Visually QA one finalized hatch-pet run.

Run dir: <RUN_DIR>
Contact sheet: <RUN_DIR>/qa/contact-sheet.png
V2 contact sheet: <RUN_DIR>/qa/contact-sheet-extended.png
Focused direction QA sheet: <RUN_DIR>/qa/look-directions.png
Direction semantics JSON: <RUN_DIR>/qa/direction-semantics.json
Blind direction validation JSON: <RUN_DIR>/qa/direction-blind-validation.json
Look continuity JSON: <RUN_DIR>/qa/look-continuity.json
Preview dir: <RUN_DIR>/qa/previews
Review JSON: <RUN_DIR>/qa/review.json
V2 validation JSON: <RUN_DIR>/final/validation-extended.json

Inspect the contact sheets, preview GIFs, and direction sheet visually (read
them as images). Confirm the same pet identity, style, palette, silhouette,
face, proportions, and props across all rows:
0 idle, 1 running-right, 2 running-left, 3 waving, 4 jumping, 5 failed,
6 waiting, 7 running (task work), 8 review.

Require `qa/direction-blind-validation.json` to have `ok: true`, or an
explicit accepted minor override in `qa/blind-review-resolution.json`.
Cardinal mismatches or ambiguity are major and block packaging. For
intermediate warnings or a fail verdict, inspect the labeled normal-size pose
and ordered loop; accept when the issue is minor with no wrong-quadrant pose
or reversal.

Inspect the 16 direction cells as a labeled ordered loop against the neutral
frame and review `qa/look-continuity.json`. Produce a pass, warning, or fail
semantic verdict for every expected direction: `000 up`, `022.5 up-right`,
`045 up-right`, `067.5 up-right`, `090 right`, `112.5 down-right`,
`135 down-right`, `157.5 down-right`, `180 down`, `202.5 down-left`,
`225 down-left`, `247.5 down-left`, `270 left`, `292.5 up-left`,
`315 up-left`, and `337.5 up-left`. Record separate horizontal and vertical
landmark evidence for every diagonal. Fail wrong or ambiguous cardinals,
labeled wrong-quadrant poses, and visible reversals. Record blind uncertainty
on intermediate poses as warnings when labeled review and loop context
confirm the intended direction.

Fail rows with identity drift, missing/blank frames, copied guide marks,
white/nontransparent backgrounds, cropped bodies, slot overlap, detached
effects, shadows/glows/smears/dust, motion that does not match the row state,
unintended size popping, wrong facing direction, reversed or non-alternating
gait, or idle loops that are effectively static. Judge chroma only on the
cleaned extended contact sheet, not the pre-cleanup standard contact sheet.
Do not fail or retry a row for chroma fringe after the final despill report
and v2 atlas validation pass; those deterministic results are authoritative.

Do not edit files, queue repairs, package, clean up, or inspect unrelated
files. If you have file access, write the per-direction verdicts to
<RUN_DIR>/qa/direction-semantics.json (every direction in order with
verdict, expected, observed, reason).

Return exactly:
visual_qa=pass|fail
qa_note=<one sentence summary>
direction_semantics=<semicolon-separated labels with pass/warning/fail and short visual reason>
review_warnings=<semicolon-separated accepted warnings, or none>
repair_rows=<comma-separated row ids, or none>
repair_notes=<short row-specific notes, or none>
```

## Brand discovery worker (optional)

See `references/pipeline.md` section 13 for the full brief. Requires web
search (`web_search`/`web_fetch`); prefer official pages; the worker returns
only the compact handoff fields and the brief file path.

## Row sanity worker (optional)

Use when parent context is tight and a generated strip needs a quick check
beyond the deterministic inspection:

```text
Inspect one generated hatch-pet row strip.

Row strip image: <absolute path>
Expected frame count: <N>

Read the image and check: exactly <N> separated full-body poses left to
right, the same pet identity and pose family across cells, flat pure chroma
background (no white, no scenery), no clipped poses at the strip edges, no
overlapping poses, no detached effects, shadows, text, or guide marks.

Do not open other files or edit anything.

Return exactly:
row_sanity=pass|fail
qa_note=<one sentence>
```

## After a worker fails

Blind or final QA failures go through Blind Review Severity Resolution in
`references/look-directions.md` before any repair: classify major/minor,
repair majors, and record accepted minor overrides in
`qa/blind-review-resolution.json` with the supporting evidence. Major
failures block packaging; never override one.
