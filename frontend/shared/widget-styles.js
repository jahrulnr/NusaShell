// Shadow-root <link> stylesheets reload every time a root is created or moved,
// which paints widgets unstyled for a frame or two. Constructed sheets are
// shared by reference and apply synchronously. They ignore @import, so each
// widget lists its dependencies explicitly in cascade order.
const sheets = new Map();
const sheetUrls = new Map();
// Fetched rule text per sheet URL. Constructed sheets belong to the document
// that created them, so foreign documents (the Document-PiP window) get their
// own sheet instances rebuilt from this cache instead of re-fetching.
const cssText = new Map();
const docSheets = new WeakMap();
const syncedSheets = new WeakSet();
const adoptedRoots = new WeakMap();

function sheetConstructor() {
  if (typeof globalThis.CSSStyleSheet === 'function') return globalThis.CSSStyleSheet;
  // jsdom exposes the constructor on the window, not as a Node global. Tests
  // that import this module after installing a document (the PWA mini-window
  // case) still need a realm-correct sheet.
  if (typeof globalThis.window?.CSSStyleSheet === 'function') return globalThis.window.CSSStyleSheet;
  throw new ReferenceError('CSSStyleSheet is not available');
}

function sheet(path) {
  const url = new URL(path, import.meta.url).href;
  if (!sheets.has(url)) {
    const StyleSheet = sheetConstructor();
    const target = new StyleSheet();
    sheets.set(url, target);
    sheetUrls.set(target, url);
  }
  return sheets.get(url);
}

const loadingLayer = sheet('../loading-layer.css?v=1');
const base = sheet('../widgets/widget-base.css?v=6');
const highlight = sheet('../vendor/highlightjs/github-dark.min.css');
const overlays = sheet('../overlays.css?v=5');

export const widgetStyles = Object.freeze({
  agent: Object.freeze([loadingLayer, base, highlight, overlays, sheet('../widgets/agent-message.css?v=12')]),
  user: Object.freeze([loadingLayer, base, overlays, sheet('../widgets/user-message.css?v=8')]),
  reasoning: Object.freeze([loadingLayer, base, sheet('../widgets/reasoning.css?v=8')]),
  tool: Object.freeze([loadingLayer, base, highlight, sheet('../widgets/tools/tool-widgets.css?v=20')]),
});

async function fetchStylesheet(url) {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`Widget stylesheet ${url} failed with ${response.status}`);
  return response.text();
}

let loaded = null;

export function widgetStylesLoaded(fetchText = fetchStylesheet) {
  loaded ||= Promise.all([...sheets].map(async ([url, target]) => {
    const text = await fetchText(url);
    cssText.set(url, text);
    await target.replace(text);
  }));
  return loaded;
}

function sheetForDocument(doc, url) {
  const home = sheets.get(url);
  if (!doc || doc === globalThis.document) return home;
  let byUrl = docSheets.get(doc);
  if (!byUrl) docSheets.set(doc, (byUrl = new Map()));
  let target = byUrl.get(url);
  if (!target) {
    // A constructed sheet belongs to the document that created it — a
    // source-document instance cannot be adopted across documents, so the
    // owner document's own realm constructor is the only valid source.
    const Ctor = doc.defaultView?.CSSStyleSheet;
    if (!Ctor) throw new Error(`Cannot construct a stylesheet for ${url}: document has no window`);
    byUrl.set(url, (target = new Ctor()));
  }
  const text = cssText.get(url);
  if (text !== undefined && !syncedSheets.has(target)) {
    target.replaceSync(text);
    syncedSheets.add(target);
  }
  return target;
}

export function adoptWidgetStyles(root, name) {
  adoptedRoots.set(root, name);
  const doc = root.ownerDocument;
  root.adoptedStyleSheets = widgetStyles[name].map((shared) => sheetForDocument(doc, sheetUrls.get(shared)));
}

// Cross-document moves (Document PiP) drop every adoptedStyleSheets entry, so
// pip.js re-adopts after moving #agent-thread/#agent-composer-stack in and
// back out. Roots register their style name in adoptWidgetStyles; the walk
// descends into open shadow roots so widgets nested inside widgets heal too.
// Idempotent: repeated calls rebuild the same per-document sheet instances.
export function readoptWidgetStyles(scope) {
  const visit = (root) => {
    const name = adoptedRoots.get(root);
    if (name) adoptWidgetStyles(root, name);
    for (const el of root.querySelectorAll?.('*') ?? []) {
      if (el.shadowRoot) visit(el.shadowRoot);
    }
  };
  if (scope) visit(scope.shadowRoot ?? scope);
}
