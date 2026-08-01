#!/usr/bin/env node
/**
 * UI docs generator for NusaShell.
 *
 * Reads `resources/agent/docs/ui-source/ui-map.json` and writes
 * `resources/agent/docs/ui/*.md` for the `docs_*` agent tools.
 *
 * The script also validates that every `data-view` in
 * `apps/desktop/src/renderer/index.html` has an entry in the UI map
 * and that every mapped control ID exists in the renderer source.
 */
import { readFile, writeFile, readdir, rm, mkdir } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(__dirname, "..");
const UI_MAP_PATH = join(
  REPO_ROOT,
  "resources",
  "agent",
  "docs",
  "ui-source",
  "ui-map.json",
);
const OUT_DIR = join(REPO_ROOT, "resources", "agent", "docs", "ui");
const HTML_PATH = join(
  REPO_ROOT,
  "apps",
  "desktop",
  "src",
  "renderer",
  "index.html",
);
const JS_PATHS = [
  join(REPO_ROOT, "apps", "desktop", "src", "renderer", "launcher.js"),
  join(
    REPO_ROOT,
    "apps",
    "desktop",
    "src",
    "renderer",
    "agent-conversation-controller.js",
  ),
  join(
    REPO_ROOT,
    "apps",
    "desktop",
    "src",
    "renderer",
    "agent-conversation-ui.js",
  ),
  join(
    REPO_ROOT,
    "apps",
    "desktop",
    "src",
    "renderer",
    "launcher-ui.js",
  ),
  join(
    REPO_ROOT,
    "apps",
    "desktop",
    "src",
    "renderer",
    "ai-model-ui.js",
  ),
  join(
    REPO_ROOT,
    "apps",
    "desktop",
    "src",
    "renderer",
    "jobs-controller.js",
  ),
];

const viewRegex = /<section[^>]*\bclass="[^"]*view[^"]*"[^>]*\bdata-view="([^"]+)"/g;
const idRegexHtml = /\bid="([^"]+)"/g;

/**
 * @param {object} paths
 * @returns {Promise<{map: object, html: string, js: string[]}>}
 */
export async function loadInputs(
  paths = { uiMapPath: UI_MAP_PATH, htmlPath: HTML_PATH, jsPaths: JS_PATHS },
) {
  const { uiMapPath = UI_MAP_PATH, htmlPath = HTML_PATH, jsPaths = JS_PATHS } = paths;
  const [mapRaw, html, ...jsFiles] = await Promise.all([
    readFile(uiMapPath, "utf8"),
    readFile(htmlPath, "utf8"),
    ...jsPaths.map((p) => readFile(p, "utf8").catch(() => "")),
  ]);
  return {
    map: JSON.parse(mapRaw),
    html,
    js: jsFiles,
  };
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
  const idRegex = /(?:\$|querySelector(?:All)?)\s*\(\s*["']#([^"']+)["']\s*\)|getElementById\s*\(\s*["']([^"']+)["']\s*\)/g;
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
  const jsPaths = options.jsPaths ?? JS_PATHS;

  const { map, html, js } = await loadInputs({ uiMapPath, htmlPath, jsPaths });
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

  for (const [id, control] of Object.entries(controls || {})) {
    if (control.generated) continue;
    if (!sourceIds.has(id)) {
      errors.push(
        `ui-map.json control "${id}" is not found in the renderer source.`,
      );
    }
  }

  // Clean output directory of generated markdown files to avoid stale docs.
  await mkdir(outDir, { recursive: true });
  const existing = await readdir(outDir).catch(() => []);
  for (const f of existing) {
    if (f.endsWith(".md")) {
      await rm(join(outDir, f), { force: true });
    }
  }

  for (const [viewId, view] of Object.entries(views)) {
    const md = renderView(viewId, view, controls);
    await writeFile(join(outDir, `${viewId}.md`), md, "utf8");
  }

  if (errors.length) {
    console.error("UI docs validation failed:");
    for (const e of errors) console.error(`  - ${e}`);
    if (options.exitOnError !== false) {
      process.exit(1);
    }
    throw new Error(errors.join("\n"));
  }

  console.log(`Generated ${Object.keys(views).length} UI docs in ${outDir}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  generate().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
