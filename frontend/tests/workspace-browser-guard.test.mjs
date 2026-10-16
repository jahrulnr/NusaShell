import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const root = new URL('../', import.meta.url);

async function mount(rpc) {
  const { initWorkspaceBrowser } = await import(`../page/conversation/workspace-browser.js?guard=${Date.now()}-${Math.random()}`);
  const html = await readFile(new URL('index.html', root), 'utf8');
  const dom = new JSDOM(html, { url: 'http://localhost/' });
  const form = dom.window.document.querySelector('#message-form');
  let applied = null;
  const controller = initWorkspaceBrowser(form, {
    rpc,
    onApply: (path) => { applied = path; },
  });
  return { dom, form, controller, applied: () => applied };
}

test('workspace browser keeps a listing error inline and leaves the folder unselected', async () => {
  const { dom, form, applied } = await mount(async () => { throw new Error('disk offline'); });
  try {
    form.querySelector('#workspace-browser-trigger').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const empty = form.querySelector('#workspace-empty');
    assert.equal(form.querySelector('#workspace-browser-view').hidden, false);
    assert.equal(empty.hidden, false);
    assert.equal(empty.textContent, 'disk offline');
    assert.equal(form.querySelector('#workspace-use-folder').disabled, true);
    assert.equal(applied(), null);
  } finally {
    dom.window.close();
  }
});

test('Escape closes the workspace browser without applying a folder', async () => {
  const { dom, form, applied } = await mount(async () => ({
    path: '/home/dev',
    parent: '/home',
    entries: [{ name: 'workspace', path: '/home/dev/workspace' }],
  }));
  try {
    form.querySelector('#workspace-browser-trigger').click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(form.querySelector('#workspace-browser-view').hidden, false);
    form.querySelector('#workspace-browser-view').dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    assert.equal(form.querySelector('#workspace-browser-view').hidden, true);
    assert.equal(form.querySelector('#model-config-view').hidden, false);
    assert.equal(applied(), null);
    assert.equal(form.querySelector('#workspace-browser-trigger strong').textContent, 'Home');
  } finally {
    dom.window.close();
  }
});


test('Home fallback stays distinct from an actual folder named workspace', async () => {
  const { dom, form, controller } = await mount(async () => ({}));
  try {
    assert.equal(form.querySelector('#workspace-browser-trigger strong').textContent, 'Home');
    controller.setPath('/home/dev/workspace');
    assert.equal(form.querySelector('#workspace-browser-trigger strong').textContent, 'workspace');
    controller.setPath('');
    assert.equal(form.querySelector('#workspace-browser-trigger strong').textContent, 'Home');
  } finally { dom.window.close(); }
});
