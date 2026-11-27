// The popover's placement and cap, and the open flag an anchor owns. The
// placement reads the window through the viewport helper, so it runs in the
// layout a desktop shows. The geometry is a stub: this checks the logic of the
// measure, not a layout the browser drew.
import { sleep } from './env.mjs';
import { installDOM, doc, Element } from './dom.mjs';
installDOM();
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import * as ui from '../components/ui.js';
import { inLayout } from './viewport.mjs';

const Host = ({ open, layer = 'flow' }) => html`<div><${ui.Popover} open=${open} layer=${layer} placement="top-end" label="Probe"><p>content</p></${ui.Popover}></div>`;

const dialogSize = (height) => {
  const realH = Object.getOwnPropertyDescriptor(Element.prototype, 'offsetHeight');
  const realW = Object.getOwnPropertyDescriptor(Element.prototype, 'offsetWidth');
  Object.defineProperty(Element.prototype, 'offsetHeight', { configurable: true, get() { return this.getAttribute('role') === 'dialog' ? height.px : 0; } });
  Object.defineProperty(Element.prototype, 'offsetWidth', { configurable: true, get() { return this.getAttribute('role') === 'dialog' ? 200 : 0; } });
  return () => {
    Object.defineProperty(Element.prototype, 'offsetHeight', realH);
    Object.defineProperty(Element.prototype, 'offsetWidth', realW);
  };
};

test('a top-layer panel pins its edge to the anchor and flips when the content outgrows the side', () => inLayout('desktop', async () => {
  const height = { px: 100 };
  const restore = dialogSize(height);
  const prevHtml = globalThis.HTMLElement;
  const prevWidth = globalThis.innerWidth;
  const prevMatch = globalThis.matchMedia;
  globalThis.HTMLElement = Element;
  globalThis.innerWidth = 1280;
  // The desktop helper answers the sheet query only. This case is the wide window.
  globalThis.matchMedia = (query) => ({
    matches: query.includes('min-width'),
    addEventListener() {},
    removeEventListener() {},
  });
  Object.defineProperty(Element.prototype, 'popover', {
    configurable: true,
    get() { return this.getAttribute('popover'); },
    set(v) { if (v == null || v === false) this.removeAttribute('popover'); else this.setAttribute('popover', v === true ? 'auto' : String(v)); },
  });
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    render(html`<${Host} open=${false} layer="top" />`, host);
    // The anchor spans 200–230px of an 800px window: 184px room above, 554px below.
    host.firstChild.getBoundingClientRect = () => ({ top: 200, bottom: 230, left: 0, right: 100, width: 100, height: 30 });
    render(html`<${Host} open=${true} layer="top" />`, host);
    await sleep(5);
    const panel = doc.querySelector('[role="dialog"]');
    assert.equal(panel.getAttribute('popover'), 'auto');
    assert.equal(panel.className.includes('sm:bottom-full'), false);
    assert.equal(panel.className.includes('max-h-[75dvh]'), false);
    // bottom: innerHeight - anchor.top + 8, so the panel's bottom edge is 8px above the anchor.
    const edge = innerHeight - parseFloat(panel.style.bottom);
    assert.ok(edge <= 200, 'the panel bottom sits at or above the anchor top');
    assert.equal(panel.style.maxHeight, '184px');
    assert.equal(panel.style.left, '8px', 'a panel wider than the anchor stays inside the window');
    height.px = 300;
    render(html`<${Host} open=${true} layer="top" />`, host);
    await sleep(5);
    assert.equal(panel.className.includes('sm:top-full'), false);
    assert.ok(parseFloat(panel.style.top) >= 230, 'flipped panel starts at or below the anchor bottom');
    assert.equal(panel.style.maxHeight, '554px');
  } finally {
    restore();
    delete Element.prototype.popover;
    delete Element.prototype.showPopover;
    globalThis.HTMLElement = prevHtml;
    globalThis.innerWidth = prevWidth;
    globalThis.matchMedia = prevMatch;
    render(null, host);
  }
}));

test('without the Popover API a top layer request stays in the flow', () => inLayout('desktop', async () => {
  const height = { px: 100 };
  const restore = dialogSize(height);
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    render(html`<${Host} open=${false} layer="top" />`, host);
    host.firstChild.getBoundingClientRect = () => ({ top: 200, bottom: 230, left: 0, right: 100, width: 100, height: 30 });
    render(html`<${Host} open=${true} layer="top" />`, host);
    await sleep(5);
    const panel = doc.querySelector('[role="dialog"]');
    assert.equal(panel.getAttribute('popover'), null);
    assert.ok(panel.className.includes('sm:absolute'), 'the flow panel is positioned by the wrapper');
    assert.ok(panel.className.includes('sm:bottom-full'), 'short content opens above, where it fits');
    assert.equal(panel.style.maxHeight, '184px');
    height.px = 300;
    render(html`<${Host} open=${true} layer="top" />`, host);
    await sleep(5);
    assert.ok(panel.className.includes('sm:top-full'), 'grown content flips below, where there is room');
    assert.equal(panel.style.maxHeight, '554px');
  } finally {
    restore();
    render(null, host);
  }
}));

// Probe is an anchor that owns a popover's flag: the button opens it, the
// row reports the flag the hook hands back.
function Probe({ show }) {
  const [open, setOpen] = ui.usePopoverOpen(show);
  return html`<button onClick=${() => setOpen(true)}>open</button><i data-open=${String(open)}></i>`;
}

test('an open flag belongs to its anchor: hidden, it reads closed at once; shown again, it stays closed', async () => {
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const flag = () => host.querySelector('i').getAttribute('data-open');
  render(html`<${Probe} show=${true} />`, host);
  host.querySelector('button').click();
  await sleep(5);
  assert.equal(flag(), 'true');
  render(html`<${Probe} show=${false} />`, host);
  assert.equal(flag(), 'false', 'hidden, it reads closed in the same render, before any effect runs');
  await sleep(5);
  render(html`<${Probe} show=${true} />`, host);
  assert.equal(flag(), 'false', 'shown again, it does not reopen by itself');
  render(null, host);
});
