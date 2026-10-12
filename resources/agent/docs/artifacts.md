# Artifacts

Artifacts are interactive HTML documents the agent renders in a sandboxed
iframe in the UI — prototypes, minigames, dashboards, simulations,
calculators, and rich visualizations that mermaid diagrams and tables
cannot express.

The workflow is **file-based**: write the HTML to disk with `file_write`,
then display it with `show(op="html", path=...)`. Edit with `file_patch`,
inspect with `file_read`. There is no separate artifact store — the file
on disk is the single source of truth.

## When to use

| Need | Tool |
|---|---|
| Compare options, list properties, structured data | Markdown table |
| Workflow, state transition, architecture, relationships | Mermaid diagram |
| Interactive prototype, minigame, dashboard, simulation, calculator | `file_write` + `show(op="html")` |

Use `show(op="html")` only when the content is genuinely interactive or
requires custom HTML/CSS/JS. For static structure, a table or mermaid
diagram is cheaper and renders inline.

## Workflow

1. `file_write(path="/abs/path/game.html", content="<!DOCTYPE html>...")` —
   write the full HTML document to disk. Inline `<style>` and `<script>`
   tags, or link CDNs.
2. `show(op="html", path="/abs/path/game.html", height=480)` —
   render it in a sandboxed iframe in the UI.
3. To edit: `file_read(path=...)` to inspect, then `file_patch(path=...,
   old_string="...", new_string="...")` for targeted edits, then `show`
   again to re-render.
4. To inspect: `file_read(path=...)`.
5. To delete: `file_delete(path=...)`.

`show` returns `{ "artifact": { path, width, height, title } }` — metadata
only, never the HTML body. The card mounts the iframe lazily on user click;
the frontend fetches the file from disk at that point.

## Sizing guide

`height` controls the iframe viewport height (html only, default 400).
`width` is accepted but visually inert — the card CSS always renders the
iframe at `width: 100%`, filling the thread width. Pick the smallest height
that comfortably fits the content.

| Content type | height | Notes |
|---|---|---|
| Prototype / minigame | 480 | Classic 4:3 feel, fits most game loops |
| Dashboard / charts | 400 | Side-by-side panels |
| Widget / calculator | 480 | Phone-like height |
| Timeline / list | 600 | Tall scrollable content |
| Full-page demo | 600 | When the artifact is the main output |

When in doubt, use `height=480`.

## Token budget

Max 256KB per HTML file (≈ 64k tokens). To stay within budget, prefer
reusing CDNs and vendor libraries over inlining large libraries:

    <script src="https://cdn.jsdelivr.net/npm/three@0.160.0/build/three.min.js"></script>

NusaShell's bundled vendors (mermaid, chartjs, vis-network) are also
available at `/vendor/...` paths inside the iframe.

## Good examples

    file_write(
      path="/tmp/pong.html",
      content="<!DOCTYPE html><html><body><canvas id='c'></canvas><script>/* minigame loop */</script></body></html>",
    )
    show(op="html", path="/tmp/pong.html", height=480)

    file_write(
      path="/tmp/dashboard.html",
      content="<!DOCTYPE html><html><body><div id='chart'></div><script src='https://cdn.jsdelivr.net/npm/chart.js'></script><script>/* render */</script></body></html>",
    )
    show(op="html", path="/tmp/dashboard.html", height=400)

    # Targeted edit after reading the file:
    file_patch(path="/tmp/pong.html", old_string="ballSpeed=2", new_string="ballSpeed=3")
    show(op="html", path="/tmp/pong.html", height=480)

## Images

For static images (PNG, JPEG, WebP, GIF), use `show(op="image",
path="/abs/path/image.png")`. The image stays on disk — the tool result is
metadata only and the frontend renders it inline.
No width/height needed — the image renders at its
natural size.

## What not to do

- Don't use `show(op="html")` for static diagrams — use mermaid.
- Don't re-output the whole file for a small edit — use `file_patch`
  with only the changed substring.
- Don't inline large libraries (three.js, d3, etc.) — use a CDN
  `<script src>` tag instead.
- Don't forget to `file_write` before `show` — `show` only reads from
  disk, it does not create files.
