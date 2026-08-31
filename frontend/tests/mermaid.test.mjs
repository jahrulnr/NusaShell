import assert from 'node:assert/strict';
import { test } from 'node:test';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';

import { renderMarkdown } from '../js/markdown.js';
import { renderMermaidDiagrams } from '../js/mermaid-render.js';
import { incrementalRender } from '../js/incremental-render.js';

test('markdown emits a mermaid placeholder (no SVG) so streaming deltas stay cheap', () => {
  const html = renderMarkdown('intro\n\n```mermaid\nflowchart TD\n A-->B\n```\n\nend');
  assert.match(html, /class="mermaid-block"/);
  assert.match(html, /class="mermaid-src"/);
  // The raw diagram source is preserved (escaped) for the renderer to pick up.
  assert.match(html, /flowchart TD/);
  // Crucially, renderMarkdown must NOT render to SVG — that would run on every
  // live delta. SVG is produced only later by renderMermaidDiagrams.
  assert.doesNotMatch(html, /<svg/);
  // A normal code fence is still a plain code block, not a mermaid block.
  const plain = renderMarkdown('```js\nconst a = 1;\n```');
  assert.match(plain, /<pre[^>]*><code[^>]*>/);
  assert.doesNotMatch(plain, /mermaid-block/);
});

test('sample conversation fixture renders without throwing; mermaid messages produce blocks', async () => {
  const raw = await readFile(new URL('../testdata/sample-conversation.json', import.meta.url), 'utf8');
  const conv = JSON.parse(raw);
  assert.ok(Array.isArray(conv.Messages) && conv.Messages.length > 0);
  let mermaidBlocks = 0;
  for (const m of conv.Messages) {
    const html = renderMarkdown(m.Content || ''); // must never throw on real content
    if (/mermaid-block/.test(html)) mermaidBlocks += 1;
  }
  // The fixture includes one valid and one invalid mermaid message.
  assert.equal(mermaidBlocks, 2, 'both mermaid messages should yield placeholders');
});

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  global.window = dom.window;
  global.document = dom.window.document;
  return dom;
}

test('renderMermaidDiagrams renders valid diagrams once and dedups on repeat', async () => {
  const dom = makeDom();
  let renderCount = 0;
  dom.window.mermaid = {
    initialize() {},
    async parse(code) { return !/INVALID/.test(code); },
    async render(id, code) { renderCount += 1; return { svg: `<svg data-mmd="${id}"><g>ok</g></svg>` }; },
  };
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="mermaid-block"><pre class="mermaid-src">flowchart TD\n A--&gt;B</pre></div>';
    await renderMermaidDiagrams(c);
    const block = c.querySelector('.mermaid-block');
    assert.ok(block.querySelector('svg'), 'valid diagram should render to SVG');
    assert.ok(block.dataset.rendered, 'rendered block should be marked');
    assert.equal(renderCount, 1);
    // A second pass over the same DOM must not re-render (delta-safe idempotency).
    await renderMermaidDiagrams(c);
    assert.equal(renderCount, 1, 'unchanged diagram must not re-render');
  } finally {
    delete global.window;
    delete global.document;
  }
});

test('renderMermaidDiagrams falls back to source for invalid mermaid without throwing', async () => {
  const dom = makeDom();
  let renderCalled = false;
  dom.window.mermaid = {
    initialize() {},
    async parse(code) { return !/INVALID/.test(code); },
    async render() { renderCalled = true; return { svg: '<svg></svg>' }; },
  };
  try {
    const c = document.createElement('div');
    c.innerHTML = '<div class="mermaid-block"><pre class="mermaid-src">INVALID @@@ -&gt;-&gt;</pre></div>';
    await renderMermaidDiagrams(c); // must not throw
    const block = c.querySelector('.mermaid-block');
    assert.ok(block.classList.contains('mermaid-error'), 'invalid diagram should get the error class');
    assert.equal(renderCalled, false, 'render must be skipped when parse reports invalid');
    // The raw source is preserved so the user still sees what the model produced.
    assert.match(block.textContent, /INVALID/);
    assert.ok(block.querySelector('.mermaid-error-note'), 'shows an explanatory note');
  } finally {
    delete global.window;
    delete global.document;
  }
});

// Per-delta idempotency: a Mermaid block whose fence just closed must be
// rendered to SVG the very next enhancement pass (no debounce), and a burst
// of text deltas that follows must not re-render or replace the SVG. This
// mirrors what scheduleLiveMermaid does for the live thread: call
// renderMermaidDiagrams on the changed node synchronously, with no setTimeout
// — and rely on the content-hash lock inside the renderer to make repeated
// calls free.
test('mermaid renders per-delta when fence closes and stays idempotent under a text burst', async () => {
  const dom = makeDom();
  let renderCount = 0;
  let parseCount = 0;
  dom.window.mermaid = {
    initialize() {},
    async parse(code) { parseCount += 1; return !/INVALID/.test(code); },
    async render(id, code) { renderCount += 1; return { svg: `<svg data-mmd="${id}"><g>ok</g></svg>` }; },
  };
  try {
    const container = document.createElement('div');

    // Delta 1: fence opens, source grows. The block is incomplete —
    // renderMermaidDiagrams must skip it (data-complete="false") even when
    // called from the per-delta path.
    incrementalRender(container, 'intro\n\n```mermaid\nflowchart TD\n A-->B');
    await renderMermaidDiagrams(container);
    let block = container.querySelector('.mermaid-block');
    assert.ok(block, 'mermaid block exists');
    assert.equal(block.dataset.complete, 'false', 'fence still open');
    assert.equal(renderCount, 0, 'must not render while fence is open');
    assert.equal(parseCount, 0, 'must not parse while fence is open');

    // Delta 2: fence closes. The next per-delta renderMermaidDiagrams MUST
    // turn the placeholder into SVG synchronously — this is the bug fix:
    // before the fix, the only place mermaid rendered was a settle point,
    // so the user saw the raw ```mermaid``` placeholder until the turn
    // sealed.
    incrementalRender(container, 'intro\n\n```mermaid\nflowchart TD\n A-->B\n```');
    await renderMermaidDiagrams(container);
    block = container.querySelector('.mermaid-block');
    assert.equal(block.dataset.complete, 'true', 'fence closed');
    assert.ok(block.querySelector('svg'), 'mermaid SVG is in the DOM after fence close');
    const renderedSvg = block.querySelector('svg');
    assert.ok(block.dataset.rendered, 'rendered flag set on the block');
    assert.equal(renderCount, 1, 'one render for the closed fence');
    assert.equal(parseCount, 1, 'one parse for the closed fence');

    // Delta 3..N: text burst after the fence. The mermaid block is byte-
    // range-locked by incrementalRender (start+end unchanged), so it is NOT
    // in the changed[] set; even if it were, the renderer's content-hash
    // lock short-circuits any second call. Either way, no re-render, no
    // replacement of the settled SVG.
    for (const tail of ['\n\nafter 1', '\n\nafter 2', '\n\nafter 3', '\n\nafter 4']) {
      incrementalRender(container, 'intro\n\n```mermaid\nflowchart TD\n A-->B\n```' + tail);
      await renderMermaidDiagrams(container);
      const after = container.querySelector('.mermaid-block svg');
      assert.strictEqual(after, renderedSvg, 'SVG identity preserved across text deltas');
    }
    assert.equal(renderCount, 1, 'text burst must not trigger additional renders');
    assert.equal(parseCount, 1, 'text burst must not trigger additional parses');
  } finally {
    delete global.window;
    delete global.document;
  }
});
