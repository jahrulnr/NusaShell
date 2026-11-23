# Vendored dependencies

Everything the WebUI loads is served from this directory — no CDN, no npm
install, no build step. Each file is copied verbatim from the published npm
tarball (`npm pack <pkg>@<version>`); only trailing `sourceMappingURL`
comments are stripped.

| Dir | Package | Version | License | File(s) |
|---|---|---|---|---|
| `preact/` | `htm` (bundles Preact + hooks) | 3.1.1 | Apache-2.0 | `preact/standalone.module.js` → `htm-preact.mjs` |
| `marked/` | `marked` | 18.2.1 | MIT | `lib/marked.esm.js` |
| `dompurify/` | `dompurify` | 3.4.18 | MPL-2.0 OR Apache-2.0 | `dist/purify.es.mjs` |
| `highlight/` | `@highlightjs/cdn-assets` | 11.12.0 | BSD-3-Clause | `es/highlight.min.js` → `highlight.min.mjs` (common languages) |
| `tailwind/` | `@tailwindcss/browser` | 4.3.5 | MIT | `dist/index.global.js` → `tailwind.browser.js` |
| `slim-select/` | `slim-select` | 4.7.0 | MIT | `dist/slimselect.es.js`, `dist/slimselect.css` |
| `pele/` | `pele` | 1.13.0 | MIT | `dist/pele.global.js` (registers `globalThis.pele`) |
| `xterm/` | `@xterm/xterm` | 6.0.0 | MIT | `lib/xterm.js` (registers `globalThis.Terminal`), `css/xterm.css`; loaded when a terminal opens |
| `xterm/` | `@xterm/addon-web-links` | 0.12.0 | MIT | `lib/addon-web-links.js` (registers `globalThis.WebLinksAddon`), `LICENSE-web-links` |
| `fonts/` | Inter v20, Tinos v26 (latin subsets, copied from NusaMation's bundled files) | OFL-1.1 (Inter), Apache-2.0 (Tinos) | `inter-v20-latin-{regular,500,600,700}.woff2`, `tinos-v26-latin-{regular,italic,700,700italic}.woff2`, `fonts.css` (our own `@font-face` and the `data-font` choices) |

## Updating

1. `npm pack <pkg>@<version>` in a scratch directory and check
   `npm view <pkg>@<version> dist.integrity` against the tarball.
2. Replace the file(s) above, keep `LICENSE`, bump the version in this table.
3. Reload the UI and exercise the feature that uses it.
