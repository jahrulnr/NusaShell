import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  openZoomableMedia,
  openArtifactPopup,
  openAudioLightbox,
  openVideoLightbox,
  openTextPreviewPopup,
  classifyLocalPreview,
  attachZoomButtons,
  attachMermaidZoomButton,
} from '../js/media-zoom.js';

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  // JSDOM does not fetch script/link src, so ensureCodeMirror() would hang.
  // Simulate asset load failure on the next microtask so the loader rejects
  // fast and the <pre><code> fallback renders. Tests that pre-set
  // window.CodeMirror bypass the loader entirely.
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

function previewResponse({ text = '', bytes, contentType = 'text/plain; charset=utf-8', contentLength } = {}) {
  const data = bytes || new TextEncoder().encode(text);
  const len = contentLength != null ? String(contentLength) : String(data.byteLength);
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => {
      const n = name.toLowerCase();
      if (n === 'content-type') return contentType;
      if (n === 'content-length') return len;
      return null;
    } },
    arrayBuffer: async () => data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength),
  };
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

test('openZoomableMedia uses the SVG viewBox rather than a shrunken inline layout', () => {
  makeDom();
  try {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 1200 400');
    svg.getBoundingClientRect = () => ({ width: 12, height: 4 });
    openZoomableMedia({ svgEl: svg, alt: 'Wide Mermaid diagram' });
    const overlay = document.querySelector('.media-zoom-overlay');
    const clone = overlay.querySelector('svg');
    assert.equal(clone.getAttribute('width'), '1200', 'viewBox is the vector’s intrinsic width');
    overlay.querySelector('.media-zoom-btn[aria-label="Zoom in"]').click();
    assert.equal(clone.style.width, '1560px', 'vector zoom resizes SVG layout instead of transform-rasterizing it');
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

// ---------- focus return on close ----------

// Closing any overlay must return keyboard focus to the element that had it
// when the overlay opened (frontend/AGENTS.md: safe focus return for overlays),
// rather than leaving focus on <body>.
function focusTriggerButton(label) {
  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.textContent = label;
  document.body.append(trigger);
  trigger.focus();
  return trigger;
}

test('openZoomableMedia returns keyboard focus to the invoker on close', () => {
  makeDom();
  try {
    const trigger = focusTriggerButton('Open zoom');
    assert.equal(document.activeElement, trigger, 'trigger focused before open');
    openZoomableMedia({ src: 'x.png' });
    assert.notEqual(document.activeElement, trigger, 'focus moved into the overlay');
    document.querySelector('.media-zoom-overlay .media-zoom-close').click();
    assert.equal(document.activeElement, trigger, 'focus returned to the trigger after close');
    trigger.remove();
  } finally { cleanup(); }
});

test('openZoomableMedia close is idempotent and restores focus once', () => {
  makeDom();
  try {
    const trigger = focusTriggerButton('Open zoom');
    openZoomableMedia({ src: 'x.png' });
    const close = document.querySelector('.media-zoom-overlay .media-zoom-close');
    close.click();
    assert.equal(document.activeElement, trigger, 'focus restored on first close');
    // A second close dispatch (e.g. a queued Escape after the backdrop click)
    // must not throw or move focus away from the restored trigger.
    assert.doesNotThrow(() => close.click());
    assert.equal(document.activeElement, trigger, 'focus stays on the trigger after a duplicate close');
    trigger.remove();
  } finally { cleanup(); }
});

test('openArtifactPopup returns keyboard focus to the invoker on close', () => {
  makeDom();
  try {
    const trigger = focusTriggerButton('Open artifact');
    assert.equal(document.activeElement, trigger);
    openArtifactPopup({ srcDoc: '<html></html>', title: 'Artifact' });
    assert.notEqual(document.activeElement, trigger, 'focus moved into the overlay');
    document.querySelector('.artifact-popup-overlay .media-zoom-close').click();
    assert.equal(document.activeElement, trigger, 'focus returned to the trigger after close');
    trigger.remove();
  } finally { cleanup(); }
});

test('openAudioLightbox returns keyboard focus to the invoker on close', () => {
  const dom = makeDom();
  const media = dom.window.HTMLMediaElement.prototype;
  const originalPlay = media.play;
  const originalPause = media.pause;
  media.play = () => Promise.resolve();
  media.pause = () => {};
  try {
    const trigger = focusTriggerButton('Open audio');
    assert.equal(document.activeElement, trigger);
    openAudioLightbox({ src: '/local-file?path=clip.mp3', name: 'clip.mp3' });
    assert.notEqual(document.activeElement, trigger, 'focus moved into the overlay');
    document.querySelector('.agent-audio-lightbox .media-zoom-close').click();
    assert.equal(document.activeElement, trigger, 'focus returned to the trigger after close');
    trigger.remove();
  } finally {
    media.play = originalPlay;
    media.pause = originalPause;
    cleanup();
  }
});

test('openVideoLightbox returns keyboard focus to the invoker on close', () => {
  const dom = makeDom();
  const media = dom.window.HTMLMediaElement.prototype;
  const originalPlay = media.play;
  const originalPause = media.pause;
  media.play = () => Promise.resolve();
  media.pause = () => {};
  try {
    const trigger = focusTriggerButton('Open video');
    assert.equal(document.activeElement, trigger);
    openVideoLightbox({ src: '/local-file?path=clip.mp4', name: 'clip.mp4' });
    assert.notEqual(document.activeElement, trigger, 'focus moved into the overlay');
    document.querySelector('.agent-video-lightbox .media-zoom-close').click();
    assert.equal(document.activeElement, trigger, 'focus returned to the trigger after close');
    trigger.remove();
  } finally {
    media.play = originalPlay;
    media.pause = originalPause;
    cleanup();
  }
});

test('overlay focus return is skipped when the invoker is no longer connected', () => {
  makeDom();
  try {
    const trigger = focusTriggerButton('Open zoom');
    openZoomableMedia({ src: 'x.png' });
    // Remove the trigger while the overlay is open — restoring focus to a
    // detached node would be a no-op; focus must fall back to <body>.
    trigger.remove();
    document.querySelector('.media-zoom-overlay .media-zoom-close').click();
    assert.equal(document.activeElement, document.body, 'no focus restore to a detached invoker');
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

test('audio and video lightboxes keep native controls inside their own popup frames', () => {
  const dom = makeDom();
  const media = dom.window.HTMLMediaElement.prototype;
  const originalPlay = media.play;
  const originalPause = media.pause;
  let pauses = 0;
  media.play = () => Promise.resolve();
  media.pause = () => { pauses++; };
  try {
    openAudioLightbox({ src: '/local-file?path=clip.mp3', name: 'clip.mp3' });
    let overlay = document.querySelector('.agent-audio-lightbox');
    assert.ok(overlay?.querySelector('.agent-audio-lightbox-frame audio[controls]'));
    overlay.querySelector('.media-zoom-close').click();

    openVideoLightbox({ src: '/local-file?path=clip.mp4', name: 'clip.mp4' });
    overlay = document.querySelector('.agent-video-lightbox');
    assert.ok(overlay?.querySelector('.agent-video-lightbox-frame video[controls]'));
    overlay.querySelector('.media-zoom-close').click();
    assert.equal(pauses, 2, 'closing either player stops its media');
  } finally {
    media.play = originalPlay;
    media.pause = originalPause;
    cleanup();
  }
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

test('attachMermaidZoomButton restores the trigger after an SVG innerHTML wipe', () => {
  makeDom();
  try {
    const block = document.createElement('div');
    block.className = 'mermaid-block';
    block.innerHTML = '<svg viewBox="0 0 10 10"><rect/></svg>';
    attachMermaidZoomButton(block);
    assert.equal(block.querySelectorAll('.media-zoom-trigger').length, 1);
    // A second in-flight mermaid.render replaces innerHTML and keeps the
    // data-zoom-attached flag on the block — the live-complete race.
    block.innerHTML = '<svg viewBox="0 0 10 10"><rect/></svg>';
    assert.equal(block.getAttribute('data-zoom-attached'), '1', 'flag survives innerHTML');
    assert.equal(block.querySelector('.media-zoom-trigger'), null, 'button was wiped');
    attachMermaidZoomButton(block);
    assert.equal(block.querySelectorAll('.media-zoom-trigger').length, 1, 'trigger restored');
  } finally { cleanup(); }
});

test('attachZoomButtons finds a mermaid-block when that block is the container', () => {
  makeDom();
  try {
    const block = document.createElement('div');
    block.className = 'mermaid-block';
    block.innerHTML = '<svg viewBox="0 0 10 10"><rect/></svg>';
    attachZoomButtons(block);
    assert.ok(block.querySelector('.media-zoom-trigger'), 'self-targeted mermaid block still gets a trigger');
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

test('classifyLocalPreview trusts the server magic MIME type before the filename', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/misleading.txt',
    contentType: 'application/pdf',
    bytes: new TextEncoder().encode('%PDF-1.7'),
  });
  assert.equal(preview.kind, 'pdf');
  assert.equal(preview.source, 'content-type');
});

test('classifyLocalPreview falls back to a Markdown filename when the server reports plain text', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/notes.mdx',
    contentType: 'text/plain; charset=utf-8',
    bytes: new TextEncoder().encode('# Notes'),
  });
  assert.equal(preview.kind, 'markdown');
  assert.equal(preview.source, 'extension');
});

test('classifyLocalPreview protects an unknown binary payload from text rendering', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/cache.data',
    contentType: 'application/octet-stream',
    bytes: new Uint8Array([0x00, 0x01, 0x02, 0xff]),
  });
  assert.equal(preview.kind, 'binary');
});

test('classifyLocalPreview respects an octet-stream response even when its first bytes are printable', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/opaque.payload',
    contentType: 'application/octet-stream',
    bytes: new TextEncoder().encode('not enough evidence to treat this as text'),
  });
  assert.equal(preview.kind, 'binary');
  assert.equal(preview.source, 'content-type');
});

// ---------- ISO-BMFF magic brand classification ----------

// A `ftyp` box is not always video. The major brand at bytes 8..12 decides:
// AVIF/HEIC brands are images, M4A/M4V are audio, and everything else (isom,
// qt, mp41, mp42, ...) stays video.
function ftypBytes(brand) {
  const brandBytes = [...brand].map((c) => c.charCodeAt(0));
  while (brandBytes.length < 4) brandBytes.push(0x20); // pad with spaces
  // [box size (4, ignored by the classifier), 'ftyp', major brand]
  return new Uint8Array([0x00, 0x00, 0x00, 0x14, 0x66, 0x74, 0x79, 0x70, ...brandBytes]);
}

test('classifyLocalPreview classifies an AVIF ftyp brand as image from magic bytes', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/photo.dat',
    contentType: 'application/octet-stream',
    bytes: ftypBytes('avif'),
  });
  assert.equal(preview.kind, 'image');
  assert.equal(preview.source, 'bytes');
});

test('classifyLocalPreview classifies an HEIC ftyp brand as image from magic bytes', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/shot.dat',
    contentType: 'application/octet-stream',
    bytes: ftypBytes('heic'),
  });
  assert.equal(preview.kind, 'image');
});

test('classifyLocalPreview classifies an M4A ftyp brand as audio from magic bytes', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/clip.dat',
    contentType: 'application/octet-stream',
    bytes: ftypBytes('M4A '),
  });
  assert.equal(preview.kind, 'audio');
  assert.equal(preview.source, 'bytes');
});

test('classifyLocalPreview keeps a plain isom mp4 ftyp brand as video (regression)', () => {
  const preview = classifyLocalPreview({
    filePath: '/path/to/clip.dat',
    contentType: 'application/octet-stream',
    bytes: ftypBytes('isom'),
  });
  assert.equal(preview.kind, 'video');
  assert.equal(preview.source, 'bytes');
});

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

test('openTextPreviewPopup uses available CodeMirror as a read-only Dracula source viewer', async () => {
  const dom = makeDom();
  const origFetch = globalThis.fetch;
  const calls = [];
  dom.window.CodeMirror = (host, options) => {
    calls.push(options);
    const editor = document.createElement('div');
    editor.className = 'CodeMirror cm-s-dracula';
    host.append(editor);
    return { getWrapperElement: () => editor };
  };
  globalThis.fetch = async () => previewResponse({ text: 'export const provider = true;' });
  try {
    await openTextPreviewPopup('/path/to/providers.js');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.deepEqual(calls, [{
      value: 'export const provider = true;', mode: 'text/javascript', theme: 'dracula',
      readOnly: true, lineNumbers: true, styleActiveLine: true, matchBrackets: true,
      lineWrapping: false, viewportMargin: 12,
    }]);
    assert.ok(overlay?.querySelector('.agent-text-preview-codemirror .CodeMirror'));
    assert.match(overlay?.querySelector('.CodeMirror')?.getAttribute('aria-label') || '', /JavaScript source/);
    assert.ok(overlay?.querySelector('.agent-text-preview-content.is-code-editor'));
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

test('openTextPreviewPopup renders markdown from the detected MIME type without a Markdown extension', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => previewResponse({
    text: '# Read me\n\nA rendered document.',
    contentType: 'text/markdown; charset=utf-8',
  });
  try {
    await openTextPreviewPopup('/path/to/README');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay?.querySelector('.agent-text-preview-md h1'), 'Markdown MIME renders as a document');
    assert.equal(overlay?.querySelector('[data-preview-kind]')?.dataset.previewKind, 'markdown');
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup shows a download-first card for unknown binary files', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => previewResponse({
    bytes: new Uint8Array([0x00, 0x02, 0xff, 0x10]),
    contentType: 'application/octet-stream',
  });
  try {
    await openTextPreviewPopup('/path/to/archive.data');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay?.querySelector('.agent-text-preview-binary'), 'binary file gets a safe file card');
    assert.equal(overlay?.querySelector('pre'), null, 'binary bytes never enter the text code surface');
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

// ---------- header-first classification ----------

test('openTextPreviewPopup classifies image from Content-Type without reading the body', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  let arrayBufferCalls = 0;
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => {
      const n = name.toLowerCase();
      if (n === 'content-type') return 'image/png';
      if (n === 'content-length') return '2048576';
      return null;
    } },
    arrayBuffer: async () => { arrayBufferCalls++; return new ArrayBuffer(0); },
  });
  try {
    await openTextPreviewPopup('/path/to/photo.dat');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay?.querySelector('.agent-text-preview-image'), 'image preview rendered from header');
    assert.equal(arrayBufferCalls, 0, 'body never read for media classification');
    const badge = overlay.querySelector('[data-preview-kind]');
    assert.equal(badge.dataset.previewKind, 'image');
    assert.match(badge.textContent, /Image/);
    assert.match(badge.textContent, /2\.0 MB/);
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup classifies PDF from Content-Type without reading the body', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  let arrayBufferCalls = 0;
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => {
      const n = name.toLowerCase();
      if (n === 'content-type') return 'application/pdf';
      if (n === 'content-length') return '50000';
      return null;
    } },
    arrayBuffer: async () => { arrayBufferCalls++; return new ArrayBuffer(0); },
  });
  try {
    await openTextPreviewPopup('/path/to/doc.dat');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay?.querySelector('.agent-text-preview-pdf'), 'PDF iframe rendered from header');
    assert.equal(arrayBufferCalls, 0, 'body never read for PDF classification');
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup uses Content-Length for the size badge on media', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => {
      const n = name.toLowerCase();
      if (n === 'content-type') return 'audio/mpeg';
      if (n === 'content-length') return '1048576';
      return null;
    } },
    arrayBuffer: async () => new ArrayBuffer(0),
  });
  try {
    await openTextPreviewPopup('/path/to/clip.dat');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    const badge = overlay.querySelector('[data-preview-kind]');
    assert.equal(badge.dataset.previewKind, 'audio');
    assert.match(badge.textContent, /1\.0 MB/);
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

// ---------- abort-on-close ----------

test('openTextPreviewPopup aborts the in-flight fetch when closed', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  let abortSignal = null;
  let abortFired = false;
  globalThis.fetch = async (url, opts) => {
    abortSignal = opts?.signal;
    abortSignal.addEventListener('abort', () => { abortFired = true; });
    // Never resolves — simulates a slow download.
    return new Promise(() => {});
  };
  try {
    const popupPromise = openTextPreviewPopup('/path/to/big.bin');
    // Let the fetch start.
    await new Promise((r) => setTimeout(r, 10));
    assert.ok(abortSignal, 'fetch received an AbortSignal');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay, 'overlay visible while loading');
    overlay.querySelector('.media-zoom-close').click();
    await new Promise((r) => setTimeout(r, 10));
    assert.ok(abortFired, 'abort signal fired on close');
    assert.equal(document.querySelector('.agent-text-preview-overlay'), null, 'overlay removed');
    // Swallow the unhandled rejection from the aborted popup.
    popupPromise.catch(() => {});
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

// ---------- loader path ----------

test('openTextPreviewPopup injects CodeMirror vendor scripts for text files', async () => {
  const dom = makeDom();
  const origFetch = globalThis.fetch;
  const injectedScripts = [];
  const origAppend = dom.window.document.head.append;
  dom.window.document.head.append = function (...nodes) {
    for (const n of nodes) {
      if (n.tagName === 'SCRIPT' && n.src) injectedScripts.push(n.src);
    }
    return origAppend.apply(this, nodes);
  };
  globalThis.fetch = async () => previewResponse({ text: 'const x = 1;', contentType: 'text/plain' });
  try {
    const popupPromise = openTextPreviewPopup('/path/to/script.js');
    await new Promise((r) => setTimeout(r, 20));
    // The loader injects the core CodeMirror script from /vendor/codemirror/.
    // Mode scripts load in parallel after core succeeds; in jsdom the core
    // script fails first, so only the core src is observed here.
    assert.ok(injectedScripts.some((s) => s.includes('/vendor/codemirror/codemirror.min.js')),
      `core script injected from vendor path, got: ${injectedScripts.join(', ')}`);
    // Close to clean up; the loader will reject due to jsdom script failure.
    const overlay = document.querySelector('.agent-text-preview-overlay');
    if (overlay) overlay.querySelector('.media-zoom-close').click();
    popupPromise.catch(() => {});
    await new Promise((r) => setTimeout(r, 20));
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup renders the <pre><code> fallback when the CodeMirror loader rejects', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  globalThis.fetch = async () => previewResponse({ text: 'const x = 1;', contentType: 'text/plain' });
  try {
    await openTextPreviewPopup('/path/to/script.js');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    // Fallback: <pre><code class="language-javascript"> rendered since
    // jsdom simulates script load failure.
    const pre = overlay?.querySelector('pre.agent-text-preview-code code');
    assert.ok(pre, 'fallback pre>code rendered');
    assert.match(pre.className, /language-javascript/, 'hljs class from the single language map');
    assert.match(pre.textContent, /const x = 1/);
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

// ---------- bounded stream read ----------

test('openTextPreviewPopup reads at most 4 KiB of an uninformative body and cancels the stream reader', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  // 64 KiB of 'A' delivered in 8 KiB chunks; the bounded reader must stop
  // after the first chunk (sliced to 4096) and cancel the stream.
  const chunkSize = 8192;
  let reads = 0;
  let cancelled = false;
  const reader = {
    read: async () => {
      reads++;
      return { done: false, value: new Uint8Array(chunkSize).fill(0x41) };
    },
    cancel: async () => { cancelled = true; },
  };
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => name.toLowerCase() === 'content-type' ? 'application/octet-stream' : null },
    body: { getReader: () => reader },
  });
  try {
    await openTextPreviewPopup('/path/to/large.dat');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay, 'overlay created');
    assert.ok(cancelled, 'stream reader was cancelled after the bounded read');
    // 4096 byte limit / 8192 chunk size => the first chunk is sliced and the
    // reader stops, so only one read call consumes data.
    assert.equal(reads, 1, 'reader stopped after the bound was reached');
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});

test('openTextPreviewPopup never reads the body when the header identifies video media', async () => {
  makeDom();
  const origFetch = globalThis.fetch;
  let readerCreated = false;
  let arrayBufferCalls = 0;
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: { get: (name) => {
      const n = name.toLowerCase();
      if (n === 'content-type') return 'video/mp4';
      if (n === 'content-length') return '10485760';
      return null;
    } },
    body: { getReader: () => { readerCreated = true; return { read: async () => ({ done: true }), cancel: async () => {} }; } },
    arrayBuffer: async () => { arrayBufferCalls++; return new ArrayBuffer(0); },
  });
  try {
    await openTextPreviewPopup('/path/to/clip.dat');
    const overlay = document.querySelector('.agent-text-preview-overlay');
    assert.ok(overlay?.querySelector('.agent-text-preview-video-stage video'), 'video preview rendered from header');
    assert.equal(readerCreated, false, 'stream reader never created for media');
    assert.equal(arrayBufferCalls, 0, 'arrayBuffer never called for media');
    overlay.remove();
  } finally {
    globalThis.fetch = origFetch;
    cleanup();
  }
});
