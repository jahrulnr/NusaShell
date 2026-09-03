# Run Pipeline Commands

Conventions used below:

- `SKILL_DIR`: the seeded skill directory. Resolve it with
  `skill(op="search", query="hatch pet")` then `file_list`; scripts live under
  `scripts/`.
- `RUN_DIR`: absolute run folder, kept inside the workspace.
- Run every shell snippet through `exec`.
- First check `python3 -c "import PIL"`. If Pillow is missing, install it
  (`pip install pillow`) or stop and report that the run needs Pillow.

## 1. Prepare the run

For a bare brand/company/product request, run brand discovery first (section
11) and pass the brief file. Then:

```bash
python3 "$SKILL_DIR/scripts/prepare_pet_run.py" \
  --pet-name "<Name>" \
  --description "<one sentence>" \
  --reference /absolute/path/to/reference.png \
  --output-dir "$RUN_DIR" \
  --pet-notes "<stable pet description>" \
  --brand-discovery-file /absolute/path/to/brand-discovery.md \
  --brand-name "<optional researched brand name>" \
  --brand-brief "<optional compact brand cue sentence>" \
  --brand-source "https://example.com/source" \
  --style-preset auto \
  --style-notes "<optional freeform style notes>" \
  --force
```

All arguments are optional except the flags needed to express user
constraints. For text-only requests pass the concept through `--pet-notes`;
the script infers name, description, chroma key, and output directory as
needed. It creates `pet_request.json`, `generation-jobs.json`, `prompts/`,
and `references/layout-guides/`.

## 2. Inspect the manifest

```bash
jq '.jobs[] | {id, kind, status, depends_on, prompt_file, retry_prompt_file, input_images, output_path, derivation_policy}' "$RUN_DIR/generation-jobs.json"
```

A job is ready when its `status` is not `complete` and every id in
`depends_on` is complete. Prefer reading the manifest directly; do not write
helper scripts for status display.

## 3. Generate a visual job

For each ready job:

1. `file_read` the job's `prompt_file`.
2. Call `generate_media` with `media_type="image"`, the prompt text verbatim,
   and `referenced_image_paths` set to the job's `input_images` plus the
   matching layout guide `references/layout-guides/<job>.png`. Max 5
   references; prioritize the canonical base and the layout guide. Look-row
   jobs should include the approved cardinal strip, completed row 9 (for row
   10), and the contact sheet. Layout guides are invisible construction
   references: outputs must not copy guide pixels.
3. Only the `base` job may be prompt-only. Every row job must attach its
   listed grounding images; a row generated without them is invalid.
4. Batch up to three independent ready jobs as parallel `generate_media`
   calls. Backfill slots as jobs finish.
5. On a transport-level failure or `Bad Request`, retry that job once with
   its `retry_prompt_file` and the same references. Still failing: stop and
   report the failing row and prompt paths.

The `generate_media` result reports the saved output path; that file is the
selected source for the job.

## 4. Copy the selected output

```bash
JOB_ID=<job-id>
SOURCE=/absolute/path/to/generated-output.png
OUTPUT_REL=$(jq -r --arg id "$JOB_ID" '.jobs[] | select(.id == $id) | .output_path' "$RUN_DIR/generation-jobs.json")
mkdir -p "$(dirname "$RUN_DIR/$OUTPUT_REL")"
cp "$SOURCE" "$RUN_DIR/$OUTPUT_REL"
```

For the base job, also create the canonical identity reference:

```bash
if [ "$JOB_ID" = "base" ]; then mkdir -p "$RUN_DIR/references"; cp "$RUN_DIR/$OUTPUT_REL" "$RUN_DIR/references/canonical-base.png"; fi
```

## 5. Per-row deterministic QA (immediately after copying)

```bash
ROW_QA_DIR="$RUN_DIR/qa/rows/$JOB_ID"
python3 "$SKILL_DIR/scripts/extract_strip_frames.py" \
  --decoded-dir "$RUN_DIR/decoded" \
  --output-dir "$ROW_QA_DIR/frames" \
  --states "$JOB_ID" \
  --method auto
python3 "$SKILL_DIR/scripts/inspect_frames.py" \
  --frames-root "$ROW_QA_DIR/frames" \
  --json-out "$ROW_QA_DIR/review.json" \
  --states "$JOB_ID" \
  --require-components
```

Treat errors as an immediate repair request; review warnings before accepting
and never defer a clipping, component, or extraction problem to final QA.
Chroma cleanup belongs to the final despill pass and must not trigger row
regeneration. If the only failure is component extraction and the strip has
stable scale and placement, use the `stable-slots` correction (section 9).

## 6. Quick visual sanity (optional)

`read_media` the row strip for a frame-count/background/spacing check, or hand
it to a row sanity worker (`references/workers.md`). The deterministic gates
bind; keep parent-side image reading light.

## 7. Mark the job complete

```bash
UPDATED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
TMP_MANIFEST=$(mktemp)
jq --arg id "$JOB_ID" --arg source "$SOURCE" --arg at "$UPDATED_AT" \
  '(.jobs[] | select(.id == $id)) += {status: "complete", source_path: $source, completed_at: $at}' \
  "$RUN_DIR/generation-jobs.json" > "$TMP_MANIFEST"
mv "$TMP_MANIFEST" "$RUN_DIR/generation-jobs.json"
```

Mark `look-cardinals` complete only after all four anchors pass semantic
review and `decoded/look-anchors-approved.png` exists. Only mark a job
complete after its output has been copied into the decoded path.

## 8. Mirror running-left only when safe

```bash
python3 "$SKILL_DIR/scripts/derive_running_left_from_running_right.py" \
  --run-dir "$RUN_DIR" \
  --confirm-appropriate-mirror \
  --decision-note "<why mirroring preserves this pet's identity>"
```

The script mirrors each generated frame slot in place so the leftward row
keeps the rightward row's temporal order. Never substitute a whole-strip
mirror. If mirroring would change meaning or identity, generate
`running-left` as a normal grounded job instead.

## 9. Intermediate 8x9 atlas

```bash
mkdir -p "$RUN_DIR/final" "$RUN_DIR/qa"
python3 "$SKILL_DIR/scripts/extract_strip_frames.py" \
  --decoded-dir "$RUN_DIR/decoded" --output-dir "$RUN_DIR/frames" --states all --method auto
python3 "$SKILL_DIR/scripts/inspect_frames.py" \
  --frames-root "$RUN_DIR/frames" --json-out "$RUN_DIR/qa/review.json" --require-components
python3 "$SKILL_DIR/scripts/compose_atlas.py" \
  --frames-root "$RUN_DIR/frames" \
  --output "$RUN_DIR/final/spritesheet.png" \
  --webp-output "$RUN_DIR/final/spritesheet.webp"
python3 "$SKILL_DIR/scripts/make_contact_sheet.py" \
  "$RUN_DIR/final/spritesheet.webp" --output "$RUN_DIR/qa/contact-sheet.png"
python3 "$SKILL_DIR/scripts/render_animation_previews.py" \
  --frames-root "$RUN_DIR/frames" --output-dir "$RUN_DIR/qa/previews"
```

Inspect `qa/contact-sheet.png` and `qa/previews/*.gif` (final visual QA worker
or the user) before look rows. Visible key-color fringe here is not a
failure: this sheet predates chroma cleanup. Block progress on identity
drift, pops, reversed cadence, wrong facing, or inert idle loops.

If previews show extraction-induced size popping and the source strips were
stable, rerun extraction deliberately with `--method stable-slots`, rerun
inspection with `--allow-stable-slots`, then re-compose and re-render. It is
a QA-driven correction, not a default.

## 10. Look-direction stage

Write `qa/look-mechanics.md` first (`references/look-directions.md`).

Cardinal anchors:

```bash
CHROMA_KEY=$(jq -r '.chroma_key.hex' "$RUN_DIR/pet_request.json")
python3 "$SKILL_DIR/scripts/extract_cardinal_anchors.py" \
  --strip "$RUN_DIR/decoded/look-cardinals.png" \
  --output-dir "$RUN_DIR/decoded/look-anchors" \
  --chroma-key "$CHROMA_KEY" \
  --json-out "$RUN_DIR/qa/cardinal-anchors.json"
python3 "$SKILL_DIR/scripts/compose_cardinal_anchor_strip.py" \
  --anchors-dir "$RUN_DIR/decoded/look-anchors" \
  --output "$RUN_DIR/decoded/look-anchors-approved.png"
```

Approve all four anchors semantically at final pet size; `090` and `270` must
be unmistakable in viewer coordinates. If one anchor fails, regenerate only
that anchor from `prompts/look-anchor-repairs/<degree>.md`, replace only its
extracted file, and re-run `compose_cardinal_anchor_strip.py`.

Row 9 registration (immediately after row 9 is generated and copied):

```bash
python3 "$SKILL_DIR/scripts/assemble_extended_atlas.py" \
  --base-atlas "$RUN_DIR/final/spritesheet.webp" \
  --look-row-9 "$RUN_DIR/decoded/look-row-9.png" \
  --neutral-cell "$RUN_DIR/frames/idle/00.png" \
  --chroma-key "$CHROMA_KEY" \
  --chroma-threshold 96 \
  --registered-row-output "$RUN_DIR/qa/look-row-9-registered.png" \
  --registration-manifest-output "$RUN_DIR/qa/look-row-9-registration.json"
```

Inspect the eight registered cells in `000`-`157.5` order; record the
semantic and continuity review; hard failures mean resynthesize the complete
row. Row 10 becomes ready only after this passes.

Extended assembly (after row 10 is generated and copied):

```bash
python3 "$SKILL_DIR/scripts/assemble_extended_atlas.py" \
  --base-atlas "$RUN_DIR/final/spritesheet.webp" \
  --registered-row-9 "$RUN_DIR/qa/look-row-9-registered.png" \
  --row-9-registration "$RUN_DIR/qa/look-row-9-registration.json" \
  --look-row-10 "$RUN_DIR/decoded/look-row-10.png" \
  --neutral-cell "$RUN_DIR/frames/idle/00.png" \
  --chroma-key "$CHROMA_KEY" \
  --chroma-threshold 96 \
  --output "$RUN_DIR/final/spritesheet-extended.png" \
  --webp-output "$RUN_DIR/final/spritesheet-extended.webp" \
  --manifest-output "$RUN_DIR/final/spritesheet-extended.json"
```

For repair/upgrade of a user-provided 16-cell source already approved as one
coherent set, use `--look-cells-dir <dir>` instead of the three row inputs.
Never use that path for newly generated repair cells.

Single chroma-cleanup pass (the only one in the workflow):

```bash
python3 "$SKILL_DIR/scripts/despill_chroma_edges.py" \
  "$RUN_DIR/final/spritesheet-extended.png" \
  --output "$RUN_DIR/final/spritesheet-extended.png" \
  --webp-output "$RUN_DIR/final/spritesheet-extended.webp" \
  --chroma-key "$CHROMA_KEY" \
  --json-out "$RUN_DIR/qa/chroma-despill-extended.json"
```

When the report has `ok: true` and atlas validation passes, do not
regenerate imagery, rerun despill, tune thresholds, or add cleanup passes.
Deterministic failure: stop with a pipeline failure instead of retrying
generation.

Validate and build QA sheets:

```bash
python3 "$SKILL_DIR/scripts/validate_atlas.py" \
  "$RUN_DIR/final/spritesheet-extended.webp" \
  --json-out "$RUN_DIR/final/validation-extended.json" \
  --chroma-key "$CHROMA_KEY" \
  --require-v2
python3 "$SKILL_DIR/scripts/make_contact_sheet.py" \
  "$RUN_DIR/final/spritesheet-extended.webp" --output "$RUN_DIR/qa/contact-sheet-extended.png"
python3 "$SKILL_DIR/scripts/make_direction_qa_sheet.py" \
  "$RUN_DIR/final/spritesheet-extended.webp" --output "$RUN_DIR/qa/look-directions.png"
```

Blind axis challenge:

```bash
python3 "$SKILL_DIR/scripts/make_direction_blind_qa_sheet.py" \
  "$RUN_DIR/final/spritesheet-extended.webp" \
  --output "$RUN_DIR/qa/direction-blind-pairs.png" \
  --answer-key "$RUN_DIR/qa/direction-blind-answer-key.json"
```

Give three fresh isolated workers only `qa/direction-blind-pairs.png`
(`references/workers.md`); each writes `qa/direction-blind-verdicts-<N>.json`.
Then combine and validate:

```bash
python3 "$SKILL_DIR/scripts/combine_direction_blind_verdicts.py" \
  --verdicts "$RUN_DIR/qa/direction-blind-verdicts-1.json" \
  --verdicts "$RUN_DIR/qa/direction-blind-verdicts-2.json" \
  --verdicts "$RUN_DIR/qa/direction-blind-verdicts-3.json" \
  --json-out "$RUN_DIR/qa/direction-blind-verdicts.json"
python3 "$SKILL_DIR/scripts/validate_direction_blind_verdicts.py" \
  --answer-key "$RUN_DIR/qa/direction-blind-answer-key.json" \
  --verdicts "$RUN_DIR/qa/direction-blind-verdicts.json" \
  --json-out "$RUN_DIR/qa/direction-blind-validation.json"
```

The cardinal pairs are hard gates; intermediate mismatches become review
warnings resolved by labeled normal-size loop review.

Continuity measurement and labeled semantics:

```bash
python3 "$SKILL_DIR/scripts/measure_direction_continuity.py" \
  "$RUN_DIR/final/spritesheet-extended.webp" --json-out "$RUN_DIR/qa/look-continuity.json"
```

Write `qa/direction-semantics.json` from the independent final visual QA
worker's verdicts (or explicit user inspection): every direction
`000`..`337.5` in order with `verdict` (`pass`/`warning`/`fail`), `expected`,
`observed`, and `reason`; diagonals include separate horizontal and vertical
landmark evidence. Packaging requires no `fail` verdicts; reviewed warnings
are allowed. A QA `fail` goes through Blind Review Severity Resolution in
`references/look-directions.md` before any repair.

## 11. Package v2

```bash
PET_ID=$(jq -r '.pet_id' "$RUN_DIR/pet_request.json")
DISPLAY_NAME=$(jq -r '.display_name' "$RUN_DIR/pet_request.json")
DESCRIPTION=$(jq -r '.description' "$RUN_DIR/pet_request.json")
DATA_DIR="${NUSASHELL_DATA_DIR:-$HOME/.config/nusashell}"
PET_DIR="$DATA_DIR/pets/$PET_ID"
mkdir -p "$PET_DIR"
cp "$RUN_DIR/final/spritesheet-extended.webp" "$PET_DIR/spritesheet.webp"
jq -n --arg id "$PET_ID" --arg displayName "$DISPLAY_NAME" --arg description "$DESCRIPTION" \
  '{id: $id, displayName: $displayName, description: $description, spriteVersionNumber: 2, spritesheetPath: "spritesheet.webp"}' \
  > "$PET_DIR/pet.json"
```

`DATA_DIR` is the NusaShell data directory; the runtime context reports the
active path. Then write the run summary:

```bash
jq -n --arg run_dir "$RUN_DIR" \
  --arg spritesheet "$RUN_DIR/final/spritesheet-extended.webp" \
  --arg validation "$RUN_DIR/final/validation-extended.json" \
  --arg chroma_despill "$RUN_DIR/qa/chroma-despill-extended.json" \
  --arg contact_sheet "$RUN_DIR/qa/contact-sheet-extended.png" \
  --arg direction_sheet "$RUN_DIR/qa/look-directions.png" \
  --arg direction_semantics "$RUN_DIR/qa/direction-semantics.json" \
  --arg blind_direction_validation "$RUN_DIR/qa/direction-blind-validation.json" \
  --arg blind_review_resolution "$RUN_DIR/qa/blind-review-resolution.json" \
  --arg continuity "$RUN_DIR/qa/look-continuity.json" \
  --arg review "$RUN_DIR/qa/review.json" \
  --arg package "$PET_DIR" \
  '{ok: true, spriteVersionNumber: 2, run_dir: $run_dir, spritesheet: $spritesheet, validation: $validation, chroma_despill: $chroma_despill, contact_sheet: $contact_sheet, direction_sheet: $direction_sheet, direction_semantics: $direction_semantics, blind_direction_validation: $blind_direction_validation, blind_review_resolution: $blind_review_resolution, continuity: $continuity, review: $review, package: $package}' \
  > "$RUN_DIR/qa/run-summary.json"
```

## 12. Cleanup

Keep: `pet_request.json`, `final/spritesheet-extended.webp`,
`final/validation-extended.json`, `qa/chroma-despill-extended.json`,
`qa/contact-sheet-extended.png`, `qa/look-directions.png`,
`qa/direction-semantics.json`, `qa/direction-blind-pairs.png`,
`qa/direction-blind-answer-key.json`, `qa/direction-blind-verdicts.json`,
`qa/direction-blind-validation.json`, `qa/blind-review-resolution.json`
(when an override was used), `qa/look-continuity.json`, `qa/previews/`,
`qa/review.json`, `qa/run-summary.json`.

Remove (unless the user wants debug artifacts): `prompts/`, layout guides,
generated row strips, extracted frames, PNG intermediates, the 8x9
intermediate atlas, and `generation-jobs.json`.

## 13. Brand discovery (bare brand requests)

Run as a `delegate`/`subagent` worker with web access:

```text
Research a brand for hatch-pet mascot creation.

Brand/product/prospect: <brand name>
User context: <short user request>
Output file: <absolute path to brand-discovery.md>

Use web search (web_search / web_fetch). Prefer official brand, product,
docs, about, press, or brand pages. Use reputable secondary sources only if
official sources are too thin. Keep the search narrow: enough for visual and
personality cues, not a market-research brief. Write an adaptive markdown
brief to the output file covering identity/category, audience/use context,
visual system (palette, shapes, line quality, materials, iconography),
personality/tone, product/domain motifs, mascot translation cues, avoidances,
and evidence/confidence with source URLs. Mark inferred mascot guidance as
inference. Do not copy logos, readable marks, UI screenshots, slogans, or
text.

End the brief with a `Generation handoff` section containing exactly:
- brand_name=<canonical brand/product name>
- brand_brief=<one sentence, max 45 words, covering palette/tone/domain motifs/personality>
- avatar_seed=<short mascot-safe visual idea, no logo copying>
- avoid=<short comma-separated list>
- brand_sources=<comma-separated source URLs>

Return exactly:
brand_discovery_file=<absolute output file path>
brand_name=<canonical brand/product name>
brand_brief=<same compact sentence from Generation handoff>
avatar_seed=<same short seed from Generation handoff>
avoid=<same short avoid list from Generation handoff>
brand_sources=<same comma-separated URLs from Generation handoff>
```

Skip discovery when the user gives a concrete mascot description or reference
images. If web search is unavailable and only a bare brand name exists, ask
the user for brand cues before generating. Pass the brief to
`prepare_pet_run.py` via `--brand-discovery-file`, the seed via `--pet-notes`,
and each source URL via repeated `--brand-source`.
