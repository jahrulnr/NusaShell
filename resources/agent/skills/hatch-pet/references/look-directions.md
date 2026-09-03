# Look-Direction Stage (rows 9-10)

Mandatory for every new pet. Read this whole file before generating any look
row. Rows 9-10 are two coherently synthesized 8-frame strips, not 16 unrelated
variants.

## Sequence

1. After standard-row QA passes, write `qa/look-mechanics.md` for this pet.
2. Generate the four-cardinal strip as one image, extract with
   `extract_cardinal_anchors.py`, and approve all four anchors semantically at
   final pet size. `090` must point toward the viewer's screen-right edge and
   `270` toward the viewer's screen-left edge; for faces, nose tip and pupils
   must cross to the corresponding side of the head center. If one cardinal is
   ambiguous, regenerate only that anchor from `prompts/look-anchor-repairs/<degree>.md`
   and re-run `compose_cardinal_anchor_strip.py`.
3. Generate row 9 (`000 -> 090 -> 180` arc) as one coherent family from the
   approved cardinals, interpolating intermediates as even 22.5-degree steps.
   Register and edge-check it immediately with `assemble_extended_atlas.py`
   (`--look-row-9` mode), inspect the eight registered cells in order, and fix
   hard failures by resynthesizing the complete row.
4. Only after row 9 passes deterministic registration and labeled review,
   generate row 10 (`180 -> 270 -> 000` arc), giving it both the approved
   cardinal strip and completed row 9 as continuity evidence. `337.5` must
   land one step before row 9's `000`; `157.5 -> 180` must continue smoothly
   across the row boundary.
5. Assemble the extended atlas, despill once, validate, and run the QA sheets
   and blind review (see the skill's workflow and `references/pipeline.md`).

Direction order is fixed:

```text
row 9:  000, 022.5, 045, 067.5, 090, 112.5, 135, 157.5
row 10: 180, 202.5, 225, 247.5, 270, 292.5, 315, 337.5
```

## Look mechanics decision (`qa/look-mechanics.md`)

First ask: what is the best natural motion for this character when looking
around? Name what stays anchored, what leads the gaze, what follows, and what
bends, shifts, turns, squashes, stretches, or deforms. Decide eyes and props:
physical eyeballs rotate as whole globes; flat screen/sticker eyes change
drawn features on a fixed surface; props stay stable, lag slightly, or move
with the body.

Define a motion budget: each 22.5-degree step moves the same parts by roughly
the same visual amount, unless the decision explicitly calls out an asymmetry.

Grounded mechanics by pet type (adapt, don't force):

- Physical eyeballs: rotate the whole eye globe (sclera, iris, pupil, lid,
  highlight together). Never slide only the iris/pupil on a fixed white.
- Screen/printed eyes: body-locked with feature motion on the fixed surface.
- Separate head: eye movement plus head turn/tilt with ear/fur/upper-body
  follow-through and a stable torso.
- Wire/flexible mascots: feet/base anchored, upper loop or face bends toward
  the target, props stable or lagging subtly.
- Blobs: base anchored, face/head stretches subtly toward the target.
- Rigid objects: lean, neck/tip aim, hinge, yaw, pitch, bend, vibration.
  Do not default to whole-object turntable rotation; preserve the object's
  primary readable face (display, label side, iconic angle) unless the user
  asked for turntable behavior.
- Humanoids: eyes lead (eye, eyelid, eyebrow participation), then head/neck
  and restrained upper-body follow-through. No broad raster warps that
  stretch skull, brows, mouth, hoodie, hands, or held props. A row where the
  head moves but the eyes stay locked in one expression is failed.
- Props: infer each prop's physical constraints before generating (anchored
  where, rigid or flexible, leads or lags, how it occludes). Props near the
  face may turn side-on or be partly hidden; hand tools may swing or lag;
  worn props follow the body; cords arc continuously. Never keep the prop in
  the same front-facing relationship across all 16 cells.

## Hard rules

- Never use whole-sprite rotation, skewing, or affine tilt to fake gaze,
  unless the pet is literally a rotating rigid object and the mechanics
  decision says so.
- Preserve the pet's original eye design. No googly eyes, replacement whites,
  floating pupils, second eye layers, or procedural pupil compositing (except
  clipped to the original aperture and visibly inside the head silhouette in
  every direction). If the original eye design cannot be preserved cleanly,
  regenerate the whole look cell.
- Do not mirror, re-center, or independently regenerate adjacent direction
  cells in a way that changes body registration. Keep a stable anchor (feet/
  base/torso) and let only the intended look mechanics change around it.
- Every look cell must be visually distinguishable from the neutral/rest
  frame at final pet size. A cell that reads as front-facing/idle is failed.
- Cardinals must be semantically unmistakable at final pet size: `000` up,
  `090` screen-right, `180` down, `270` screen-left. Eyeless pets must carry
  direction through head, face surface, eyelids, antennae, ears, or body bend.
- Adjacent cells must move continuously: anchored parts never jump, flip
  sides, or teleport; lateral motion progresses gradually through the arc,
  including `157.5 -> 180` and `337.5 -> 000`.
- Define directions in viewer/screen coordinates, never character-relative.
- Keep motion subtle: preserve volume, baseline, silhouette readability,
  identity, and material believability.
- No labels, degree text, arrows, clocks, guide marks, shadows, glows,
  scenery, or detached effects.

## Direction acceptance policy

Hard failures (require resynthesizing the complete containing row):

- a cardinal anchor is wrong or ambiguous, or blind cardinal classification
  contradicts/cannot confirm the expected axis
- labeled normal-size review confirms an intermediate pose in the wrong
  principal quadrant, a reversed loop, or a badly lost axis
- the ordered loop visibly reverses, backtracks, or contains a conspicuous
  snap, scale pop, identity change, registration jump, or broken attachment
- deterministic structural failure; confirmed clipping, accidental transparent
  interior hole, seam band, replacement eyes, or materially broken sprite
- whole-sprite rotation/deformation/eye mechanics visibly break identity

Warnings (do not require regeneration by themselves):

- an intermediate pose is similar to a neighbor, a diagonal cue is subtle, or
  the pet uses less body movement than the ideal mechanics plan
- blind reviewers disagree or return `ambiguous` on an intermediate while
  labeled normal-size review confirms the intended direction and coherent loop
- continuity metrics warn without a visible snap, pop, seam, or broken
  silhouette

## Scale and registration

Look cells must keep the same practical scale and body registration as the
neutral/default pet: no noticeably larger neutral, no floating look cells, no
left/right sliding within the cell. Extended assembly recovers pose groups
from the original-resolution row, computes one shared scale from height plus
left/right extents around the shared lower-body anchor, resizes each crop
exactly once, and never enlarges an already-resampled cell. Pass
`--neutral-cell` (the approved idle frame) whenever available. If the focused
QA sheet still shows scale or placement drift, repair before packaging.

Look rows must have transparent backgrounds after assembly. Never accept
chroma-key panels behind look cells; if lighting variation leaks, rerun
assembly with a wider `--chroma-threshold` instead of packaging opaque key
color. Validation must pass without opaque chroma-key-pixel errors.

## Blind review severity resolution

After an independent blind or final QA `fail`:

1. Read the worker's semantic reasons, repair note, labeled direction sheet,
   `qa/direction-semantics.json`, and `qa/look-continuity.json` first.
2. Classify the failure:
   - `major`: wrong/ambiguous cardinal; labeled wrong principal quadrant or
     visible reversal; conspicuous snap, scale pop, identity change, broken
     attachment, clipping, interior seam/hole, or deterministic failure.
   - `minor`: exact pupil/nose placement differs from the numerical ideal;
     subtle near-vertical cue; isolated reviewer disagreement or `ambiguous`;
     intermediate blind majority conflict while the labeled loop reads
     correctly; metric warnings without a visible defect.
3. Major failures require repair. Minor failures may be overridden and
   packaging continues.
4. Record every override in `qa/blind-review-resolution.json` with
   `decision: "accept"`, `severity: "minor"`, the failed checks, the
   labeled/continuity evidence, and `reviewed_by: "parent"` or `"user"`.
   Never override a major failure.
