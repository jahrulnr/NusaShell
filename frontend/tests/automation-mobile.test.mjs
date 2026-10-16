import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';
import { createAutomationPage } from '../page/automation/app.js';

const root = new URL('../', import.meta.url);
const settle = () => new Promise(resolve => setTimeout(resolve, 30));

test('mobile Runs and Events browse independently of detail, retaining the reading position on Back', async () => {
  const dom = new JSDOM(await readFile(new URL('index.html', root), 'utf8'), { url: 'http://localhost/', pretendToBeVisual: true });
  dom.window.innerWidth = 375;
  const previous = new Map();
  for (const key of ['window', 'document', 'HTMLElement', 'requestAnimationFrame']) {
    previous.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, value: dom.window[key] });
  }
  const runs = [{ id: 'run-test', name: 'Daily digest', status: 'completed', jobs: [] }];
  const events = [{ id: 'event-test', type: 'telegram.message', subject: 'Message received' }];
  const rpcClient = async method => ({
    'automation.list': { workflows: [] }, 'automation.runs.list': { runs },
    'automation.events': { events }, 'automation.schedules': { schedules: [] },
    'automation.runs.get': { run: runs[0] },
  })[method] || {};
  const view = dom.window.document.querySelector('#automation-view');
  const page = createAutomationPage(view, { rpcClient, onEvent: () => () => {} });
  try {
    page.init();
    await page.refresh();
    for (const mode of ['runs', 'events']) {
      view.querySelector(`[data-automation-mode="${mode}"]`).click();
      await settle();
      assert.equal(view.dataset.mode, mode);
      assert.equal(view.dataset.mobilePane, 'list', 'automatic selection must not take over the mobile screen');
      const search = view.querySelector('#automation-search');
      search.value = mode === 'runs' ? 'digest' : 'telegram';
      search.dispatchEvent(new dom.window.Event('input'));
      await new Promise(resolve => setTimeout(resolve, 150));
      view.scrollTop = 340;
      view.querySelector('#automation-list .automation-record').click();
      await settle();
      assert.equal(view.dataset.mobilePane, 'detail');
      assert.equal(view.scrollTop, 0);
      await page.refresh();
      assert.equal(view.dataset.mobilePane, 'detail', 'background refresh preserves the chosen pane');
      view.querySelector('#automation-mobile-back').click();
      await settle();
      assert.equal(view.dataset.mobilePane, 'list');
      assert.equal(view.scrollTop, 340);
      assert.equal(search.value, mode === 'runs' ? 'digest' : 'telegram');
      view.querySelector('#automation-list .automation-record').click();
      await settle();
      assert.equal(view.dataset.mobilePane, 'detail', 'same selected record reopens after Back');
      view.querySelector('#automation-mobile-back').click();
      await settle();
    }
    dom.window.innerWidth = 1440;
    view.querySelector('#automation-list .automation-record').click();
    assert.equal(view.dataset.mobilePane, 'list', 'desktop selection does not enter a mobile-only pane');
  } finally {
    page.destroy();
    dom.window.close();
    for (const [key, descriptor] of previous) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globalThis[key];
    }
  }
});
