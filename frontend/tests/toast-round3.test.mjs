// Toast repair, round 3 — lead browser QA found `#toast-container` count 0 in
// index.html. js/ui.js toast() returns silently when the container is absent,
// so the blocked-popup feedback both plugin Open callers emit never rendered.
// These tests pin the production-parity mount (body-level .toast-container),
// the layer contract (toasts above every shell overlay), the real render +
// dismiss path through js/ui.js, and the two callers that rely on it.
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { basename, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { toast } from '../js/ui.js';

const root = new URL('../', import.meta.url);

async function source(path) {
  return readFile(new URL(path, root), 'utf8');
}

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'localStorage', 'navigator',
  ];
  const previous = new Map();
  for (const name of names) {
    previous.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
    Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
  }
  // jsdom without pretendToBeVisual has no rAF; the toast show-class only needs
  // the callback to fire.
  Object.defineProperty(globalThis, 'requestAnimationFrame', {
    configurable: true,
    value: (callback) => { callback(0); return 0; },
  });
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

// Every z-index declared by the shell's own stylesheets (vendor internals like
// CodeMirror live inside their component's own stacking context and do not
// compete with body-level layers). The toast layer must clear the maximum —
// its own declaration is stripped from the scan.
// Only sheets the live shell can load count: index.html links, another
// sheet's @import, or a JS-injected link (page/conversation/strips.js mounts
// strips.css itself). Promoted trees keep the retired view sheets under
// styles/ beside the ported five — unreferenced remnants render no elements
// and cannot bound the layer.
async function maxShellZIndex() {
  const rootPath = fileURLToPath(root);
  const files = [];
  const walk = async (dir, deep) => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) { if (deep) await walk(path, deep); }
      else if (entry.name.endsWith('.css')) files.push(path);
    }
  };
  // The shell's own sheets only: top-level sheets, the ported styles/ subtree,
  // and page-scoped sheets. vendor/ internals keep their own stacking contexts.
  await walk(rootPath, false);
  await walk(join(rootPath, 'styles'), true);
  await walk(join(rootPath, 'page'), true);
  // Reference corpus: markup, modules, and sheets — anything that can pull a
  // stylesheet into the document.
  const refs = [];
  const collect = async (dir, deep) => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) { if (deep && entry.name !== 'vendor') await collect(path, deep); }
      else if (/\.(html|css|js|mjs)$/.test(entry.name)) refs.push(await readFile(path, 'utf8'));
    }
  };
  await collect(rootPath, false);
  for (const dir of ['styles', 'page', 'js', 'shared', 'widgets']) {
    await collect(join(rootPath, dir), true);
  }
  const corpus = refs.join('\n');
  const bases = new Map();
  for (const file of files) {
    const base = basename(file);
    bases.set(base, (bases.get(base) ?? 0) + 1);
  }
  let max = 0;
  for (const file of files) {
    const rel = relative(rootPath, file);
    const base = basename(file);
    // styles/ sheets share basenames with the live root sheets, so a styles/
    // file is reachable only through its qualified path; other files may also
    // be reached by a unique basename (JS builds URLs like './strips.css').
    if (!corpus.includes(rel) && !(bases.get(base) === 1 && corpus.includes(base))) continue;
    const css = (await readFile(file, 'utf8')).replace(/\.toast-container\s*\{[^}]*\}/, '');
    for (const match of css.matchAll(/z-index:\s*(\d+)/g)) {
      max = Math.max(max, Number(match[1]));
    }
  }
  return max;
}

test('index.html mounts the production toast container once, at body level', async () => {
  const html = await source('index.html');
  const { document } = new JSDOM(html).window;

  const containers = document.querySelectorAll('#toast-container');
  assert.equal(containers.length, 1, 'exactly one #toast-container exists — toast() silently no-ops without it');
  const container = containers[0];
  assert.ok(container.classList.contains('toast-container'),
    'the element carries the .toast-container hook styles/modals.css positions');
  assert.equal(container.parentElement, document.body,
    'the container is a direct <body> child so it stacks at the root level like production');
});

test('the styled toast layer clears every shell overlay and stays tap-transparent between toasts', async () => {
  const html = await source('index.html');
  const css = await source('styles/modals.css');
  assert.match(html, /href="styles\/modals\.css\?v=\d+"/, 'index.html loads the toast stylesheet');

  const rule = css.match(/\.toast-container\s*\{([^}]*)\}/);
  assert.ok(rule, 'styles/modals.css positions .toast-container');
  assert.match(rule[1], /position:\s*fixed;/, 'the container is a fixed overlay layer');
  const zIndex = Number(rule[1].match(/z-index:\s*(\d+)/)?.[1]);
  const shellMax = await maxShellZIndex();
  assert.ok(zIndex > shellMax,
    `toast z-index ${zIndex} must clear the shell overlay max ${shellMax} (dialogs/select portals reach 1000-1100)`);
  assert.match(rule[1], /pointer-events:\s*none;/,
    'the strip between toasts never swallows page taps');
  const toastRule = css.match(/\.toast\s*\{([^}]*)\}/);
  assert.match(toastRule[1], /pointer-events:\s*auto;/, 'the toast itself stays clickable for dismissal');
  assert.match(toastRule[1], /max-width:\s*min\(320px,\s*100%\);/, 'the toast stays bounded on narrow viewports');
  assert.match(css, /\.toast-dismiss\s*\{/, 'the dismiss button is styled');
});

test('toast() renders the styled toast into the container and the dismiss button removes it', async () => {
  const html = await source('index.html');
  const dom = new JSDOM(html, { url: 'http://127.0.0.1:8766/' });
  const restore = installDOMGlobals(dom.window);
  try {
    toast('Allow pop-ups to open “Kanban”.', 'error');
    const container = dom.window.document.getElementById('toast-container');
    const node = container?.querySelector('.toast.toast-error');
    assert.ok(node, 'an error toast renders inside #toast-container');
    assert.equal(node.getAttribute('role'), 'status');
    assert.equal(node.querySelector('.toast-message')?.textContent, 'Allow pop-ups to open “Kanban”.');
    const dismiss = node.querySelector('.toast-dismiss');
    assert.ok(dismiss, 'the toast carries its dismiss button');
    assert.ok(node.classList.contains('toast-show'), 'the toast transitions in');

    dismiss.click();
    await new Promise((resolve) => setTimeout(resolve, 420));
    assert.equal(node.isConnected, false, 'dismiss removes the toast for pointer and touch alike');
  } finally {
    restore();
  }
});

test('both plugin Open callers keep their blocked-popup toast feedback', async () => {
  const conversation = await source('page/conversation/app.js');
  const homeOpen = conversation.slice(
    conversation.indexOf('function openHomeMCPApp('),
    conversation.indexOf('// Launching a UI is the explicit user intent'),
  );
  assert.match(homeOpen, /if \(!opened\)\s*\{\s*toast\(`Allow pop-ups to open/, 'Home card toasts when window.open is blocked');

  const plugins = await source('page/plugins/app.js');
  const selectedOpen = plugins.slice(
    plugins.indexOf('function openSelectedUI('),
    plugins.indexOf('void connectPlugin(plugin);'),
  );
  assert.match(selectedOpen, /if \(!opened\) toast\(`Allow pop-ups to open/, 'Plugins Open UI toasts when window.open is blocked');
});
