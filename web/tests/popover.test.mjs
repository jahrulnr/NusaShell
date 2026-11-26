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

const Host = ({ open }) => html`<div><${ui.Popover} open=${open} placement="top-start" label="Probe"><p>content</p></${ui.Popover}></div>`;

test('the panel follows its content: grown content flips to the side with room, and takes that side\'s cap', () => inLayout('desktop', async () => {
  const height = { px: 100 };
  const real = Object.getOwnPropertyDescriptor(Element.prototype, 'offsetHeight');
  Object.defineProperty(Element.prototype, 'offsetHeight', { configurable: true, get() { return this.getAttribute('role') === 'dialog' ? height.px : 0; } });
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    render(html`<${Host} open=${false} />`, host);
    // The anchor spans 200–230px of an 800px window: 184px room above, 554px below.
    host.firstChild.getBoundingClientRect = () => ({ top: 200, bottom: 230, left: 0, right: 100, width: 100, height: 30 });
    render(html`<${Host} open=${true} />`, host);
    await sleep(5);
    const panel = doc.querySelector('[role="dialog"]');
    assert.ok(panel.className.includes('sm:bottom-full'), 'short content opens above, where it fits');
    assert.equal(panel.style.maxHeight, '184px');
    height.px = 300;
    render(html`<${Host} open=${true} />`, host);
    await sleep(5);
    assert.ok(panel.className.includes('sm:top-full'), 'grown content flips below, where there is room');
    assert.equal(panel.style.maxHeight, '554px');
  } finally {
    Object.defineProperty(Element.prototype, 'offsetHeight', real);
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
