// FolderBrowser names its scroll owner, and a new listing starts at the top of
// it. Each mode is tested inside the host it is used in: the panel mode in a
// popover, whose panel must be the only scroll owner; the list mode in a plain
// body, where the list must be.
import { mockApi, sleep } from './env.mjs';
import { installDOM, doc } from './dom.mjs';
installDOM();
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import * as ui from '../components/ui.js';
import { FolderBrowser } from '../components/folders.js';

const MANY = ['sub', ...Array.from({ length: 40 }, (_, i) => `dir-${i}`)];
const listing = (path, names) => ({
  path,
  parent: path.replace(/\/[^/]+$/, '') || '/',
  roots: [],
  truncated: false,
  entries: names.map((name) => ({ name, path: `${path}/${name}`, hidden: false })),
});
function serveFolders() {
  mockApi({
    'GET /api/fs/browse': (call) => {
      const path = new URL(call.url, 'http://localhost').searchParams.get('path') || '/a';
      return [200, listing(path, path === '/a' ? MANY : ['deep'])];
    },
  });
}
const host = () => {
  const h = doc.createElement('div');
  doc.body.appendChild(h);
  return h;
};
// scrollers answers every element under node (node included) that scrolls.
const scrollers = (node) => [node, ...node.querySelectorAll('*')].filter((n) => /(^| )overflow-(y-)?auto( |$)/.test(n.className || ''));
const browser = (scroll) => html`<${FolderBrowser} scroll=${scroll} title="Test" onPick=${() => {}} onCancel=${() => {}} />`;
const inPopover = (scroll) => html`<${ui.Popover} open=${true} placement="top-start" label="Working directory">${browser(scroll)}</${ui.Popover}>`;
const inBody = (scroll) => html`<div class="px-5">${browser(scroll)}</div>`;

test('panel mode: the popover panel is the only scroll owner, and the browser has none of its own', async () => {
  serveFolders();
  const h = host();
  render(inPopover('panel'), h);
  await sleep(20);
  const panel = doc.querySelector('[role="dialog"]');
  assert.ok(panel, 'the panel is open');
  const owners = scrollers(panel);
  assert.equal(owners.length, 1);
  assert.equal(owners[0], panel);
  render(null, h);
});

test('list mode: the list is the only scroll owner, and it is the capped one', async () => {
  serveFolders();
  const h = host();
  render(inBody('list'), h);
  await sleep(20);
  const owners = scrollers(h);
  assert.equal(owners.length, 1);
  assert.match(owners[0].className, /max-h-/, 'the owner is the list under its height cap');
  render(null, h);
});

test('a browser with no scroll mode does not render: the caller names the owner', () => {
  assert.throws(() => render(html`<${FolderBrowser} title="Test" />`, host()), /scroll must be "panel" or "list"/);
});

for (const scroll of ['panel', 'list']) {
  test(`a new listing starts at the top of its scroll owner (${scroll})`, async () => {
    serveFolders();
    const h = host();
    render(scroll === 'panel' ? inPopover(scroll) : inBody(scroll), h);
    await sleep(20);
    const owner = scroll === 'panel' ? doc.querySelector('[role="dialog"]') : scrollers(h)[0];
    owner.scrollTop = 120;
    [...h.querySelectorAll('button')].find((b) => b.textContent.trim() === 'sub').click();
    await sleep(20);
    assert.equal(owner.scrollTop, 0, 'the new folder starts at its top');
    render(null, h);
  });
}
