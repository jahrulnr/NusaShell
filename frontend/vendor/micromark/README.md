# micromark vendor bundle

Bundled from:
- micromark@3.x (CommonMark parser with positional info)
- micromark-extension-gfm@3.x (GitHub Flavored Markdown)

## Rebuild

```bash
npm install micromark@3 micromark-extension-gfm@3
npx esbuild entry-nusashell.mjs --bundle --format=esm --outfile=micromark.mjs --minify
# Patch: guard document access for Node test compatibility
sed -i 's|var <varname>=document.createElement("i")|var <varname>=typeof document!=="undefined"?document.createElement("i"):{innerHTML:""}|' micromark.mjs
```

## Exports

- `micromark(value, options)` — high-level markdown → HTML
- `parse(options)` — low-level parser (returns document writer)
- `postprocess(events)` — postprocess parse events
- `preprocess()` — preprocess input for parser
- `gfm()` — GFM syntax extension
- `gfmHtml()` — GFM HTML extension

## Patch

The `document.createElement("i")` call in `decode-named-character-reference`
is guarded with `typeof document !== "undefined"` so the bundle works in
Node.js test environments (where `document` is not defined at module load).
