import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderMarkdown } from '../js/markdown.js';
import { highlightCode } from '../js/highlight-render.js';

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  global.window = dom.window;
  global.document = dom.window.document;
  return dom;
}

function cleanup() {
  delete global.window;
  delete global.document;
}

// Build a container from raw markdown so the DOM mirrors what the renderer
// produces in the real app (including data-complete and language-xxx class).
function containerFromMarkdown(src) {
  const c = document.createElement('div');
  c.innerHTML = renderMarkdown(src);
  return c;
}

test('markdown + highlight: complete code block gets highlighted via hljs.highlightElement', async () => {
  const dom = makeDom();
  const highlighted = [];
  dom.window.hljs = {
    highlightElement(el) { highlighted.push(el.textContent); },
  };
  try {
    const c = containerFromMarkdown('```js\nconst a = 1;\n```');
    await highlightCode(c);
    assert.equal(highlighted.length, 1, 'one code block highlighted');
    assert.match(highlighted[0], /const a = 1/);
    const code = c.querySelector('pre > code');
    assert.ok(code.dataset.highlighted, 'block marked as highlighted');
  } finally { cleanup(); }
});

test('highlightCode skips incomplete fences (data-complete="false")', async () => {
  const dom = makeDom();
  const highlighted = [];
  dom.window.hljs = {
    highlightElement(el) { highlighted.push(el); },
  };
  try {
    // No closing fence → data-complete="false"
    const c = containerFromMarkdown('```js\nconst a = 1;');
    await highlightCode(c);
    assert.equal(highlighted.length, 0, 'incomplete fence must not be highlighted');
    const code = c.querySelector('pre > code');
    assert.ok(!code.dataset.highlighted, 'incomplete block not marked');
  } finally { cleanup(); }
});

test('highlightCode is idempotent — repeated calls do not re-highlight', async () => {
  const dom = makeDom();
  let count = 0;
  dom.window.hljs = {
    highlightElement(el) { count += 1; },
  };
  try {
    const c = containerFromMarkdown('```bash\necho hi\n```');
    await highlightCode(c);
    assert.equal(count, 1);
    // Second pass over the same DOM must not re-highlight (delta-safe).
    await highlightCode(c);
    assert.equal(count, 1, 'unchanged block must not re-highlight');
  } finally { cleanup(); }
});

test('highlightCode skips empty code blocks', async () => {
  const dom = makeDom();
  const highlighted = [];
  dom.window.hljs = {
    highlightElement(el) { highlighted.push(el); },
  };
  try {
    const c = document.createElement('div');
    c.innerHTML = '<pre data-complete="true"><code>   </code></pre>';
    await highlightCode(c);
    assert.equal(highlighted.length, 0, 'empty block skipped');
  } finally { cleanup(); }
});

test('highlightCode does not touch mermaid blocks', async () => {
  const dom = makeDom();
  const highlighted = [];
  dom.window.hljs = {
    highlightElement(el) { highlighted.push(el); },
  };
  try {
    const c = containerFromMarkdown('```mermaid\nflowchart TD\n A-->B\n```');
    await highlightCode(c);
    assert.equal(highlighted.length, 0, 'mermaid placeholder has no pre>code child');
    assert.ok(c.querySelector('.mermaid-block'), 'mermaid block intact');
  } finally { cleanup(); }
});

test('highlightCode is a no-op when highlight.js fails to load', async () => {
  const dom = makeDom();
  // No window.hljs and the script src will fail in JSDOM. Override
  // createElement to make the script error immediately.
  const origCreate = document.createElement.bind(document);
  dom.window.document.createElement = (tag) => {
    const el = origCreate(tag);
    if (tag === 'script') {
      // Simulate load failure on next microtask.
      setTimeout(() => el.onerror?.(new Error('no network')), 0);
    }
    return el;
  };
  try {
    const c = containerFromMarkdown('```js\nconst a = 1;\n```');
    await highlightCode(c); // must not throw
    const code = c.querySelector('pre > code');
    assert.ok(!code.dataset.highlighted, 'block left unhighlighted on load failure');
    // Raw escaped source is still visible.
    assert.match(code.textContent, /const a = 1/);
  } finally { cleanup(); }
});

test('highlightCode handles highlightElement throwing (leaves raw source)', async () => {
  const dom = makeDom();
  dom.window.hljs = {
    highlightElement() { throw new Error('boom'); },
  };
  try {
    const c = containerFromMarkdown('```py\nprint("hi")\n```');
    await highlightCode(c); // must not throw
    const code = c.querySelector('pre > code');
    assert.ok(!code.dataset.highlighted, 'failed block not marked');
    assert.match(code.textContent, /print/);
  } finally { cleanup(); }
});

test('highlightCode highlights multiple blocks in one container', async () => {
  const dom = makeDom();
  const highlighted = [];
  dom.window.hljs = {
    highlightElement(el) { highlighted.push(el.textContent); },
  };
  try {
    const c = containerFromMarkdown(
      '```bash\necho a\n```\n\n```html\n<div>b</div>\n```\n\n```\nplain\n```',
    );
    await highlightCode(c);
    assert.equal(highlighted.length, 3, 'all three complete blocks highlighted');
  } finally { cleanup(); }
});
