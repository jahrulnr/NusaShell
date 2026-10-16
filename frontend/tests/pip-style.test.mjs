import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const root = new URL('../', import.meta.url);

// The shared-styles registry lives inside the module instance: every importer
// must pin the same versioned specifier or the widgets and the PiP healer end
// up in two registries and re-adoption silently heals zero roots. Tests import
// the exact specifier the source uses instead of hardcoding a version that
// goes stale on the next cache-bust.
async function sharedStylesImport(importer) {
  const importerURL = new URL(importer, root);
  const source = await readFile(importerURL, 'utf8');
  const specifier = source.match(/from\s+'([^']*shared\/widget-styles\.js[^']*)'/)?.[1];
  assert.ok(specifier, `${importer} imports shared/widget-styles.js`);
  return import(new URL(specifier, importerURL).href);
}

// Document-PiP regression: moving a shadow host across documents makes the
// browser drop every adoptedStyleSheets entry, and sheets constructed in the
// source document cannot be adopted by the destination document anyway. The
// fix must rebuild sheets owned by the root's CURRENT ownerDocument from the
// cached source text — on entry, on restore, for nested shadow roots, and for
// widgets created while the PiP window is open. jsdom keeps the sheet array
// on adoption, so tests clear it manually to mirror the verified Chromium
// behavior.

function installDOMGlobals(window) {
  const keys = ['window', 'document', 'HTMLElement', 'customElements', 'CustomEvent', 'CSSStyleSheet'];
  const previous = Object.fromEntries(keys.map((key) => [key, globalThis[key]]));
  for (const key of keys) globalThis[key] = window[key];
  return previous;
}

function restoreDOMGlobals(previous) {
  for (const [key, value] of Object.entries(previous)) {
    if (value === undefined) delete globalThis[key];
    else globalThis[key] = value;
  }
}

function shadowHost(doc, name, adopt, tag = 'div') {
  const host = doc.createElement(tag);
  const shadow = host.attachShadow({ mode: 'open' });
  adopt(shadow, name);
  return { host, shadow };
}

test('adoptWidgetStyles re-adopts sheets owned by the root current ownerDocument', async () => {
  const main = new JSDOM('<body><div id="wrap"></div></body>', { url: 'http://localhost/' });
  const pip = new JSDOM('<body></body>', { url: 'about:blank' });
  const previous = installDOMGlobals(main.window);
  try {
    const styles = await import(`../shared/widget-styles.js?pip=${Date.now()}`);
    const { adoptWidgetStyles, widgetStyles, widgetStylesLoaded } = styles;
    let fetches = 0;
    await widgetStylesLoaded(async (url) => {
      fetches += 1;
      return `/* ${new URL(url).pathname.split('/').pop()} */ .x { color: red; }`;
    });

    const { host, shadow } = shadowHost(document, 'user', adoptWidgetStyles);
    const wrap = document.getElementById('wrap');
    wrap.append(host);
    widgetStyles.user.forEach((sheet, index) => assert.equal(shadow.adoptedStyleSheets[index], sheet));

    // Chromium empties the array on cross-document moves; jsdom does not.
    shadow.adoptedStyleSheets = [];
    pip.window.document.body.append(wrap);
    assert.equal(shadow.ownerDocument, pip.window.document);

    adoptWidgetStyles(shadow, 'user');
    assert.equal(shadow.adoptedStyleSheets.length, widgetStyles.user.length);
    shadow.adoptedStyleSheets.forEach((sheet, index) => {
      assert.notEqual(sheet, widgetStyles.user[index], 'foreign sheets must not be the source-document instances');
      assert.ok(sheet instanceof pip.window.CSSStyleSheet, 'sheets are constructed by the new ownerDocument realm');
      assert.match(sheet.cssRules[0].cssText, /color:\s*red/, 'sheets rebuild from the cached source text');
    });
    assert.equal(fetches, new Set(Object.values(widgetStyles).flat()).size, 're-adopt reuses cached text — no refetch');

    // Moving back restores the original shared sheet objects, and every
    // further adopt call is a stable no-op for the same document.
    shadow.adoptedStyleSheets = [];
    document.body.append(wrap);
    adoptWidgetStyles(shadow, 'user');
    widgetStyles.user.forEach((sheet, index) => assert.equal(shadow.adoptedStyleSheets[index], sheet));

    shadow.adoptedStyleSheets = [];
    pip.window.document.body.append(wrap);
    adoptWidgetStyles(shadow, 'user');
    const pipSheets = [...shadow.adoptedStyleSheets];
    adoptWidgetStyles(shadow, 'user');
    assert.deepEqual([...shadow.adoptedStyleSheets], pipSheets, 'repeated adoption in one document is idempotent');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('readoptWidgetStyles heals nested shadow roots and new PiP-created roots', async () => {
  const main = new JSDOM('<body><div id="agent-thread"></div></body>', { url: 'http://localhost/' });
  const pip = new JSDOM('<body></body>', { url: 'about:blank' });
  const previous = installDOMGlobals(main.window);
  try {
    const { adoptWidgetStyles, readoptWidgetStyles, widgetStyles, widgetStylesLoaded } =
      await import(`../shared/widget-styles.js?pip=${Date.now()}-nested`);
    await widgetStylesLoaded(async () => '.x { color: red; }');

    const thread = document.getElementById('agent-thread');
    const outer = shadowHost(document, 'agent', adoptWidgetStyles);
    const nested = shadowHost(document, 'reasoning', adoptWidgetStyles);
    const sibling = shadowHost(document, 'tool', adoptWidgetStyles);
    outer.shadow.append(nested.host);
    thread.append(outer.host, sibling.host);

    for (const { shadow } of [outer, nested, sibling]) shadow.adoptedStyleSheets = [];
    pip.window.document.body.append(thread);
    readoptWidgetStyles(thread);

    for (const [entry, name] of [[outer, 'agent'], [nested, 'reasoning'], [sibling, 'tool']]) {
      assert.equal(entry.shadow.adoptedStyleSheets.length, widgetStyles[name].length, name);
      for (const sheet of entry.shadow.adoptedStyleSheets) {
        assert.ok(sheet instanceof pip.window.CSSStyleSheet, `${name} sheets belong to the PiP document`);
      }
    }

    // A widget mounted inside the PiP document after the move adopts sheets
    // built for that document on its very first render — no re-adopt pass.
    const fresh = shadowHost(pip.window.document, 'user', adoptWidgetStyles);
    thread.append(fresh.host);
    for (const sheet of fresh.shadow.adoptedStyleSheets) {
      assert.ok(sheet instanceof pip.window.CSSStyleSheet);
    }
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('document PiP re-adopts widget styles on entry and restore, with absolute stylesheet links', async () => {
  const main = new JSDOM(`<head><link rel="stylesheet" href="styles/agent.css?v=1"></head>
    <body><div id="agent-thread"><div class="event-stream"><div id="w1"></div><div id="w2"></div></div></div>
    <div id="agent-composer-stack"></div></body>`, { url: 'http://localhost/' });
  const pip = new JSDOM('<head></head><body></body>', { url: 'about:blank' });
  const previous = installDOMGlobals(main.window);
  try {
    main.window.documentPictureInPicture = { requestWindow: async () => pip.window };
    // Import through the same specifier pip.js uses so the adoption registry
    // is shared between the test and the module under test.
    const { adoptWidgetStyles, widgetStyles, widgetStylesLoaded } = await sharedStylesImport('js/pip.js');
    const { openMiniWindow, miniWindowOpen } = await import(`../js/pip.js?pip=${Date.now()}`);
    await widgetStylesLoaded(async () => '.x { color: red; }');

    const w1 = shadowHost(document, 'agent', adoptWidgetStyles);
    const w2 = shadowHost(document, 'user', adoptWidgetStyles);
    document.getElementById('w1').replaceWith(w1.host);
    document.getElementById('w2').replaceWith(w2.host);
    const thread = document.getElementById('agent-thread');
    const composer = document.getElementById('agent-composer-stack');

    assert.equal(await openMiniWindow(), 'pip');
    assert.equal(miniWindowOpen(), true);
    assert.equal(thread.ownerDocument, pip.window.document);
    assert.equal(composer.ownerDocument, pip.window.document);
    for (const { shadow } of [w1, w2]) {
      assert.ok(shadow.adoptedStyleSheets.length > 0, 'sheets restored after the cross-document move');
      for (const sheet of shadow.adoptedStyleSheets) {
        assert.ok(sheet instanceof pip.window.CSSStyleSheet, 'adopted sheets belong to the PiP document');
      }
    }
    const cloned = pip.window.document.querySelector('link[rel="stylesheet"]');
    assert.equal(cloned.getAttribute('href'), 'http://localhost/styles/agent.css?v=1',
      'cloned links pin the resolved absolute URL — relative hrefs 404 on the about:blank base');

    pip.window.dispatchEvent(new pip.window.Event('pagehide'));
    assert.equal(document.getElementById('agent-thread'), thread, 'thread returns to the shell document');
    assert.equal(composer.ownerDocument, document);
    for (const [entry, name] of [[w1, 'agent'], [w2, 'user']]) {
      assert.equal(entry.shadow.adoptedStyleSheets.length, widgetStyles[name].length);
      widgetStyles[name].forEach((sheet, index) => {
        assert.equal(entry.shadow.adoptedStyleSheets[index], sheet, 'restore re-adopts the original shared sheets');
      });
    }
  } finally {
    restoreDOMGlobals(previous);
  }
});
