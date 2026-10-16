import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createAutomationPage } from '../page/automation/app.js';

const VIEW_HTML = `<!doctype html><html><body>
  <div class="automation-mode-switch" role="group">
    <button type="button" data-automation-mode="workflows" aria-pressed="true">Workflows</button>
    <button type="button" data-automation-mode="runs" aria-pressed="false">Runs</button>
  </div>
  <button id="automation-create" type="button"></button>
  <button id="automation-refresh" type="button"></button>
  <span id="automation-list-count"></span>
  <div id="automation-list"></div>
  <div id="automation-inspector-empty"></div>
  <section id="automation-workflow-detail" hidden></section>
  <section id="automation-run-detail" hidden></section>
  <section id="automation-editor" hidden></section>
  <div id="toast-container"></div>
</body></html>`;

const runningRun = {
  id: 'run-1',
  name: 'demo-run',
  status: 'running',
  jobs: [{
    id: 'job-1',
    name: 'job',
    status: 'running',
    steps: [{ id: 'step-1', name: 'agent', status: 'running', output: 'working…' }],
  }],
};

const tick = () => new Promise((resolve) => setTimeout(resolve, 40));

function install(dom) {
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    HTMLElement: globalThis.HTMLElement,
    requestAnimationFrame: globalThis.requestAnimationFrame,
  };
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.requestAnimationFrame = (callback) => { callback(); return 0; };
  return () => {
    globalThis.window = previous.window;
    globalThis.document = previous.document;
    globalThis.HTMLElement = previous.HTMLElement;
    globalThis.requestAnimationFrame = previous.requestAnimationFrame;
    dom.window.close();
  };
}

async function mountView() {
  const dom = new JSDOM(VIEW_HTML, { url: 'http://localhost/' });
  const restore = install(dom);
  const requests = [];
  const rpcClient = async (method, payload) => {
    requests.push({ method, payload });
    if (method === 'automation.list') return { workflows: [] };
    if (method === 'automation.runs.list') return { runs: [runningRun] };
    if (method === 'automation.runs.get') return { run: runningRun };
    if (method === 'automation.schedules') return { schedules: [] };
    if (method === 'automation.events') return { events: [] };
    return {};
  };
  const page = createAutomationPage(dom.window.document, { rpcClient, onEvent: () => () => {} });
  page.init();
  page.activate();
  await tick();
  return { requests, restore, document: dom.window.document };
}

async function openSteerDialog(document) {
  document.querySelector('[data-automation-mode="runs"]').click();
  await tick();
  await tick();
  const steer = [...document.querySelectorAll('#automation-run-detail button')]
    .find((button) => button.textContent.trim() === 'Steer');
  assert.ok(steer, 'Steer button rendered for a run with a running agent step');
  steer.click();
  await tick();
  const overlay = document.querySelector('.ui-dialog-overlay');
  assert.ok(overlay, 'dialog overlay rendered');
  return overlay;
}

test('Steer dialog collects text and sends a string payload', async () => {
  const { requests, restore, document } = await mountView();
  try {
    const overlay = await openSteerDialog(document);
    const textarea = overlay.querySelector('textarea');
    assert.ok(textarea, 'dialog renders a textarea field');
    textarea.value = 'Please hurry up';
    const send = [...overlay.querySelectorAll('.ui-dialog-actions button')]
      .find((button) => button.textContent.trim() === 'Send');
    assert.ok(send, 'Send action rendered');
    send.click();
    await tick();
    const steer = requests.filter((request) => request.method === 'automation.runs.steer');
    assert.equal(steer.length, 1);
    assert.equal(steer[0].payload.id, 'run-1');
    assert.equal(typeof steer[0].payload.text, 'string');
    assert.equal(steer[0].payload.text, 'Please hurry up');
    assert.equal(document.getElementById('automation-list-count').textContent, '1');
  } finally {
    restore();
  }
});

test('Cancelling the Steer dialog sends no steer request', async () => {
  const { requests, restore, document } = await mountView();
  try {
    const overlay = await openSteerDialog(document);
    const textarea = overlay.querySelector('textarea');
    if (textarea) textarea.value = 'never sent';
    const cancel = [...overlay.querySelectorAll('.ui-dialog-actions button')]
      .find((button) => button.textContent.trim() === 'Cancel');
    assert.ok(cancel, 'Cancel action rendered');
    cancel.click();
    await tick();
    assert.equal(requests.filter((request) => request.method === 'automation.runs.steer').length, 0);
  } finally {
    restore();
  }
});
