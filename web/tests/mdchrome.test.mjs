// The renderer's chrome is told apart from a message's own markup by a nonce
// drawn per render pass. These tests drive the rules over a tiny element
// stand-in: what matters is which attributes and classes survive, and that a
// box wearing the attribute without the renderer's shape is never touched.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import './env.mjs';
import {
  CHROME_ATTRS, NONCE_ATTR, newNonce, stripForeignChrome, diagramParts,
} from '../lib/mdchrome.js';

// el is the slice of Element the strip and the diagram guard use: attributes,
// a class string, children, and a descendant query.
function el(tag, attrs = {}, kids = []) {
  const map = new Map(Object.entries(attrs).map(([k, v]) => [k, String(v)]));
  const self = {
    tag,
    kids,
    getAttribute: (n) => (map.has(n) ? map.get(n) : null),
    hasAttribute: (n) => map.has(n),
    setAttribute: (n, v) => { map.set(n, String(v)); },
    removeAttribute: (n) => { map.delete(n); },
    get textContent() { return kids.map((k) => (typeof k === 'string' ? k : k.textContent)).join(''); },
    querySelectorAll(sel) {
      assert.equal(sel, '*', 'the strip walks every element');
      const out = [];
      const walk = (n) => { for (const k of n.kids) if (typeof k !== 'string') { out.push(k); walk(k); } };
      walk(self);
      return out;
    },
    querySelector(sel) {
      const parts = sel.trim().split(/\s+/);
      const match = (n, p) => (p.startsWith('.') ? (n.getAttribute('class') || '').split(/\s+/).includes(p.slice(1)) : n.tag === p);
      const find = (n, i) => {
        for (const k of n.kids) {
          if (typeof k === 'string') continue;
          if (match(k, parts[i])) { if (i === parts.length - 1) return k; const deep = find(k, i + 1); if (deep) return deep; }
          const under = find(k, i);
          if (under) return under;
        }
        return null;
      };
      return find(self, 0);
    },
  };
  return self;
}

const attrsOf = (node) => CHROME_ATTRS.filter((a) => node.hasAttribute(a));
const classesOf = (node) => (node.getAttribute('class') || '').split(/\s+/).filter(Boolean);

test('a message that writes the renderer’s chrome loses it, and keeps its words', () => {
  const nonce = newNonce();
  const text = el('div', {}, [el('p', {}, ['<div data-diagram> dijual di sini']),
    el('div', { class: 'diagram', 'data-diagram': '' }, [el('div', {}, ['not a canvas'])]),
    el('div', { class: 'codeblock' }, [el('button', { class: 'copybtn', 'data-copy': '' }, ['Copy'])]),
    el('span', { class: 'md-media', 'data-media': 'a.png', 'data-kind': 'image' }, ['shot']),
    el('button', { 'data-fn': 'x' }, ['1'])]);
  stripForeignChrome(text, nonce);
  const [, box, code, media, fn] = text.kids;
  assert.deepEqual(attrsOf(box), [], 'the diagram attribute does not survive');
  assert.deepEqual(classesOf(box), [], 'nor the class that made it look like one');
  assert.deepEqual(attrsOf(code.kids[0]), [], 'a forged copy button is just a button');
  assert.deepEqual(classesOf(code.kids[0]), [], 'and loses the class that styled it');
  assert.deepEqual(attrsOf(media), []);
  assert.deepEqual(attrsOf(fn), []);
  assert.equal(box.kids[0].textContent, 'not a canvas', 'the words the message wrote stay');
});

test('the renderer’s own chrome keeps its attributes and classes', () => {
  const nonce = newNonce();
  const box = el('div', { class: 'diagram', 'data-diagram': '', [NONCE_ATTR]: nonce });
  const button = el('button', { class: 'copybtn', 'data-copy': '', [NONCE_ATTR]: nonce });
  const root = el('div', {}, [box, el('div', { class: 'codeblock', [NONCE_ATTR]: nonce }, [button])]);
  stripForeignChrome(root, nonce);
  assert.deepEqual(attrsOf(box), ['data-diagram']);
  assert.deepEqual(classesOf(box), ['diagram']);
  assert.deepEqual(attrsOf(button), ['data-copy']);
  assert.deepEqual(classesOf(button), ['copybtn']);
});

test('a nonce from another render is not ours', () => {
  const old = newNonce();
  const now = newNonce();
  assert.notEqual(old, now, 'every pass draws a new mark');
  const box = el('div', { class: 'diagram', 'data-diagram': '', [NONCE_ATTR]: old });
  stripForeignChrome(el('div', {}, [box]), now);
  assert.deepEqual(attrsOf(box), [], 'a box marked by an earlier render is not adopted');
  assert.deepEqual(classesOf(box), []);
  assert.equal(box.hasAttribute(NONCE_ATTR), false, 'nor does a stale mark linger');
});

test('a nonce is not something a message could guess', () => {
  const seen = new Set();
  for (let i = 0; i < 50; i++) seen.add(newNonce());
  assert.equal(seen.size, 50);
  for (const n of seen) assert.ok(/^ns[0-9a-z]{4,}$/.test(n), n);
});

test('a diagram box is used only when it has the renderer’s shape', () => {
  const canvas = el('div', { class: 'canvas' });
  const pre = el('pre', {}, ['graph TD; A-->B']);
  const details = el('details', {}, [pre]);
  const ours = el('div', { class: 'diagram', 'data-diagram': '' }, [canvas, details]);
  const parts = diagramParts(ours);
  assert.equal(parts.canvas, canvas);
  assert.equal(parts.source, 'graph TD; A-->B');

  // a forged box: the attribute, but nothing to mount into
  const forged = el('div', { class: 'diagram', 'data-diagram': '' });
  assert.equal(diagramParts(forged), null, 'no canvas, no mounting — and no throw');
  // a box with a canvas but no source to read
  const headless = el('div', { class: 'diagram', 'data-diagram': '' }, [canvas]);
  assert.equal(diagramParts(headless), null);
  // nothing at all
  assert.equal(diagramParts(null), null);
});
