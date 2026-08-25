import assert from 'node:assert/strict';
import { test } from 'node:test';

import { renderMarkdown, parseBlocks } from './js/markdown.js';

// Tests for CommonMark + GFM features that the old custom parser did not
// support. These verify the micromark-backed renderer handles them correctly.

test('nested lists render with proper nesting', () => {
  const html = renderMarkdown('- a\n  - b\n  - c\n- d\n');
  assert.match(html, /<ul>/);
  assert.match(html, /<li>a\n<ul>/);
  assert.match(html, /<li>b<\/li>/);
  assert.match(html, /<li>c<\/li>/);
  assert.match(html, /<li>d<\/li>/);
});

test('reference-style links resolve', () => {
  const html = renderMarkdown('[x][1]\n\n[1]: https://example.com\n');
  assert.match(html, /<a href="https:\/\/example\.com"[^>]*>x<\/a>/);
});

test('autolink renders as link', () => {
  const html = renderMarkdown('<https://example.com>\n');
  assert.match(html, /<a href="https:\/\/example\.com"/);
});

test('GFM strikethrough renders as <del>', () => {
  const html = renderMarkdown('~~strike~~\n');
  assert.match(html, /<del>strike<\/del>/);
});

test('GFM task list renders without native <input> controls', () => {
  const html = renderMarkdown('- [x] done\n- [ ] todo\n');
  assert.match(html, /task-checkbox/);
  assert.match(html, /data-checked="true"/);
  assert.match(html, /data-checked="false"/);
  assert.doesNotMatch(html, /<input/);
});

test('absolute path link renders as clickable <a>', () => {
  const html = renderMarkdown('[app.py](/abs/path/app.py:12)\n');
  assert.match(html, /<a href="\/abs\/path\/app\.py:12">app\.py<\/a>/);
});

test('absolute path link with spaces in angle brackets', () => {
  const html = renderMarkdown('[My Report.md](</abs/path/My Project/My Report.md:3>)\n');
  assert.match(html, /<a href="\/abs\/path\/My%20Project\/My%20Report\.md:3">/);
});

test('nested emphasis renders correctly', () => {
  const html = renderMarkdown('**bold *and italic* bold**\n');
  assert.match(html, /<strong>bold <em>and italic<\/em> bold<\/strong>/);
});

test('external links get target="_blank" rel="noopener"', () => {
  const html = renderMarkdown('[x](https://example.com)\n');
  assert.match(html, /target="_blank"/);
  assert.match(html, /rel="noopener"/);
});

test('absolute path links do not get target="_blank"', () => {
  const html = renderMarkdown('[app.py](/abs/path/app.py:12)\n');
  assert.doesNotMatch(html, /target="_blank"/);
});

test('GFM table renders with thead and tbody', () => {
  const html = renderMarkdown('| a | b |\n|---|---|\n| 1 | 2 |\n');
  assert.match(html, /<table[^>]*>/);
  assert.match(html, /<thead>/);
  assert.match(html, /<tbody>/);
  assert.match(html, /<th>a<\/th>/);
  assert.match(html, /<td>1<\/td>/);
});

test('GFM autolink literal renders bare URLs as links', () => {
  const html = renderMarkdown('visit https://example.com today\n');
  assert.match(html, /<a href="https:\/\/example\.com"/);
});

test('code span with backticks', () => {
  const html = renderMarkdown('inline `code` here\n');
  assert.match(html, /<code>code<\/code>/);
});

test('fenced code with language class', () => {
  const html = renderMarkdown('```bash\necho hello\n```\n');
  assert.match(html, /class="language-bash"/);
});

test('fenced code without language has no language class', () => {
  const html = renderMarkdown('```\necho hello\n```\n');
  assert.doesNotMatch(html, /language-/);
});

test('data-start/data-end present on all top-level blocks', () => {
  const blocks = parseBlocks('# Hello\n\nworld\n\n- a\n- b\n');
  for (const b of blocks) {
    assert.match(b.html, /data-start="\d+"/);
    assert.match(b.html, /data-end="\d+"/);
  }
});
