import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { parseBlocks, renderMarkdown } from './js/markdown.js';
import { incrementalRender } from './js/incremental-render.js';

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

// ---------- parseBlocks: byte-range anchors ----------

test('parseBlocks emits data-start/data-end on every top-level block', () => {
  const blocks = parseBlocks('hello\n\n```js\nconst a = 1;\n```\n\nworld');
  assert.equal(blocks.length, 3, 'p + pre + p');
  assert.equal(blocks[0].start, 0);
  assert.equal(blocks[0].end, 5, 'p covers "hello"');
  assert.match(blocks[0].html, /data-start="0" data-end="5"/);
  // pre: fenceStart=7 (line 2), fenceEnd=29 (end of closing ```)
  assert.match(blocks[1].html, /data-start="7" data-end="29"/);
  // p "world": start=31, end=36
  assert.match(blocks[2].html, /data-start="31" data-end="36"/);
});

test('parseBlocks marks incomplete mermaid fence as data-complete="false"', () => {
  // No closing fence — incomplete.
  const blocks = parseBlocks('```mermaid\nflowchart TD\n A-->B');
  assert.equal(blocks.length, 1);
  assert.match(blocks[0].html, /data-complete="false"/);
});

test('parseBlocks marks complete mermaid fence as data-complete="true"', () => {
  const blocks = parseBlocks('```mermaid\nflowchart TD\n A-->B\n```');
  assert.equal(blocks.length, 1);
  assert.match(blocks[0].html, /data-complete="true"/);
});

test('parseBlocks emits data-complete on fenced code blocks', () => {
  // Complete fence → data-complete="true"
  const complete = parseBlocks('```js\nconst a = 1;\n```');
  assert.equal(complete.length, 1);
  assert.match(complete[0].html, /data-complete="true"/);
  // Incomplete fence (no closing ```) → data-complete="false"
  const incomplete = parseBlocks('```js\nconst a = 1;');
  assert.equal(incomplete.length, 1);
  assert.match(incomplete[0].html, /data-complete="false"/);
});

test('parseBlocks emits language-xxx class on <code> when lang is specified', () => {
  const blocks = parseBlocks('```bash\necho hello\n```');
  assert.equal(blocks.length, 1);
  assert.match(blocks[0].html, /<code class="language-bash">/);
});

test('parseBlocks omits language class when no lang is given (auto-detect)', () => {
  const blocks = parseBlocks('```\necho hello\n```');
  assert.equal(blocks.length, 1);
  assert.match(blocks[0].html, /<code>/);
  assert.doesNotMatch(blocks[0].html, /language-/);
});

test('parseBlocks emits data-complete="true" on indented code blocks', () => {
  const blocks = parseBlocks('    const a = 1;');
  assert.equal(blocks.length, 1);
  assert.match(blocks[0].html, /data-complete="true"/);
});

test('renderMarkdown is backward compatible (produces same blocks joined)', () => {
  const html = renderMarkdown('hello\n\nworld');
  assert.match(html, /<p data-start="0"/);
  assert.match(html, /<p data-start="7"/);
});

// ---------- incrementalRender: diff-based DOM update ----------

test('incrementalRender returns only new or changed block elements', () => {
  const dom = makeDom();
  try {
    const container = document.createElement('div');

    // First render: the paragraph is new.
    const first = incrementalRender(container, 'hello');
    assert.equal(first.length, 1, 'first render yields the one created block');
    assert.equal(container.children.length, 1);

    // Delta grows the same paragraph: the block is re-created (changed).
    const second = incrementalRender(container, 'hello world');
    assert.equal(second.length, 1, 'grown block is reported as changed');
    assert.notEqual(second[0], first[0], 'changed block is a fresh element');
    assert.equal(container.children.length, 1, 'still one block in the DOM');

    // Unchanged delta: nothing new to enhance.
    const third = incrementalRender(container, 'hello world');
    assert.equal(third.length, 0, 'unchanged blocks produce no enhancement targets');

    // A new block appends; only the new block is reported.
    const fourth = incrementalRender(container, 'hello world\n\n```js\nconst a = 1;\n```');
    assert.equal(fourth.length, 1, 'only the new code block is reported');
    assert.match(fourth[0].tagName, /^PRE$/i);

    // A settled block stays untouched: the reported set excludes it.
    const fifth = incrementalRender(container, 'hello world\n\n```js\nconst a = 1;\n```\n\nbye');
    assert.equal(fifth.length, 1, 'only the trailing paragraph is reported');
    assert.equal(container.children.length, 3);
  } finally { cleanup(); }
});


test('incrementalRender first paint inserts all blocks', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    incrementalRender(c, 'hello\n\nworld');
    const blocks = c.querySelectorAll('[data-start]');
    assert.equal(blocks.length, 2, 'two <p> blocks');
    assert.equal(blocks[0].dataset.start, '0');
    assert.equal(blocks[1].dataset.start, '7');
  } finally { cleanup(); }
});

test('incrementalRender preserves unchanged blocks (same data-start + data-end)', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    // First paint: two blocks.
    incrementalRender(c, 'hello\n\nworld');
    const blocksBefore = [...c.children].filter((el) => el.dataset.start != null);
    assert.equal(blocksBefore.length, 2);
    const p1Before = blocksBefore[0];
    const p2Before = blocksBefore[1];

    // Second paint: append a third block. The first two should be preserved
    // (same DOM nodes — not replaced).
    incrementalRender(c, 'hello\n\nworld\n\nmore text');
    const blocksAfter = [...c.children].filter((el) => el.dataset.start != null);
    assert.equal(blocksAfter.length, 3);
    assert.strictEqual(blocksAfter[0], p1Before, 'unchanged block 1 must be the same DOM node');
    assert.strictEqual(blocksAfter[1], p2Before, 'unchanged block 2 must be the same DOM node');
    assert.ok(blocksAfter[2].dataset.start === '14', 'new block 3 inserted');
  } finally { cleanup(); }
});

test('incrementalRender re-renders blocks whose end grew (paragraph absorbing text)', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    // First paint: one paragraph "hello".
    incrementalRender(c, 'hello');
    const p1Before = c.querySelector('[data-start="0"]');
    assert.equal(p1Before.dataset.end, '5');

    // Second paint: paragraph grew to "hello world" (same start, bigger end).
    incrementalRender(c, 'hello world');
    const p1After = c.querySelector('[data-start="0"]');
    assert.notStrictEqual(p1After, p1Before, 'changed block must be a new DOM node');
    assert.equal(p1After.dataset.end, '11');
  } finally { cleanup(); }
});

test('incrementalRender locks mermaid block after fence closes (the ChatGPT pattern)', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');

    // Delta 1: fence opened, not closed yet.
    incrementalRender(c, 'intro\n\n```mermaid\nflowchart TD\n A-->B');
    let mermaidBlock = c.querySelector('.mermaid-block');
    assert.ok(mermaidBlock, 'mermaid placeholder exists');
    assert.equal(mermaidBlock.dataset.complete, 'false', 'incomplete fence');
    const mermaidStart = mermaidBlock.dataset.start;

    // Delta 2: fence closes. Block is re-rendered (end changed) with
    // data-complete="true".
    incrementalRender(c, 'intro\n\n```mermaid\nflowchart TD\n A-->B\n```');
    mermaidBlock = c.querySelector('.mermaid-block');
    assert.ok(mermaidBlock);
    assert.equal(mermaidBlock.dataset.complete, 'true', 'fence closed');
    assert.equal(mermaidBlock.dataset.start, mermaidStart, 'same start position');
    const mermaidAfterClose = mermaidBlock;

    // Delta 3: more text after the fence. The mermaid block must be
    // PRESERVED — same DOM node, not re-rendered. This is the "lock".
    incrementalRender(c, 'intro\n\n```mermaid\nflowchart TD\n A-->B\n```\n\nafter text');
    mermaidBlock = c.querySelector('.mermaid-block');
    assert.strictEqual(mermaidBlock, mermaidAfterClose, 'mermaid block must be locked (same DOM node)');
    assert.equal(mermaidBlock.dataset.complete, 'true', 'still complete');
    const afterP = c.querySelector('[data-start]');
    const lastBlock = c.querySelectorAll('[data-start]');
    // Should have: p(intro), mermaid, p(after text) = 3 blocks.
    assert.equal(lastBlock.length, 3, '3 blocks total');
  } finally { cleanup(); }
});

test('incrementalRender removes orphaned blocks', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    incrementalRender(c, 'hello\n\nworld');
    assert.equal(c.querySelectorAll('[data-start]').length, 2);

    // Replace with single block — the second should be removed.
    incrementalRender(c, 'only one');
    const blocks = c.querySelectorAll('[data-start]');
    assert.equal(blocks.length, 1);
    assert.equal(blocks[0].dataset.start, '0');
  } finally { cleanup(); }
});

test('incrementalRender handles empty raw (clears block children)', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    incrementalRender(c, 'hello');
    assert.equal(c.querySelectorAll('[data-start]').length, 1);
    incrementalRender(c, '');
    assert.equal(c.querySelectorAll('[data-start]').length, 0);
  } finally { cleanup(); }
});

test('incrementalRender preserves non-block children (thinking dots etc.)', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    const dots = document.createElement('span');
    dots.className = 'agent-thinking-dots';
    c.appendChild(dots);

    incrementalRender(c, 'hello');
    assert.ok(c.querySelector('.agent-thinking-dots'), 'non-block child preserved');
    assert.equal(c.querySelectorAll('[data-start]').length, 1, 'one block added');

    // Non-block child should still be there after incremental update.
    incrementalRender(c, 'hello world');
    assert.ok(c.querySelector('.agent-thinking-dots'), 'non-block child still there');
  } finally { cleanup(); }
});

test('incrementalRender handles headings, lists, and blockquotes with anchors', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    incrementalRender(c, '# Title\n\n- item 1\n- item 2\n\n> quote');
    // Use direct children — blockquote inner blocks also carry data-start
    // but are not top-level blocks.
    const blocks = [...c.children].filter((el) => el.dataset.start != null);
    assert.equal(blocks.length, 3, 'h1 + ul + blockquote');
    assert.equal(blocks[0].tagName, 'H1');
    assert.equal(blocks[1].tagName, 'UL');
    assert.equal(blocks[2].tagName, 'BLOCKQUOTE');
    for (const b of blocks) assert.ok(b.dataset.start != null);
  } finally { cleanup(); }
});

test('incrementalRender keeps scroll-wrapped tables as one anchored block', () => {
  const dom = makeDom();
  try {
    const container = document.createElement('div');
    const first = incrementalRender(container, '| a | b |\n|---|---|\n| 1 | 2 |');
    assert.equal(first.length, 1);
    assert.equal(first[0].className, 'markdown-table-scroll');
    assert.equal(container.children.length, 1);

    const second = incrementalRender(container, '| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |');
    assert.equal(second.length, 1);
    assert.equal(second[0].className, 'markdown-table-scroll');
    assert.notEqual(second[0], first[0]);
    assert.equal(container.children.length, 1);
  } finally { cleanup(); }
});

// Live round text is unbounded on purpose. incrementalRender must accept
// raw text much larger than the old 512KB cap (which used to truncate the
// user's view of long assistant turns mid-stream) without dropping or
// re-anchoring any block. The byte-range keys are derived from the raw
// source, so the largest legal anchor (data-end) is at the end of the
// input — which is fine as long as the underlying DOM string fields can
// hold it. This test simulates the worst case: a 2 MiB paragraph.
test('incrementalRender handles multi-MiB raw text without losing the head', () => {
  const dom = makeDom();
  try {
    const c = document.createElement('div');
    const filler = 'x'.repeat(2 * 1024 * 1024);
    const raw = `intro\n\n${filler}\n\nend`;
    const changed = incrementalRender(c, raw);
    assert.equal(changed.length, 3, 'all three blocks reported as new');
    // The first paragraph ("intro") must still be at start=0 end=5 — the
    // head must not be silently dropped by a hidden cap.
    const blocks = [...c.querySelectorAll('[data-start]')];
    assert.equal(blocks[0].dataset.start, '0');
    assert.equal(blocks[0].dataset.end, '5');
    // "intro" (5) + "\n\n" (2) + filler (2 MiB) + "\n\n" (2) = 5 + 2 + 2*1024*1024 + 2 = 2097161
    assert.equal(blocks[2].dataset.start, String(5 + 2 + filler.length + 2));
    assert.equal(blocks[2].dataset.end, String(5 + 2 + filler.length + 2 + 3));
  } finally { cleanup(); }
});
