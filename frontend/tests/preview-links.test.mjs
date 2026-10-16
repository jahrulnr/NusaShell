import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { openTextPreviewPopup } from '../js/media-zoom.js';

// Regression coverage for Markdown previews: links inside the previewed
// document must keep working in place instead of navigating the app.
// - Relative file links resolve against the previewed document's directory
//   and open the same type-aware preview (nested navigation).
// - data-local-path absolute links get the same nested-preview treatment.
// - Fragments scroll inside the preview (file.md#section and bare #section).
// - External links keep their target=_blank behavior.
const root = new URL('../', import.meta.url);
const README_PATH = decodeURIComponent(new URL('reports/README.md', import.meta.url).pathname);
const REPORTS_DIR = README_PATH.slice(0, README_PATH.lastIndexOf('/'));

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  // JSDOM does not fetch script/link src, so ensureCodeMirror() would hang.
  const origCreate = dom.window.document.createElement.bind(dom.window.document);
  dom.window.document.createElement = (tag) => {
    const node = origCreate(tag);
    if (tag === 'script' || tag === 'link') {
      setTimeout(() => node.onerror?.(new Error('no network in jsdom')), 0);
    }
    return node;
  };
  return dom;
}

function cleanup() {
  delete globalThis.window;
  delete globalThis.document;
}

function markdownResponse(text) {
  const data = new TextEncoder().encode(text);
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => {
      const n = name.toLowerCase();
      if (n === 'content-type') return 'text/markdown; charset=utf-8';
      if (n === 'content-length') return String(data.byteLength);
      return null;
    } },
    text: async () => text,
    arrayBuffer: async () => data.buffer.slice(0),
  };
}

// stubFetch maps absolute file paths to Markdown bodies and records every
// /local-file path the preview requests, so tests can assert resolution.
function stubFetch(bodies) {
  const calls = [];
  const original = globalThis.fetch;
  globalThis.fetch = async (url) => {
    calls.push(url);
    const path = new URL(url, 'http://test.invalid').searchParams.get('path');
    const body = bodies[path];
    if (body == null) {
      return { ok: false, status: 404, statusText: 'Not Found', headers: { get: () => null }, text: async () => '' };
    }
    return markdownResponse(body);
  };
  return { calls, restore: () => { globalThis.fetch = original; } };
}

function overlays() {
  return [...document.querySelectorAll('.agent-text-preview-overlay')];
}

function click(dom, anchor) {
  const event = new dom.window.MouseEvent('click', { bubbles: true, cancelable: true });
  anchor.dispatchEvent(event);
  return event;
}

async function settle(rounds = 12) {
  for (let i = 0; i < rounds; i += 1) await Promise.resolve();
}

test('markdown preview resolves the fixture README relative links into nested previews', async () => {
  const dom = makeDom();
  const readme = await readFile(README_PATH, 'utf8');
  const bodies = {
    [README_PATH]: readme,
    [`${REPORTS_DIR}/A-coverage.md`]: '# Coverage\n\nLinked report body.',
  };
  const fetchStub = stubFetch(bodies);
  try {
    await openTextPreviewPopup(README_PATH);
    const overlay = overlays()[0];
    assert.ok(overlay?.querySelector('.agent-text-preview-md'), 'README renders as Markdown');

    const link = overlay.querySelector('.agent-text-preview-md a[href="./A-coverage.md"]');
    assert.ok(link, 'README keeps its ./A-coverage.md link');
    const event = click(dom, link);
    assert.equal(event.defaultPrevented, true, 'relative link must not navigate the app');
    await settle();

    assert.equal(overlays().length, 2, 'relative link opens a nested preview');
    const fetched = fetchStub.calls.at(-1);
    assert.ok(fetched.includes(`path=${encodeURIComponent(`${REPORTS_DIR}/A-coverage.md`)}`),
      `nested preview resolves against the previewed directory: ${fetched}`);
    assert.match(overlays()[1].textContent, /Linked report body/, 'nested preview renders the linked document');
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('markdown preview resolves nested relative paths, parents, and link fragments', async () => {
  const dom = makeDom();
  const docPath = '/repo/docs/reports/README.md';
  const bodies = {
    [docPath]: [
      '# Reports', '',
      '- [fixture](work/F1-fixes.md)',
      '- [parent](../other.md)',
      '- [frag](B-produksi.md#matrix)',
      '- [self](#reports)',
    ].join('\n'),
    '/repo/docs/reports/work/F1-fixes.md': '# Fixes\n\nDeep link body.',
    '/repo/docs/other.md': '# Other\n\nParent link body.',
    '/repo/docs/reports/B-produksi.md': '# Inventory\n\n## Matrix\n\nrows',
  };
  const fetchStub = stubFetch(bodies);
  const scrolled = [];
  dom.window.Element.prototype.scrollIntoView = function scrollIntoView() { scrolled.push(this); };
  try {
    await openTextPreviewPopup(docPath);
    const overlay = overlays()[0];
    assert.equal(overlays().length, 1);

    // Sub-directory relative path resolves under the document's directory.
    click(dom, overlay.querySelector('a[href="work/F1-fixes.md"]'));
    await settle();
    assert.equal(overlays().length, 2, 'sub-directory link opens a nested preview');
    assert.ok(fetchStub.calls.at(-1).includes(`path=${encodeURIComponent('/repo/docs/reports/work/F1-fixes.md')}`),
      fetchStub.calls.at(-1));

    // Parent traversal resolves above the document's directory.
    click(dom, overlay.querySelector('a[href="../other.md"]'));
    await settle();
    assert.equal(overlays().length, 3, 'parent link opens a nested preview');
    assert.ok(fetchStub.calls.at(-1).includes(`path=${encodeURIComponent('/repo/docs/other.md')}`),
      fetchStub.calls.at(-1));

    // A file fragment opens the target document, then scrolls to its heading.
    click(dom, overlay.querySelector('a[href="B-produksi.md#matrix"]'));
    await settle();
    assert.equal(overlays().length, 4, 'fragment link opens a nested preview');
    assert.ok(fetchStub.calls.at(-1).includes(`path=${encodeURIComponent('/repo/docs/reports/B-produksi.md')}`),
      `fragment never reaches the file request: ${fetchStub.calls.at(-1)}`);
    assert.equal(scrolled.at(-1)?.tagName, 'H2', 'fragment scrolls to the matching heading');

    // A bare fragment scrolls inside the current preview without a fetch.
    const fetchedBefore = fetchStub.calls.length;
    const selfEvent = click(dom, overlay.querySelector('a[href="#reports"]'));
    assert.equal(selfEvent.defaultPrevented, true, 'bare fragments stay inside the preview');
    await settle();
    assert.equal(fetchStub.calls.length, fetchedBefore, 'bare fragment triggers no file fetch');
    assert.equal(overlays().length, 4, 'bare fragment opens no extra overlay');
    assert.equal(scrolled.at(-1)?.tagName, 'H1', 'bare fragment scrolls to the matching heading');
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('markdown preview keeps external links external and routes absolute local links through the same seam', async () => {
  const dom = makeDom();
  const docPath = '/repo/docs/index.md';
  const bodies = {
    [docPath]: '# Index\n\n- [site](https://example.com/x)\n- [abs](/abs/dir/note.md)\n',
    '/abs/dir/note.md': '# Note\n\nAbsolute body.',
  };
  const fetchStub = stubFetch(bodies);
  try {
    await openTextPreviewPopup(docPath);
    const overlay = overlays()[0];

    const external = overlay.querySelector('a[href="https://example.com/x"]');
    const event = click(dom, external);
    assert.equal(event.defaultPrevented, false, 'external links keep default navigation');
    assert.equal(external.getAttribute('target'), '_blank');
    await settle();
    assert.equal(overlays().length, 1, 'external link opens no preview');

    const absolute = overlay.querySelector('a[data-local-path="/abs/dir/note.md"]');
    assert.ok(absolute, 'absolute file link keeps the data-local-path seam');
    const absEvent = click(dom, absolute);
    assert.equal(absEvent.defaultPrevented, true, 'absolute file link must not navigate the app');
    await settle();
    assert.equal(overlays().length, 2, 'absolute file link opens a nested preview');
    assert.ok(fetchStub.calls.at(-1).includes(`path=${encodeURIComponent('/abs/dir/note.md')}`),
      fetchStub.calls.at(-1));
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('Escape pops only the topmost preview so nested navigation unwinds one level', async () => {
  const dom = makeDom();
  const docPath = '/repo/docs/index.md';
  const bodies = {
    [docPath]: '# Index\n\n- [next](next.md)\n',
    '/repo/docs/next.md': '# Next\n\n- [third](third.md)\n',
    '/repo/docs/third.md': '# Third\n\nEnd of the chain.\n',
  };
  const fetchStub = stubFetch(bodies);
  try {
    await openTextPreviewPopup(docPath);
    click(dom, overlays()[0].querySelector('a[href="next.md"]'));
    await settle();
    click(dom, overlays()[1].querySelector('a[href="third.md"]'));
    await settle();
    assert.equal(overlays().length, 3, 'two nested previews stack over the parent');

    const escape = new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    document.dispatchEvent(escape);
    await settle();
    assert.equal(overlays().length, 2, 'Escape unwinds one preview at a time');
    assert.match(overlays().at(-1).textContent, /Next/, 'the middle preview stays underneath');

    document.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    await settle();
    assert.equal(overlays().length, 1);
    document.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    await settle();
    assert.equal(overlays().length, 0);
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('openTextPreviewPopup scrolls to a heading when the path carries a fragment', async () => {
  const dom = makeDom();
  const docPath = '/repo/docs/guide.md';
  const bodies = { [docPath]: '# Guide\n\n## Usage notes\n\ntext\n' };
  const fetchStub = stubFetch(bodies);
  const scrolled = [];
  dom.window.Element.prototype.scrollIntoView = function scrollIntoView() { scrolled.push(this); };
  try {
    await openTextPreviewPopup(`${docPath}#usage-notes`);
    assert.equal(overlays().length, 1);
    const docFetch = fetchStub.calls.at(-1);
    assert.ok(docFetch.includes(`path=${encodeURIComponent(docPath)}`) && !docFetch.includes('%23'),
      `fragment stays out of the resolved file request: ${docFetch}`);
    assert.equal(scrolled.at(-1)?.textContent, 'Usage notes', 'preview scrolls to the fragment heading');
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('openTextPreviewPopup keeps literal percent-decoded # characters as filenames', async () => {
  // data-local-path values arrive decodeURIComponent'd, so a Markdown author
  // who wrote file%231.md reaches this API as the literal path file#1.md.
  // The whole-path fetch must win before fragment semantics kick in.
  const dom = makeDom();
  const docPath = '/repo/docs/file#1.md';
  const bodies = { [docPath]: '# Literal hash file\n' };
  const fetchStub = stubFetch(bodies);
  const scrolled = [];
  dom.window.Element.prototype.scrollIntoView = function scrollIntoView() { scrolled.push(this); };
  try {
    await openTextPreviewPopup(docPath);
    assert.equal(overlays().length, 1);
    assert.equal(fetchStub.calls.length, 1, 'literal-# path is a single whole-path request');
    assert.ok(fetchStub.calls[0].includes(`path=${encodeURIComponent(docPath)}`),
      fetchStub.calls[0]);
    assert.match(overlays()[0].textContent, /Literal hash file/);
    assert.equal(scrolled.length, 0, 'a literal-# path performs no fragment scroll');
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('markdown preview resolves relative media sources through the local-file seam', async () => {
  // Relative img/video srcs used to resolve against the page URL and 404.
  // They must resolve against the previewed document's directory and load
  // through /local-file — the same seam relative links already use.
  const dom = makeDom();
  const docPath = '/repo/docs/reports/README.md';
  const bodies = {
    [docPath]: [
      '# Doc', '',
      '![dot](./img/sample.svg)',
      '![up](../assets/up.png)',
      '![space](./my%20file.jpg)',
      '![abs](/abs/pic.png)',
      '![ext](https://example.com/x.png)',
      '![data](data:image/svg+xml;base64,AAAA)',
      '![file](file:///abs/via-file.png)',
      '![vid](./clip.mp4)',
    ].join('\n'),
  };
  const fetchStub = stubFetch(bodies);
  const srcOf = (overlay, alt) => overlay.querySelector(`[alt="${alt}"]`)?.getAttribute('src');
  const proxied = (p) => `/local-file?path=${encodeURIComponent(p)}`;
  try {
    await openTextPreviewPopup(docPath);
    const overlay = overlays()[0];
    assert.ok(overlay?.querySelector('.agent-text-preview-md'), 'document renders as Markdown');

    // ./, .., and percent-encoded space names resolve under the document's
    // directory; absolute local paths map straight onto the proxy.
    assert.equal(srcOf(overlay, 'dot'), proxied('/repo/docs/reports/img/sample.svg'));
    assert.equal(srcOf(overlay, 'up'), proxied('/repo/docs/assets/up.png'));
    assert.equal(srcOf(overlay, 'space'), proxied('/repo/docs/reports/my file.jpg'));
    assert.equal(srcOf(overlay, 'abs'), proxied('/abs/pic.png'));
    assert.equal(overlay.querySelector('video')?.getAttribute('src'), proxied('/repo/docs/reports/clip.mp4'),
      'video elements rendered from image syntax resolve the same way');

    // External and data: sources keep their own URLs; a file:// source that
    // markdown.js already proxied must not be wrapped a second time.
    assert.equal(srcOf(overlay, 'ext'), 'https://example.com/x.png');
    assert.equal(srcOf(overlay, 'data'), 'data:image/svg+xml;base64,AAAA');
    assert.equal(srcOf(overlay, 'file'), proxied('/abs/via-file.png'));
  } finally {
    fetchStub.restore();
    cleanup();
  }
});
