---
name: hatch-pet
description: Hatch, repair, validate, visually QA, and package NusaShell desktop pets on the hatch-pet v2 atlas contract (8x11 spritesheet, 192x208 cells, 1536x2288, 16 look directions, spriteVersionNumber 2) consumed by apps/pets. Use when the user asks to create or hatch a pet, mascot, or custom animated character for NusaShell, make a pet from a brand/company name or reference image, fix or upgrade an existing pet atlas, or run the deterministic pet QA/packaging pipeline.
metadata:
  version: "1"
---

# Hatch Pet (NusaShell)

Ported from the Apache-2.0 Codex `hatch-pet` skill. The bundled `scripts/` are
the upstream deterministic pipeline, unchanged except tooling renames. The
atlas contract is shared: `apps/pets/internal/char/atlas.go` loads exactly this
format (`DefaultAtlasLayout` maps NusaShell states onto hatch-pet v2 rows).

## Overview

Hatch a NusaShell pet from a concept, brand name, or reference images. Every
newly hatched pet is an 8x11 atlas (9 standard animation rows + 16 clockwise
look directions), packaged with `spriteVersionNumber: 2`. The intermediate 8x9
atlas exists only to assemble and review rows 0-8; never package it.

User inputs are optional. Infer name and description from the concept, brand,
or reference filenames when omitted. Without reference images, generate the
base pet from text first, then use that base as the canonical reference for
every row.

## Target contract and NusaShell state mapping

Full geometry, packaging shape, and row/frame counts: read
`references/v2-contract.md` before your first run.

| NusaShell state | Atlas row (hatch-pet state) | Frames | Playback |
| --- | --- | --- | --- |
| `idle` | 0 idle | 6 | loop |
| `thinking` | 7 running (task work) | 6 | loop |
| `reasoning` | 8 review | 6 | loop |
| `done` | 3 waving | 4 | play once |
| `error` | 5 failed | 8 | play once |
| `waiting` | 6 waiting | 6 | loop |
| hover gaze | 9-10 (16 look directions) | 8+8 | clockwise |

Rows 1 (`running-right`), 2 (`running-left`), and 4 (`jumping`) are not mapped
by the current runtime but are still mandatory: atlas geometry is validated as
exactly 1536x2288 and every row must be complete.

## Tooling

- **Visual generation**: the `generate_media` tool with `media_type="image"`.
  Attach grounding images with `referenced_image_paths` (max 5). The result
  gives the saved output path; copy that exact file into the run folder as
  instructed. Never call image APIs or raster-generate strips in code.
- **Deterministic image work**: `exec` with `python3`. Before running any
  script, check Pillow: `python3 -c "import PIL"`. If it is missing, install
  it (`pip install pillow`) or stop and tell the user the run needs Pillow.
- **Skill scripts**: resolve the seeded skill directory with
  `skill(op="search", query="hatch pet")`, then `file_list` its folder; the
  scripts live under `scripts/`. Pass the directory as `SKILL_DIR` in commands.
- **Isolated review**: `subagent` (ACP: Codex/Cursor/Devin) or `delegate` for
  blind direction QA and final visual QA. If no subagent is available, ask the
  user to inspect the QA sheets instead; never self-approve a repaired look
  row.
- **Brand discovery**: `web_search` + `web_fetch` when the user gives only a
  brand/company/product name (see `references/pipeline.md`).

## Boundaries

- `generate_media` is the only visual generation layer. Bundled scripts only
  process already-generated outputs; never write helpers that populate row
  outputs.
- Only the `base` job may be prompt-only. Every row job must attach the images
  listed in `generation-jobs.json` (`input_images`), including the canonical
  base and its layout guide. A row generated without grounding images is
  invalid.
- Keep one identity: same face, palette, materials, proportions, props, and
  silhouette across all 11 rows. Identity drift is a blocker even when
  deterministic validation passes.
- Chroma cleanup happens exactly once, on the final 8x11 atlas
  (`despill_chroma_edges.py`). Never regenerate imagery or add cleanup passes
  after that report has `ok: true` and atlas validation passes.
- Every visual effect must be attached to the pet and opaque. No detached
  sparkles, shadows, speed lines, wave marks, text, or chroma-key-adjacent
  colors. Full policy: `references/look-directions.md`.

## Visible progress plan

Keep a checklist for the user, one step active at a time, replacing `<Pet>`:

1. Getting `<Pet>` ready. (confirm name, description, style, working folder)
2. Imagining `<Pet>`'s main look. (base reference image)
3. Picturing `<Pet>`'s poses. (rows 0-8 approved, look mechanics written, rows 9-10)
4. Hatching `<Pet>`. (8x11 atlas, QA, package v2, report paths)

Only mark a step complete when the real file or decision exists. For a repair
run, start from the first relevant step.

## Workflow

Full command sequences for every step: `references/pipeline.md`. Worker prompts
for isolated review: `references/workers.md`.

1. **Prepare the run.** Run `scripts/prepare_pet_run.py` with the pet name,
   description, optional reference image, and output directory. It creates
   `pet_request.json`, `generation-jobs.json`, prompts, and layout guides.
   Read the manifest to see ready jobs (a job is ready when its `depends_on`
   are all complete). For a bare brand request, run web brand discovery first
   and pass the brief through `--brand-discovery-file`.
2. **Generate the base.** One `generate_media` call with the base prompt and
   the user's reference images. Copy the output to the manifest's
   `output_path`, then create `references/canonical-base.png` from it.
3. **Generate and validate rows 0-8.** Generate `idle` and `running-right`
   next (identity + gait check), then the remaining rows. Every row: generate
   with its prompt + layout guide + canonical base attached, copy into
   `decoded/`, then immediately `extract_strip_frames.py` +
   `inspect_frames.py` for that row. Fix or regenerate the row on error before
   continuing. Mirror `running-left` with
   `derive_running_left_from_running_right.py --confirm-appropriate-mirror`
   only after approving `running-right`; otherwise generate it normally.
4. **Review the intermediate atlas.** Extract all frames, compose the 8x9
   atlas, make the contact sheet and per-row GIF previews, and inspect them
   (or send to a QA worker). Block progress on identity drift, pops, reversed
   cadence, wrong facing, or inert idle loops.
5. **Look-direction stage.** Mandatory for every new pet. Write the pet's
   look-mechanics decision, generate and approve the four-cardinal strip
   (000 up, 090 screen-right, 180 down, 270 screen-left), then synthesize row
   9, register and QA it, then row 10. Assemble the extended 8x11 atlas, run
   the single despill pass, validate with `--require-v2`, and run labeled
   semantic review, continuity measurement, and the three-isolated-worker
   blind axis QA. Read `references/look-directions.md` before starting this
   stage; its acceptance policy is not optional.
6. **Package v2.** Copy `final/spritesheet-extended.webp` to
   `<datadir>/pets/<pet-id>/spritesheet.webp` with the manifest from
   `references/v2-contract.md`, write `qa/run-summary.json`, then clean up
   intermediates (prompts, guides, row strips, frames, PNG intermediates,
   generation-jobs manifest) unless the user wants debug artifacts. Keep the
   QA artifacts listed in the acceptance criteria.

Repair rule: regenerate the smallest failing scope (one standard row or one
complete coherent look row) and re-run every downstream gate. Never mix an
individually generated repair cell into a look row.

## Convergence

Target a normal run within 30 minutes: prep 2, base 3, standard rows 10, look
directions 8, QA + packaging 5, buffer 2. After every failed attempt classify
the failure, change the root condition, and compare against the previous
result; the same root failure twice means change strategy, not prompt. The
time target never waives a QA gate.

## Acceptance criteria

- Final atlas exactly 1536x2288 (8 columns x 11 rows of 192x208), PNG or WebP.
- `pet.json` has `spriteVersionNumber: 2`; despill report `ok: true`;
  `validate_atlas.py --require-v2` passes with the run's chroma key.
- Used cells non-empty; unused standard-row cells fully transparent; no
  transparent interior holes or seam bands inside silhouettes.
- `qa/review.json` has no errors; contact sheet, previews, and the focused
  16-direction sheet were produced and inspected by an independent reviewer.
- Blind axis validation `qa/direction-blind-validation.json` has `ok: true`,
  or the failure is recorded as an accepted minor override in
  `qa/blind-review-resolution.json`. Wrong or ambiguous cardinals are always
  major and block packaging.
- Every direction has an explicit pass/warning/fail verdict with landmark
  evidence in `qa/direction-semantics.json`; no fail verdicts remain.
- Full rubric: `references/qa-rubric.md`. Do not package until every section
  passes.
