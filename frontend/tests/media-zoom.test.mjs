import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  openZoomableMedia,
  openArtifactPopup,
  openTextPreviewPopup,
  attachZoomButtons,
  attachMermaidZoomButton,
} from '../js/media-zoom.js';

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  return dom;
}

function cleanup() {
  delete globalThis.window;
  delete globalThis.document;
}

// ---------- openZoomableMedia: image ----------

test('openZoomableMedia creates an overlay with zoom controls for an image', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'https://example.com/cat.png', alt: 'A cat' });
    const overlay = document.querySelector('.media-zoom-overlay');
    assert.ok(overlay, 'overlay appended to body');
    assert.ok(overlay.querySelector('img[src="https://example.com/cat.png"]'), 'image in overlay');
    assert.ok(overlay.querySelector('.media-zoom-btn'), 'zoom buttons present');
    assert.ok(overlay.querySelector('.media-zoom-reset'), 'reset button present');
    assert.ok(overlay.querySelector('.media-zoom-scale'), 'scale label present');
    overlay.remove();
  } finally { cleanup(); }
});

test('openZoomableMedia creates an overlay with a cloned SVG', () => {
  makeDom();
  try {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 100 50');
    svg.innerHTML = '<rect width="100" height="50" fill="red"/>';
    openZoomableMedia({ svgEl: svg, alt: 'Mermaid diagram' });
    const overlay = document.querySelector('.media-zoom-overlay');
    assert.ok(overlay, 'overlay appended');
    const cloned = overlay.querySelector('svg');
    assert.ok(cloned, 'SVG clone in overlay');
    assert.ok(cloned.querySelector('rect'), 'SVG content cloned');
    // Explicit dimensions set from viewBox so the clone is visible in flex.
    assert.ok(cloned.hasAttribute('width'), 'width set from viewBox');
    assert.ok(cloned.hasAttribute('height'), 'height set from viewBox');
    overlay.remove();
  } finally { cleanup(); }
});

test('openZoomableMedia is a no-op when neither src nor svgEl is provided', () => {
  makeDom();
  try {
    openZoomableMedia({});
    assert.equal(document.querySelector('.media-zoom-overlay'), null, 'no overlay created');
  } finally { cleanup(); }
});

// ---------- zoom controls ----------

test('zoom in button increases scale and updates label', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'x.png' });
    const overlay = document.querySelector('.media-zoom-overlay');
    const scaleLabel = overlay.querySelector('.media-zoom-scale');
    assert.equal(scaleLabel.textContent, '100%');
    const zoomIn = overlay.querySelector('.media-zoom-btn[aria-label="Zoom in"]');
    zoomIn.click();
    assert.ok(scaleLabel.textContent !== '100%', 'scale changed after zoom in');
    overlay.remove();
  } finally { cleanup(); }
});

test('zoom out button decreases scale (clamped to MIN_SCALE)', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'x.png' });
    const overlay = document.querySelector('.media-zoom-overlay');
    const scaleLabel = overlay.querySelector('.media-zoom-scale');
    const zoomOut = overlay.querySelector('.media-zoom-btn[aria-label="Zoom out"]');
    // Click zoom out many times — should clamp, not go negative.
    for (let i = 0; i < 50; i++) zoomOut.click();
    const pct = parseInt(scaleLabel.textContent, 10);
    assert.ok(pct >= 20, `scale clamped at min 20%, got ${pct}%`);
    overlay.remove();
  } finally { cleanup(); }
});

test('reset button restores scale to 100%', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'x.png' });
    const overlay = document.querySelector('.media-zoom-overlay');
    const scaleLabel = overlay.querySelector('.media-zoom-scale');
    const zoomIn = overlay.querySelector('.media-zoom-btn[aria-label="Zoom in"]');
    const reset = overlay.querySelector('.media-zoom-reset');
    zoomIn.click();
    zoomIn.click();
    assert.ok(scaleLabel.textContent !== '100%', 'scale changed');
    reset.click();
    assert.equal(scaleLabel.textContent, '100%', 'scale reset to 100%');
    overlay.remove();
  } finally { cleanup(); }
});

// ---------- close handlers ----------

test('close button removes the overlay', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'x.png' });
    const overlay = document.querySelector('.media-zoom-overlay');
    assert.ok(overlay);
    overlay.querySelector('.media-zoom-close').click();
    assert.equal(document.querySelector('.media-zoom-overlay'), null, 'overlay removed');
  } finally { cleanup(); }
});

test('Escape key closes the overlay', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'x.png' });
    assert.ok(document.querySelector('.media-zoom-overlay'));
    const evt = new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true });
    document.dispatchEvent(evt);
    assert.equal(document.querySelector('.media-zoom-overlay'), null, 'overlay removed on Escape');
  } finally { cleanup(); }
});

test('click on overlay backdrop closes the overlay', () => {
  makeDom();
  try {
    openZoomableMedia({ src: 'x.png' });
    const overlay = document.querySelector('.media-zoom-overlay');
    overlay.dispatchEvent(new window.MouseEvent('click', { bubbles: true }));
    assert.equal(document.querySelector('.media-zoom-overlay'), null, 'overlay removed on backdrop click');
  } finally { cleanup(); }
});

// ---------- openArtifactPopup ----------

test('openArtifactPopup creates an 80% screen overlay with an iframe', () => {
  makeDom();
  try {
    openArtifactPopup({ srcDoc: '<!doctype html><html><body><h1>Hello</h1></body></html>', title: 'My Artifact' });
    const overlay = document.querySelector('.artifact-popup-overlay');
    assert.ok(overlay, 'artifact popup overlay created');
    const iframe = overlay.querySelector('iframe');
    assert.ok(iframe, 'iframe present');
    assert.equal(iframe.getAttribute('srcdoc'), '<!doctype html><html><body><h1>Hello</h1></body></html>');
    assert.ok(overlay.querySelector('.artifact-popup-title'), 'title bar present');
    overlay.remove();
  } finally { cleanup(); }
});

test('openArtifactPopup is a no-op without srcDoc', () => {
  makeDom();
  try {
    openArtifactPopup({ title: 'Empty' });
    assert.equal(document.querySelector('.artifact-popup-overlay'), null);
  } finally { cleanup(); }
});

test('openArtifactPopup close button removes overlay', () => {
  makeDom();
  try {
    openArtifactPopup({ srcDoc: '<html></html>', title: 'Test' });
    const overlay = document.querySelector('.artifact-popup-overlay');
    overlay.querySelector('.media-zoom-close').click();
    assert.equal(document.querySelector('.artifact-popup-overlay'), null);
  } finally { cleanup(); }
});

// ---------- attachZoomButtons ----------

test('attachZoomButtons adds a zoom trigger to rendered mermaid blocks', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="mermaid-block"><svg viewBox="0 0 10 10"><rect/></svg></div>';
    attachZoomButtons(c);
    const btn = c.querySelector('.media-zoom-trigger');
    assert.ok(btn, 'zoom trigger button added to mermaid block');
    assert.ok(btn.closest('.mermaid-block'), 'button is inside the mermaid block');
  } finally { cleanup(); }
});

test('attachZoomButtons skips mermaid blocks without SVG (placeholder)', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="mermaid-block"><pre class="mermaid-src">flowchart TD</pre></div>';
    attachZoomButtons(c);
    assert.equal(c.querySelector('.media-zoom-trigger'), null, 'no button on placeholder');
  } finally { cleanup(); }
});

test('attachZoomButtons wraps inline images with a zoom trigger', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="agent-bubble-text"><img src="https://example.com/pic.png" alt="pic"></div>';
    attachZoomButtons(c);
    const wrap = c.querySelector('.media-zoom-img-wrap');
    assert.ok(wrap, 'image wrapped');
    assert.ok(wrap.querySelector('.media-zoom-trigger'), 'zoom trigger in wrapper');
  } finally { cleanup(); }
});

test('attachZoomButtons excludes generated-image cards (own lightbox)', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="agent-genimage-card"><div class="agent-bubble-text"><img src="x.png"></div></div>';
    attachZoomButtons(c);
    assert.equal(c.querySelector('.media-zoom-img-wrap'), null, 'genimage image not wrapped');
    assert.equal(c.querySelector('.media-zoom-trigger'), null, 'no trigger on genimage');
  } finally { cleanup(); }
});

test('attachZoomButtons excludes broken images (img-load-error)', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="agent-bubble-text"><img src="x.png" class="img-load-error"></div>';
    attachZoomButtons(c);
    assert.equal(c.querySelector('.media-zoom-img-wrap'), null, 'broken image not wrapped');
  } finally { cleanup(); }
});

test('attachZoomButtons is idempotent — repeated calls do not add duplicate buttons', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="mermaid-block"><svg viewBox="0 0 10 10"><rect/></svg></div>';
    attachZoomButtons(c);
    attachZoomButtons(c);
    attachZoomButtons(c);
    assert.equal(c.querySelectorAll('.media-zoom-trigger').length, 1, 'only one trigger');
  } finally { cleanup(); }
});

test('attachMermaidZoomButton adds a trigger to a single block', () => {
  makeDom();
  try {
    const block = document.createElement('div');
    block.className = 'mermaid-block';
    block.innerHTML = '<svg viewBox="0 0 10 10"><rect/></svg>';
    attachMermaidZoomButton(block);
    assert.ok(block.querySelector('.media-zoom-trigger'), 'trigger added');
    // Idempotent.
    attachMermaidZoomButton(block);
    assert.equal(block.querySelectorAll('.media-zoom-trigger').length, 1, 'no duplicate');
  } finally { cleanup(); }
});

test('attachZoomButtons handles null/missing container gracefully', () => {
  makeDom();
  try {
    assert.doesNotThrow(() => attachZoomButtons(null));
    assert.doesNotThrow(() => attachZoomButtons(undefined));
  } finally { cleanup(); }
});

// ---------- zoom trigger click opens overlay ----------

test('clicking the mermaid zoom trigger opens the zoomable overlay', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="mermaid-block"><svg viewBox="0 0 10 10"><rect/></svg></div>';
    attachZoomButtons(c);
    assert.equal(document.querySelector('.media-zoom-overlay'), null);
    c.querySelector('.media-zoom-trigger').click();
    assert.ok(document.querySelector('.media-zoom-overlay'), 'overlay opened');
    document.querySelector('.media-zoom-overlay').remove();
  } finally { cleanup(); }
});

test('clicking the image zoom trigger opens the zoomable overlay', () => {
  makeDom();
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="agent-bubble-text"><img src="https://example.com/pic.png" alt="pic"></div>';
    attachZoomButtons(c);
    c.querySelector('.media-zoom-trigger').click();
    const overlay = document.querySelector('.media-zoom-overlay');
    assert.ok(overlay, 'overlay opened');
    assert.ok(overlay.querySelector('img[src="https://example.com/pic.png"]'), 'image in overlay');
    overlay.remove();
  } finally { cleanup(); }
});

// ---------- openTextPreviewPopup ----------

test('openTextPreviewPopup creates an overlay and loads file content', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async (url) => {
    assert.match(url, /\/local-file\?path=%2Fpath%2Fto%2Ffile\.txt/);
    return {
      ok: true,
      status: 200,
      text: async () => 'hello world from file',
    };
  };
  try {
    await openTextPreviewPopup('/path/to/file.txt:12');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay, 'text preview overlay created');
    assert.equal(overlay.getAttribute('aria-label'), 'file.txt');
    assert.match(overlay.textContent, /hello world from file/);
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup renders markdown for .md files', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    text: async () => '# Heading\n\n- item 1',
  });
  try {
    await openTextPreviewPopup('/path/to/tools.md');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay);
    assert.ok(overlay.querySelector('h1'), 'renders h1');
    assert.ok(overlay.querySelector('ul'), 'renders ul');
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup renders Mermaid diagrams in markdown previews', async () => {
  const dom = makeDom();
  const origFetch = globalThis.fetch;
  dom.window.mermaid = {
    initialize() {},
    parse: async () => true,
    render: async () => ({ svg: '<svg viewBox="0 0 100 50"><path d="M0 0"/></svg>' }),
  };
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    text: async () => '# Diagram\n\n```mermaid\nflowchart TD\n A-->B\n```',
  });
  try {
    await openTextPreviewPopup('/path/to/diagram.md');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay?.querySelector('.mermaid-block'), 'Mermaid placeholder is present');
    assert.ok(overlay?.querySelector('.mermaid-block svg'), 'Mermaid diagram is rendered');
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup handles fetch errors gracefully', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: false,
    status: 404,
    statusText: 'Not Found',
  });
  try {
    await openTextPreviewPopup('/missing/file.go');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay);
    assert.match(overlay.textContent, /Failed to load/);
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});
