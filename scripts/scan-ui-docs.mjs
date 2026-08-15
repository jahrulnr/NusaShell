#!/usr/bin/env node
/**
 * UI docs generator for NusaShell Light (Go port).
 *
 * Reads `infrastructure/docs/corpus/ui-source/ui-map.json` and writes
 * `infrastructure/docs/corpus/ui-*.md` so the agent can surface them
 * through `docs_search` / `docs_read` (the corpus is embedded at build
 * time by `infrastructure/docs/docs.go`).
 *
 * The script also validates that every `data-view` in
 * `frontend/index.html` has an entry in the UI map and that every mapped
 * control ID exists in the frontend source (HTML or JS).
 *
 * Adapted from NusaShell Electron's `scripts/scan-ui-docs.mjs`.
 */
import { readFile, writeFile, readdir, rm, mkdir } from "node:fs/promises";
import { join, dirname, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(__dirname, "..");
const UI_MAP_PATH = join(
  REPO_ROOT,
  "infrastructure",
  "docs",
  "corpus",
  "ui-source",
  "ui-map.json",
);
const OUT_DIR = join(REPO_ROOT, "infrastructure", "docs", "corpus");
const HTML_PATH = join(REPO_ROOT, "frontend", "index.html");
const JS_GLOB = join(REPO_ROOT, "frontend", "js", "**", "*.js");

const viewRegex = /<section[^>]*\bclass="[^"]*view[^"]*"[^>]*\bdata-view="([^"]+)"/g;
const idRegexHtml = /\bid="([^"]+)"/g;

/**
 * @param {object} paths
 * @returns {Promise<{map: object, html: string, js: string[]}>}
 */
export async function loadInputs(
  paths = { uiMapPath: UI_MAP_PATH, htmlPath: HTML_PATH, jsGlob: JS_GLOB },
) {
  const {
    uiMapPath = UI_MAP_PATH,
    htmlPath = HTML_PATH,
    jsGlob = JS_GLOB,
  } = paths;
  const jsFiles = await collectJsFiles(jsGlob);
  const [mapRaw, html, ...jsContents] = await Promise.all([
    readFile(uiMapPath, "utf8"),
    readFile(htmlPath, "utf8"),
    ...jsFiles.map((p) => readFile(p, "utf8").catch(() => "")),
  ]);
  return {
    map: JSON.parse(mapRaw),
    html,
    js: jsContents,
    jsPaths: jsFiles,
  };
}

async function collectJsFiles(_globPattern) {
  return walkJs(join(REPO_ROOT, "frontend", "js"));
}

async function walkJs(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const e of entries) {
    const full = join(dir, e.name);
    if (e.isDirectory()) {
      files.push(...(await walkJs(full)));
    } else if (e.name.endsWith(".js")) {
      files.push(full);
    }
  }
  return files.sort();
}

export function collectViews(html) {
  const ids = new Set();
  let m;
  while ((m = viewRegex.exec(html)) !== null) {
    ids.add(m[1]);
  }
  return ids;
}

export function collectHtmlIds(html) {
  const ids = new Set();
  let m;
  while ((m = idRegexHtml.exec(html)) !== null) {
    ids.add(m[1]);
  }
  return ids;
}

export function collectJsIds(jsFiles) {
  const ids = new Set();
  // Only capture ID-based lookups: $("#id"), document.getElementById("id"),
  // document.querySelector("#id"), and document.querySelectorAll("#id").
  const idRegex =
    /(?:\$|querySelector(?:All)?)\s*\(\s*["']#([^"']+)["']\s*\)|getElementById\s*\(\s*["']([^"']+)["']\s*\)/g;
  for (const source of jsFiles) {
    let m;
    while ((m = idRegex.exec(source)) !== null) {
      ids.add(m[1] ?? m[2]);
    }
  }
  return ids;
}

export function controlRef(id, control) {
  if (control?.selector) return `\`${control.selector}\``;
  return `\`#${id}\``;
}

export function renderControl(id, control, allControls) {
  const label = control.label || control.title || id;
  const lines = [`- **${label}** (${controlRef(id, control)}):`];
  if (control.section) lines.push(`  - Section: ${control.section}`);
  if (control.type) lines.push(`  - Type: ${control.type}`);
  if (control.action) lines.push(`  - Action: ${control.action}`);
  if (control.shortcut) lines.push(`  - Shortcut: ${control.shortcut}`);
  if (control.related?.length) {
    const related = control.related
      .map((rid) => {
        const r = allControls[rid];
        return `${r?.label || r?.title || rid} (${controlRef(rid, r || {})})`;
      })
      .join(", ");
    lines.push(`  - Related: ${related}`);
  }
  if (control.notes) {
    lines.push(`  - Notes: ${control.notes}`);
  }
  return lines.join("\n");
}

export function renderView(id, view, controls) {
  const lines = [`# ${view.title || id}`, ""];
  if (view.purpose) lines.push(`${view.purpose}`, "");
  if (view.howToOpen) lines.push(`**How to open:** ${view.howToOpen}`, "");
  if (view.notes) lines.push(`${view.notes}`, "");

  for (const section of view.sections || []) {
    lines.push(`## ${section.heading}`, "");
    if (section.paragraphs) {
      for (const p of section.paragraphs) lines.push(`${p}`, "");
    }
    for (const controlId of section.controls || []) {
      const control = controls[controlId];
      if (control) {
        lines.push(renderControl(controlId, control, controls), "");
      } else {
        lines.push(`- **\`#${controlId}\`** (missing map entry)`, "");
      }
    }
  }
  return lines.join("\n").trim() + "\n";
}

export async function generate(options = {}) {
  const uiMapPath = options.uiMapPath ?? UI_MAP_PATH;
  const outDir = options.outDir ?? OUT_DIR;
  const htmlPath = options.htmlPath ?? HTML_PATH;
  const jsGlob = options.jsGlob ?? JS_GLOB;

  const { map, html, js, jsPaths } = await loadInputs({
    uiMapPath,
    htmlPath,
    jsGlob,
  });
  const { views, controls } = map;

  const htmlViews = collectViews(html);
  const htmlIds = collectHtmlIds(html);
  const jsIds = collectJsIds(js);
  const sourceIds = new Set([...htmlIds, ...jsIds]);

  const errors = [];

  for (const viewId of htmlViews) {
    if (!views[viewId]) {
      errors.push(
        `HTML has data-view="${viewId}" but ui-map.json has no view entry for it.`,
      );
    }
  }
  for (const [viewId, view] of Object.entries(views || {})) {
    if (view.chrome) continue; // chrome views (sidebar, titlebar) have no <section data-view>
    if (!htmlViews.has(viewId)) {
      errors.push(
        `ui-map.json has view "${viewId}" but HTML has no data-view="${viewId}" section.`,
      );
    }
  }

  for (const [id, control] of Object.entries(controls || {})) {
    if (control.generated) continue;
    if (!sourceIds.has(id)) {
      errors.push(
        `ui-map.json control "${id}" is not found in the frontend source.`,
      );
    }
  }

  // Clean output directory of generated UI markdown files to avoid stale docs.
  await mkdir(outDir, { recursive: true });
  const existing = await readdir(outDir).catch(() => []);
  for (const f of existing) {
    if (f.startsWith("ui-") && f.endsWith(".md")) {
      await rm(join(outDir, f), { force: true });
    }
  }

  let generated = 0;
  for (const [viewId, view] of Object.entries(views || {})) {
    const md = renderView(viewId, view, controls);
    await writeFile(join(outDir, `ui-${viewId}.md`), md, "utf8");
    generated++;
  }

  if (errors.length) {
    console.error("UI docs validation failed:");
    for (const e of errors) console.error(`  - ${e}`);
    if (options.exitOnError !== false) {
      process.exit(1);
    }
    throw new Error(errors.join("\n"));
  }

  const scannedJs = jsPaths.map((p) => relative(REPO_ROOT, p)).join(", ");
  console.log(
    `Generated ${generated} UI docs in ${relative(REPO_ROOT, outDir)} (scanned ${jsPaths.length} JS files: ${scannedJs})`,
  );
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  generate().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
