# Artifacts

Artifacts are interactive HTML/CSS/JS documents the agent produces via
`artifact_create` / `artifact_update`. They render in a sandboxed iframe
in the UI, so the agent can ship prototypes, minigames, dashboards,
simulations, calculators, and rich visualizations that mermaid diagrams
and tables cannot express.

## When to use

| Need | Tool |
|---|---|
| Compare options, list properties, structured data | Markdown table |
| Workflow, state transition, architecture, relationships | Mermaid diagram |
| Interactive prototype, minigame, dashboard, simulation, calculator | `artifact_create` |

Use `artifact_create` only when the content is genuinely interactive or
requires custom HTML/CSS/JS. For static structure, a table or mermaid
diagram is cheaper and renders inline.

## Tools

- `artifact_create(html, css?, js?, title?, width, height)` — create a
  new artifact. `width` and `height` are **required** (pixels) and
  control the iframe viewport size. Returns
  `{ "artifact": { id, title, html, css, js, width, height } }`. The UI
  renders it as a card; the user clicks to open the iframe.
- `artifact_update(id, html?, css?, js?, title?)` — partial update. Only
  the fields you pass are replaced; omitted fields keep their current
  value. Use this for small edits instead of re-outputting the whole
  artifact. Width and height are preserved from the original.
- `artifact_read(id)` — read an artifact's full content.
- `artifact_list()` — list artifacts in the current conversation.
- `artifact_delete(id)` — delete an artifact.

## Sizing guide

`width` and `height` are required on `artifact_create`. The iframe fills
the thread width (CSS `width: 100%`), so `width` controls the max
rendering width and `height` controls the visible viewport. Pick the
smallest size that comfortably fits the content — too small crops the
view, too large dominates the thread.

| Content type | width | height | Notes |
|---|---|---|---|
| Prototype / minigame | 640 | 480 | Classic 4:3, fits most game loops |
| Dashboard / charts | 720 | 400 | Wider for side-by-side panels |
| Widget / calculator | 360 | 480 | Narrow, phone-like |
| Timeline / list | 640 | 600 | Tall scrollable content |
| Full-page demo | 800 | 600 | When the artifact is the main output |

When in doubt, use 640x480.

## Token budget

Max 64k tokens total per artifact (html + css + js). To stay within
budget, prefer reusing CDNs and vendor libraries over inlining large
libraries:

    <script src="https://cdn.jsdelivr.net/npm/three@0.160.0/build/three.min.js"></script>

NusaShell's bundled vendors (mermaid, chartjs, vis-network) are also
available at `/vendor/...` paths inside the iframe.

## Good examples

    artifact_create(
      html="<canvas id='c'></canvas>",
      js="const c=document.getElementById('c');/* minigame loop */",
      title="Pong prototype",
      width=640, height=480,
    )

    artifact_create(
      html="<div id='chart'></div>",
      js="/* chartjs render */",
      title="Sales dashboard",
      width=720, height=400,
    )

    artifact_update(id="art_01J…", js="/* fix: ball speed */")

## What not to do

- Don't use `artifact_create` for static diagrams — use mermaid.
- Don't re-output the whole artifact for a small edit — use
  `artifact_update` with only the changed field.
- Don't inline large libraries (three.js, d3, etc.) — use a CDN `<script
  src>` tag instead.
