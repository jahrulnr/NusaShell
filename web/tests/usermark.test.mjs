import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import { markedUser, withRefSentinels, applyRefHighlights, stripRefSentinels } from '../lib/usermark.js';
import { splitReplyQuote } from '../lib/turn.js';
import { referencesFromStored } from '../lib/references.js';

// markedUser is the owner-typed profile: GFM with single-newline breaks,
// no footnote extraction, no diagram mounting. parse() yields the
// pre-sanitize HTML string — enough to assert the profile's shape in
// Node, where the sanitize/DOM tail needs a real document.
const parse = (src) => markedUser.parse(src);

const ref = (kind, id, label, start, end) => ({ kind, id, label, start, end });
const enc = new TextEncoder();
const byteRefs = (text, spans) => spans.map(([kind, id, label]) => {
  const start = enc.encode(text.slice(0, text.indexOf(label))).length;
  return { kind, id, label, start, end: start + enc.encode(label).length };
});

test('bold, italic, code, and a link render', () => {
  const html = parse('**bold** and _it_ and `c` and [x](https://a.b)');
  assert.match(html, /<strong>bold<\/strong>/);
  assert.match(html, /<em>it<\/em>/);
  assert.match(html, /<code>c<\/code>/);
  assert.match(html, /<a href="https:\/\/a\.b">x<\/a>/);
});

test('a single newline becomes a line break (breaks: true)', () => {
  assert.match(parse('first\nsecond'), /first<br ?\/?>\s*second|first\s*<br[^>]*>second/);
});

test('a typed footnote definition stays literal text', () => {
  const html = parse('see [^a]\n\n[^a]: the note');
  assert.match(html, /\[\^a\]/);
  assert.doesNotMatch(html, /md-fnref|md-footnotes|<sup/);
});

test('a mermaid fence stays a code block — never a mounted diagram', () => {
  const html = parse('```mermaid\ngraph TD;\nA-->B;\n```');
  assert.match(html, /class="codeblock"/);
  assert.match(html, /data-lang="mermaid"/);
  assert.doesNotMatch(html, /data-diagram/);
});

test('raw HTML passes through to the sanitizer unchanged', () => {
  // The script element is sanitized downstream by the same DOMPurify
  // allowlist assistant prose uses — the profile must not pre-mangle it.
  assert.match(parse('<script>alert(1)</script>'), /<script>/);
});

test('sentinels wrap each label in a copy of the source', () => {
  const src = 'hi @one and ./f.go bye';
  const refs = [ref('conversation', 'c1', '@one', 3, 7), ref('file', 'f1', './f.go', 12, 18)];
  const out = withRefSentinels(src, refs);
  assert.equal(out, 'hi \uE0000\uE001@one\uE0000\uE002 and \uE0001\uE001./f.go\uE0001\uE002 bye');
  assert.equal(src, 'hi @one and ./f.go bye', 'source is a copy, never the raw text');
});

test('sentinels survive the parse beside and inside markdown', () => {
  const out = parse(withRefSentinels('**b** @x `code @y`', [ref('conversation', 'c', '@x', 6, 8), ref('conversation', 'd', '@y', 15, 17)]));
  assert.match(out, /\uE0000\uE001@x\uE0000\uE002/);
  assert.match(out, /<code>code \uE0001\uE001@y\uE0001\uE002<\/code>/);
});

test('an emoji label and a label after a reply quote line up', () => {
  const text = 'Ana: [In reply to: hi 👋]\nask about @🧠proj now';
  const reply = splitReplyQuote(text);
  assert.ok(reply);
  const stored = referencesFromStored(text, byteRefs(text, [['project', 'p1', '@🧠proj']]));
  const shown = stored.filter((r) => r.start >= reply.head).map((r) => ({ ...r, start: r.start - reply.head, end: r.end - reply.head }));
  assert.equal(shown.length, 1);
  const out = withRefSentinels(reply.body, shown);
  assert.equal(out, 'ask about \uE0000\uE001@🧠proj\uE0000\uE002 now');
});

test('a ref clipped by the quote head gets no sentinel', () => {
  const text = '[In reply to: @gone]\nbody @here';
  const reply = splitReplyQuote(text);
  const stored = referencesFromStored(text, [
    { kind: 'conversation', id: 'a', label: '@gone', start: 14, end: 20 },
    { kind: 'conversation', id: 'b', label: '@here', start: 26, end: 31 },
  ]);
  const shown = stored.filter((r) => r.start >= reply.head).map((r) => ({ ...r, start: r.start - reply.head, end: r.end - reply.head }));
  assert.equal(shown.length, 1, 'the ref inside the quote head drops');
  assert.equal(withRefSentinels(reply.body, shown), 'body \uE0000\uE001@here\uE0000\uE002');
});

test('overlapping and out-of-range refs produce no sentinel', () => {
  const src = 'ab @x cd';
  const out = withRefSentinels(src, [
    ref('conversation', 'a', '@x', 3, 5),
    ref('file', 'b', 'x c', 4, 7),   // overlaps the first — skipped
    ref('file', 'c', 'zz', 90, 92),  // out of range — skipped
  ]);
  assert.equal(out, 'ab \uE0000\uE001@x\uE0000\uE002 cd');
});

test('sentinels inside a link target percent-encode into the href', () => {
  // The label in a URL stays the typed text; the highlight pass strips
  // both the literal and the percent-encoded mark shapes from the
  // attribute so the href keeps what the user wrote.
  const out = parse(withRefSentinels('[see](@t)', [ref('conversation', 'c', '@t', 6, 8)]));
  assert.match(out, /%EE%80%800%EE%80%81@t%EE%80%800%EE%80%82/);
});

test('applyRefHighlights needs a document — exported for the DOM pass', () => {
  assert.equal(typeof applyRefHighlights, 'function');
  assert.doesNotThrow(() => applyRefHighlights({ querySelectorAll: () => [] }, []));
});

test('stripRefSentinels leaves the label as typed', () => {
  const marked = withRefSentinels('sip, si @cat udah selesai', [{ start: 8, end: 12, kind: 'conversation', id: 'c_1' }]);
  assert.notEqual(marked, 'sip, si @cat udah selesai');
  assert.equal(stripRefSentinels(marked), 'sip, si @cat udah selesai');
  assert.equal(stripRefSentinels('https://x.test/%EE%80%800%EE%80%81a%EE%80%800%EE%80%82'), 'https://x.test/a');
});
