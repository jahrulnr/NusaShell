import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { openTextPreviewPopup, resolveWorkspaceFilePath } from '../js/media-zoom.js';

const root = new URL('../', import.meta.url);

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

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
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

async function settle(rounds = 12) {
  for (let i = 0; i < rounds; i += 1) await Promise.resolve();
}

function click(dom, node) {
  const event = new dom.window.MouseEvent('click', { bubbles: true, cancelable: true });
  node.dispatchEvent(event);
  return event;
}

function overlays() {
  return [...document.querySelectorAll('.agent-text-preview-overlay')];
}

// ── Finding 7: reasoning code blocks expose styled editor actions ────────

test('reasoning details render styled code editor controls inside the block chrome', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/reasoning.js?round2-code=${Date.now()}`);
    const widget = document.createElement('reasoning-widget');
    widget.event = {
      id: 'reason-code',
      type: 'agent.reasoning',
      summary: 'Plan',
      details: ['Checking the fixture\n\n```js\nconst ready = true;\n```'],
    };
    document.body.append(widget);
    widget.shadowRoot.querySelector('button').click();
    const details = widget.shadowRoot.querySelector('.reasoning-details');
    const pre = details.querySelector('pre');
    assert.ok(pre?.querySelector('code'), 'fenced markdown renders a code block');
    const trigger = pre.querySelector('.code-editor-trigger');
    assert.equal(trigger?.getAttribute('aria-label'), 'Open JavaScript code in editor');
    assert.equal(pre.querySelector('.code-language-label')?.textContent, 'JavaScript');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('reasoning stylesheet carries the positioned code-block action chrome', async () => {
  const css = await readFile(new URL('widgets/reasoning.css', root), 'utf8');
  assert.match(css, /\.reasoning-details pre\s*\{[^}]*position:\s*relative;/s,
    'the pre must anchor the floating label and trigger');
  assert.match(css, /\.reasoning-details pre\s*\{[^}]*padding:\s*3\dpx/s,
    'the pre reserves headroom for the code actions');
  assert.match(css, /\.reasoning-details \.code-language-label\s*\{[^}]*position:\s*absolute;/s);
  assert.match(css, /\.reasoning-details \.code-editor-trigger\s*\{[^}]*position:\s*absolute;/s);
  assert.match(css, /\.reasoning-details \.code-editor-trigger:hover|\.reasoning-details \.code-editor-trigger:focus-visible/,
    'the trigger keeps a styled hover/focus affordance');
});

// ── Finding 8: reasoning markdown links route through the preview seam ────

async function mountedReasoning(details, tag = `links-${Date.now()}`) {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  await import(`../widgets/reasoning.js?${tag}`);
  const widget = document.createElement('reasoning-widget');
  widget.event = { id: 'reason-links', type: 'agent.reasoning', summary: 'Plan', details };
  document.body.append(widget);
  widget.shadowRoot.querySelector('button').click();
  return { dom, previous, widget, details: widget.shadowRoot.querySelector('.reasoning-details') };
}

test('reasoning markdown emits local-file-open for relative and absolute file links only', async () => {
  const { dom, previous, widget, details } = await mountedReasoning([
    'Evidence\n\n- [audit](readiness-audit-2026-10-12.md)\n- [abs](/abs/dir/note.md)\n- [ext](https://example.com/x)\n- [frag](#local)',
  ]);
  try {
    const emitted = [];
    widget.addEventListener('local-file-open', (event) => emitted.push(event.detail));

    const relative = details.querySelector('a[href="readiness-audit-2026-10-12.md"]');
    assert.ok(relative, 'the historical relative markdown link renders');
    const event = click(dom, relative);
    assert.equal(event.defaultPrevented, true, 'a workspace-relative file link must not navigate the app');
    assert.deepEqual(emitted.at(-1), { path: 'readiness-audit-2026-10-12.md' });

    const absolute = details.querySelector('a[data-local-path="/abs/dir/note.md"]');
    assert.ok(absolute, 'absolute file links keep the data-local-path seam');
    const absEvent = click(dom, absolute);
    assert.equal(absEvent.defaultPrevented, true, 'absolute links must not navigate the app');
    assert.deepEqual(emitted.at(-1), { path: '/abs/dir/note.md' });

    const external = details.querySelector('a[href="https://example.com/x"]');
    const extEvent = click(dom, external);
    assert.equal(extEvent.defaultPrevented, false, 'external links keep their own behavior');
    assert.equal(external.getAttribute('target'), '_blank');
    assert.equal(emitted.length, 2, 'external links emit no preview request');

    const fragment = details.querySelector('a[href="#local"]');
    const fragEvent = click(dom, fragment);
    assert.equal(fragEvent.defaultPrevented, false, 'bare fragments keep default anchor behavior');
    assert.equal(emitted.length, 2, 'fragments emit no preview request');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('resolveWorkspaceFilePath resolves markdown hrefs against the workspace', () => {
  assert.equal(resolveWorkspaceFilePath('/abs/dir/note.md', '/ws/dir'), '/abs/dir/note.md',
    'absolute paths pass straight through');
  assert.equal(resolveWorkspaceFilePath('readiness-audit-2026-10-12.md', '/ws/dir'),
    '/ws/dir/readiness-audit-2026-10-12.md');
  assert.equal(resolveWorkspaceFilePath('docs/report.md', '/ws/dir/'), '/ws/dir/docs/report.md',
    'a trailing workspace slash resolves the same');
  assert.equal(resolveWorkspaceFilePath('../up.md', '/ws/dir'), '/ws/up.md',
    'parent segments walk above the workspace root');
  assert.equal(resolveWorkspaceFilePath('a.md#section', '/ws/dir'), '/ws/dir/a.md#section',
    'fragments survive resolution for the preview scroll seam');
  assert.equal(resolveWorkspaceFilePath('a.md', ''), 'a.md',
    'no workspace leaves the raw href for the error state to describe');
  assert.equal(resolveWorkspaceFilePath('a.md', 'C:\\ws\\dir'), 'C:/ws/dir/a.md',
    'windows workspaces resolve the same way');
});

test('conversation routes workspace-relative local-file-open paths through the preview seam', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  assert.match(app, /resolveWorkspaceFilePath[\s\S]*?from\s+'\.\.\/\.\.\/js\/media-zoom\.js'/,
    'app.js reuses the existing preview-path resolver, not a duplicate service');
  const handler = app.slice(app.indexOf("addEventListener('local-file-open'"));
  assert.match(handler, /openTextPreviewPopup\(resolveWorkspaceFilePath\(/,
    'the existing local-file-open seam resolves before previewing');
  assert.match(handler, /activeConversation\?\.workspace\s*\|\|\s*draftWorkspace/,
    'the workspace base comes from the active room or the draft workspace');
});

// ── Finding 9: media widget falls back to readable text on load errors ───

test('media widget keeps lazy loading and replaces a broken preview with readable actions', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?round2-media=${Date.now()}`);
    const widget = document.createElement('media-tool-widget');
    widget.event = { id: 'm-broken', tool: 'show', args: { path: '/missing/montage2.png' }, status: 'ok' };
    document.body.append(widget);

    widget.shadowRoot.querySelector('[data-primary-action]').click();
    const preview = widget.shadowRoot.querySelector('.media-preview');
    const image = preview.querySelector('img');
    assert.equal(image.loading, 'lazy', 'the image preview stays natively lazy');
    assert.match(image.src, /\/local-file\?path=%2Fmissing%2Fmontage2\.png/);

    image.dispatchEvent(new dom.window.Event('error'));
    assert.equal(preview.querySelector('img'), null, 'the broken image is replaced');
    const fallback = preview.querySelector('.media-preview-fallback');
    assert.ok(fallback, 'a readable fallback replaces the broken image');
    assert.match(fallback.textContent, /montage2\.png/);
    assert.match(fallback.textContent, /could not be loaded/i);

    const opened = new Promise((resolve) => widget.addEventListener('artifact-open', resolve, { once: true }));
    [...fallback.querySelectorAll('button')].find((button) => button.textContent === 'Preview file').click();
    assert.equal((await opened).detail.path, '/missing/montage2.png',
      'the fallback reuses the existing file-preview action');

    [...fallback.querySelectorAll('button')].find((button) => button.textContent === 'Retry').click();
    const retried = preview.querySelector('img');
    assert.ok(retried, 'retry mounts a fresh media element');
    retried.dispatchEvent(new dom.window.Event('error'));
    assert.ok(preview.querySelector('.media-preview-fallback'),
      'a second failure falls back instead of looping');
  } finally {
    restoreDOMGlobals(previous);
  }
});

test('media widget error fallback also covers audio and video preview surfaces', async () => {
  const dom = new JSDOM('<body></body>', { url: 'http://localhost/' });
  const previous = installDOMGlobals(dom.window);
  try {
    await import(`../widgets/tools/widgets.js?round2-media-av=${Date.now()}`);
    for (const [path, tag] of [['/missing/clip.mp3', 'audio'], ['/missing/clip.mp4', 'video']]) {
      const widget = document.createElement('media-tool-widget');
      widget.event = { id: `m-${tag}`, tool: 'show', args: { path }, status: 'ok' };
      document.body.append(widget);
      widget.shadowRoot.querySelector('[data-primary-action]').click();
      const preview = widget.shadowRoot.querySelector('.media-preview');
      const media = preview.querySelector(tag);
      assert.ok(media, `${tag} preview mounts`);
      media.dispatchEvent(new dom.window.Event('error'));
      assert.ok(preview.querySelector('.media-preview-fallback'), `${tag} failure shows the readable fallback`);
      assert.equal(preview.querySelector(tag), null, `${tag} element is replaced, not hidden`);
      widget.remove();
    }
  } finally {
    restoreDOMGlobals(previous);
  }
});

// ── Finding 10: text preview failure state is friendly and recoverable ───

function stubFetch(handler) {
  const calls = [];
  const original = globalThis.fetch;
  globalThis.fetch = async (url) => {
    calls.push(url);
    return handler(url);
  };
  return { calls, restore: () => { globalThis.fetch = original; } };
}

function notFound() {
  return { ok: false, status: 404, statusText: 'Not Found', headers: { get: () => null }, text: async () => '' };
}

test('a missing local file renders a friendly preview error with retry and close seams', async () => {
  const dom = makeDom();
  const fetchStub = stubFetch(notFound);
  try {
    await openTextPreviewPopup('/media/workspace/tasks/termux-parity-matrix.md');
    const overlay = overlays()[0];
    const error = overlay.querySelector('.agent-text-preview-error');
    assert.ok(error, 'the existing error state container renders');
    assert.equal(error.getAttribute('role'), 'alert');
    assert.equal(error.querySelector('strong')?.textContent.includes('termux-parity-matrix.md'), true,
      'the file label stays descriptive');
    assert.match(error.textContent, /moved, renamed, or deleted/i, 'the primary copy is a friendly message');
    const detail = error.querySelector('.agent-text-preview-error-detail');
    assert.ok(detail, 'technical details stay available as secondary copy');
    assert.match(detail.textContent, /termux-parity-matrix\.md/);
    assert.match(detail.textContent, /HTTP 404/);
    assert.equal(error.querySelector('strong').textContent.includes('HTTP 404'), false,
      'the primary message is not a raw status dump');
    assert.equal(error.querySelectorAll('button').length, 1, 'only Retry sits inside the error message');
    assert.ok(overlay.querySelector('.media-zoom-close'), 'the existing popup close remains available');
    assert.equal(error.closest('.agent-text-preview-content').classList.contains('is-preview-error'), true);
    assert.equal(overlay.querySelector('.agent-text-preview-kind').dataset.previewKind, 'error',
      'the kind badge stops pretending the file is still loading');
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

test('preview error close reuses the overlay seam and retry re-fetches into a real preview', async () => {
  const dom = makeDom();
  let fail = true;
  const fetchStub = stubFetch(async () => {
    if (fail) return notFound();
    const text = '# Recovered\n\nThe file is back.';
    const data = new TextEncoder().encode(text);
    return {
      ok: true,
      status: 200,
      statusText: 'OK',
      headers: { get: (name) => (name.toLowerCase() === 'content-type' ? 'text/markdown' : name.toLowerCase() === 'content-length' ? String(data.byteLength) : null) },
      text: async () => text,
      arrayBuffer: async () => data.buffer.slice(0),
    };
  });
  try {
    await openTextPreviewPopup('/repo/docs/missing.md');
    const overlay = overlays()[0];
    const retry = [...overlay.querySelectorAll('.agent-text-preview-error button')]
      .find((button) => button.textContent === 'Retry');
    const close = overlay.querySelector('.media-zoom-close');
    assert.ok(retry && close, 'error state exposes retry and close');

    fail = false;
    retry.click();
    await settle();
    assert.equal(fetchStub.calls.length, 2, 'retry issues a fresh request');
    assert.equal(overlay.querySelector('.agent-text-preview-content').classList.contains('is-preview-error'), false, 'successful retry restores the normal document layout');
    assert.match(overlay.querySelector('.agent-text-preview-md')?.textContent || '', /Recovered/,
      'a successful retry renders the document in the same overlay');

    // Re-fail to exercise the close seam: a fresh overlay with a 404.
    fail = true;
    await openTextPreviewPopup('/repo/docs/gone.md');
    const gone = overlays()[1];
    const goneClose = gone.querySelector('.media-zoom-close');
    goneClose.click();
    assert.equal(overlays().length, 1, 'close reuses the overlay dismissal seam');
  } finally {
    fetchStub.restore();
    cleanup();
  }
});

// ── Integration notes pinned in source ──────────────────────────────────

test('shared markdown-body link binding is exported for shadow-DOM widgets', async () => {
  const source = await readFile(new URL('shared/markdown-body.js', root), 'utf8');
  assert.match(source, /export function bindLocalFileLinks/,
    'shadow-DOM markdown shares one delegated local-file seam');
  assert.match(source, /data-local-path/);
  const reasoning = await readFile(new URL('widgets/reasoning.js', root), 'utf8');
  assert.match(reasoning, /bindLocalFileLinks/);
});
