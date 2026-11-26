// A minimal DOM for component tests: just enough for preact to render,
// diff, and dispatch events, plus a MutationObserver and the focus/activeElement
// pair — because the popup-class invariant tests assert node identity, and
// only a real render can prove it. Not a browser: no layout, no CSS, no
// selector engine beyond the compound-and-descendant subset the tests use.
class ClassList {
  constructor(el) { this.el = el; }
  get _set() { return new Set((this.el.className || '').split(/\s+/).filter(Boolean)); }
  contains(c) { return this._set.has(c); }
}
let observerQueue = [];
function flushObservers() {
  for (const o of observerQueue) {
    if (!o._records.length) continue;
    const batch = o._records; o._records = [];
    o._cb(batch);
  }
}
export class MutationObserver {
  constructor(cb) { this._cb = cb; this._records = []; }
  observe(target, opts) { this._target = target; this._opts = opts || {}; if (!observerQueue.includes(this)) observerQueue.push(this); }
  disconnect() { observerQueue = observerQueue.filter((o) => o !== this); }
}
const matchesObserved = (obs, node) => {
  let n = node;
  while (n) { if (n === obs._target) return true; n = n.parentNode; }
  return false;
};
const notifyChildren = (node, added, removed) => {
  for (const o of observerQueue) {
    if (!o._opts.childList || !matchesObserved(o, node)) continue;
    if (!o._opts.subtree && node !== o._target) continue;
    o._records.push({ type: 'childList', target: node, addedNodes: added, removedNodes: removed });
    queueMicrotask(flushObservers);
  }
};
class Node {
  constructor() { this.parentNode = null; this.childNodes = []; this._evreg = {}; }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.childNodes.indexOf(this);
    return this.parentNode.childNodes[i + 1] || null;
  }
  get previousSibling() {
    if (!this.parentNode) return null;
    const i = this.parentNode.childNodes.indexOf(this);
    return this.parentNode.childNodes[i - 1] || null;
  }
  get ownerDocument() { return doc; }
  get parentElement() { return this.parentNode?.nodeType === 1 ? this.parentNode : null; }
  appendChild(n) { return this.insertBefore(n, null); }
  insertBefore(n, ref) {
    if (n.parentNode) n.parentNode.removeChild(n);
    const i = ref == null ? -1 : this.childNodes.indexOf(ref);
    if (i < 0) this.childNodes.push(n); else this.childNodes.splice(i, 0, n);
    n.parentNode = this;
    notifyChildren(this, [n], []);
    return n;
  }
  removeChild(n) {
    const i = this.childNodes.indexOf(n);
    if (i >= 0) this.childNodes.splice(i, 1);
    n.parentNode = null;
    notifyChildren(this, [], [n]);
    return n;
  }
  remove() { this.parentNode?.removeChild(this); }
  contains(n) { while (n) { if (n === this) return true; n = n.parentNode; } return false; }
  addEventListener(type, fn) { (this._evreg[type.toLowerCase()] ||= new Set()).add(fn); }
  removeEventListener(type, fn) { this._evreg[type]?.delete(fn); }
  dispatchEvent(e) {
    e.target ||= this;
    let node = this;
    while (node) {
      e.currentTarget = node;
      for (const fn of [...(node._evreg?.[e.type.toLowerCase()] || [])]) fn.call(node, e);
      if (e._stopped) break;
      node = node.parentNode || (node === doc.documentElement ? doc : null);
    }
    return !e.defaultPrevented;
  }
  get textContent() {
    if (this.nodeType === 3) return this.data;
    return this.childNodes.map((c) => c.textContent).join('');
  }
  set textContent(v) {
    if (this.nodeType === 3) { this.data = String(v); this.nodeValue = this.data; return; }
    while (this.childNodes.length) this.removeChild(this.childNodes[0]);
    if (v) this.appendChild(doc.createTextNode(String(v)));
  }
}
export class TextNode extends Node {
  constructor(data) { super(); this.nodeType = 3; this.data = data; this.nodeValue = data; this.splitText = null; }
  set data(v) { this._data = String(v); this.nodeValue = this._data; }
  get data() { return this._data; }
}
export class Element extends Node {
  constructor(tag, ns) {
    super();
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.localName = tag.toLowerCase();
    this.namespaceURI = ns || 'http://www.w3.org/1999/xhtml';
    this.attributes = {};
    this.className = '';
    this.style = {};
    this.scrollTop = 0;
    this.classList = new ClassList(this); this.onclick = null; this.oninput = null; this.onchange = null; this.onkeydown = null; this.onscroll = null;
  }
  setAttribute(name, v) {
    this.attributes[name] = String(v);
    if (name === 'class') this.className = String(v);
    for (const o of observerQueue) if (o._opts.attributes && matchesObserved(o, this)) { o._records.push({ type: 'attributes', target: this, attributeName: name, addedNodes: [], removedNodes: [] }); queueMicrotask(flushObservers); }
  }
  getAttribute(name) { return name in this.attributes ? this.attributes[name] : null; }
  removeAttribute(name) { delete this.attributes[name]; }
  hasAttribute(name) { return name in this.attributes; }
  focus() { doc.activeElement = this; }
  blur() { if (doc.activeElement === this) doc.activeElement = doc.body; }
  click() { this.dispatchEvent(new Ev('click', { bubbles: true })); }
  getBoundingClientRect() { return { top: 0, bottom: 0, left: 0, right: 0, width: 0, height: 0 }; }
  matches(sel) { return matchChain(this, sel.trim().split(/\s+/).map(parseSimple)); }
  closest(sel) { let n = this; while (n && n.nodeType === 1) { if (n.matches(sel)) return n; n = n.parentNode; } return null; }
  get offsetHeight() { return 0; }
  get offsetWidth() { return 0; }
  _walk(out) { out.push(this); for (const c of this.childNodes) if (c._walk) c._walk(out); return out; }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  querySelectorAll(sel) {
    const parts = sel.trim().split(/\s+/).map(parseSimple);
    const all = [];
    for (const el of this._walk([])) { if (el === this) continue; if (matchChain(el, parts)) all.push(el); }
    return all;
  }
}
function parseSimple(s) {
  const m = { tag: null, classes: [], attrs: [] };
  const re = /(^[a-zA-Z][\w-]*)|(\.[\w-]+)|(\[[\w-]+(?:="[^"]*")?\])/g;
  let x;
  while ((x = re.exec(s))) {
    if (x[1]) m.tag = x[1].toLowerCase();
    else if (x[2]) m.classes.push(x[2].slice(1));
    else if (x[3]) { const inner = x[3].slice(1, -1); const [name, val] = inner.split('='); m.attrs.push([name, val ? val.replace(/"/g, '') : null]); }
  }
  return m;
}
function matchSimple(el, m) {
  if (!el.tagName) return false;
  if (m.tag && el.localName !== m.tag) return false;
  for (const c of m.classes) if (!el.classList.contains(c)) return false;
  for (const [name, val] of m.attrs) {
    const v = el.getAttribute(name);
    if (val == null ? v == null : v !== val) return false;
  }
  return true;
}
function matchChain(el, parts) {
  if (!matchSimple(el, parts[parts.length - 1])) return false;
  let anc = el.parentNode;
  for (let i = parts.length - 2; i >= 0; i--) {
    while (anc && !matchSimple(anc, parts[i])) anc = anc.parentNode;
    if (!anc) return false;
    anc = anc.parentNode;
  }
  return true;
}
export class Ev {
  constructor(type, opts = {}) { this.type = type; this._stopped = false; this.defaultPrevented = false; this.bubbles = !!opts.bubbles; }
  stopPropagation() { this._stopped = true; }
  preventDefault() { this.defaultPrevented = true; }
  stopImmediatePropagation() { this._stopped = true; }
}
class Doc extends Node {
  constructor() {
    super();
    this.nodeType = 9;
    this.documentElement = new Element('html');
    this.body = new Element('body');
    this.documentElement.appendChild(this.body);
    this.documentElement.parentNode = this;
    this.activeElement = this.body;
  }
  createElement(tag) { return new Element(tag); }
  createElementNS(ns, tag) { return new Element(tag, ns); }
  createTextNode(data) { return new TextNode(data); }
  querySelector(sel) { return this.documentElement.querySelector(sel); }
  querySelectorAll(sel) { return this.documentElement.querySelectorAll(sel); }
}
export const doc = new Doc();
// installDOM puts the document on globalThis for a component test and
// returns a restore function. Import order matters: modules that read
// document at evaluation time must load after install.
export function installDOM() {
  const prev = { document: globalThis.document, MutationObserver: globalThis.MutationObserver, matchMedia: globalThis.matchMedia };
  globalThis.document = doc;
  globalThis.MutationObserver = MutationObserver;
  globalThis.matchMedia = globalThis.matchMedia || (() => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  globalThis.requestAnimationFrame = globalThis.requestAnimationFrame || ((fn) => setTimeout(() => fn(Date.now()), 0));
  globalThis.cancelAnimationFrame = globalThis.cancelAnimationFrame || ((id) => clearTimeout(id));
  return () => {
    if (prev.document === undefined) delete globalThis.document; else globalThis.document = prev.document;
    if (prev.MutationObserver === undefined) delete globalThis.MutationObserver; else globalThis.MutationObserver = prev.MutationObserver;
  };
}
