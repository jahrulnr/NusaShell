import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

// Minimal fixture covering every id the automation view touches.
const VIEW_HTML = `<!doctype html><html><body>
  <div id="automation-tabs" role="tablist">
    <button type="button" data-auto-tab="workflows" role="tab" aria-selected="true">Workflows</button>
    <button type="button" data-auto-tab="runs" role="tab" aria-selected="false">Runs</button>
    <button type="button" data-auto-tab="schedules" role="tab" aria-selected="false">Schedules</button>
    <button type="button" data-auto-tab="events" role="tab" aria-selected="false">Events</button>
  </div>
  <button id="automation-new-btn" type="button"></button>
  <button id="automation-enable-provider-btn" type="button"></button>
  <span id="automation-stat-runnable"></span>
  <span id="automation-stat-blocked"></span>
  <span id="automation-stat-waiting"></span>
  <div id="automation-blocked-banner" hidden><span id="automation-blocked-text"></span></div>
  <div id="automation-workspace"></div>
  <h2 id="automation-list-title"></h2>
  <span id="automation-list-count"></span>
  <div id="automation-list"></div>
  <h2 id="automation-detail-title"></h2>
  <div id="automation-detail-actions"></div>
  <div id="automation-detail"></div>
  <div id="toast-container"></div>
</body></html>`;

// A run with a running agent step (status running + output present) is what
// makes renderRunDetail append the Steer button.
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

const tick = () => new Promise((r) => setTimeout(r, 40));

function rpcResult(result) {
  return { ok: true, json: async () => ({ ok: true, result }) };
}

async function mountView() {
  const dom = new JSDOM(VIEW_HTML, { url: 'http://localhost/' });
  const prevWindow = globalThis.window;
  const prevDocument = globalThis.document;
  const prevHTMLElement = globalThis.HTMLElement;
  const prevRaf = globalThis.requestAnimationFrame;
  const prevFetch = globalThis.fetch;
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.requestAnimationFrame = (cb) => { cb(); return 0; };

  const requests = [];
  globalThis.fetch = async (_url, init) => {
    const body = JSON.parse(init.body);
    requests.push(body);
    if (body.method === 'automation.list') return rpcResult({ workflows: [] });
    if (body.method === 'automation.runs.list') return rpcResult({ runs: [runningRun] });
    if (body.method === 'automation.schedules') return rpcResult({ schedules: [] });
    if (body.method === 'automation.events') return rpcResult({ events: [] });
    return rpcResult({});
  };

  const restore = () => {
    globalThis.fetch = prevFetch;
    globalThis.window = prevWindow;
    globalThis.document = prevDocument;
    globalThis.HTMLElement = prevHTMLElement;
    globalThis.requestAnimationFrame = prevRaf;
  };

  try {
    // Fresh module instance so module-level view state is not shared.
    const mod = await import(`../js/views/automation.js?steer=${Date.now()}-${Math.random()}`);
    await mod.initAutomation();
  } catch (err) {
    restore();
    throw err;
  }
  return { requests, restore };
}

async function openSteerDialog() {
  document.querySelector('[data-auto-tab="runs"]').click();
  await tick();
  document.querySelector('.automation-row').click();
  const steer = [...document.querySelectorAll('#automation-detail-actions button')]
    .find((b) => b.textContent === 'Steer');
  assert.ok(steer, 'Steer button rendered for a run with a running agent step');
  steer.click();
  await tick();
  const overlay = document.querySelector('.ui-dialog-overlay');
  assert.ok(overlay, 'dialog overlay rendered');
  return overlay;
}

test('Steer dialog collects text and sends a string payload', async () => {
  const { requests, restore } = await mountView();
  try {
    const overlay = await openSteerDialog();
    const textarea = overlay.querySelector('textarea');
    assert.ok(textarea, 'dialog renders a textarea field');
    textarea.value = 'Please hurry up';
    const send = [...overlay.querySelectorAll('.ui-dialog-actions button')]
      .find((b) => b.textContent === 'Send');
    assert.ok(send, 'Send action rendered');
    send.click();
    await tick();
    const steer = requests.filter((r) => r.method === 'automation.runs.steer');
    assert.equal(steer.length, 1);
    assert.equal(steer[0].payload.id, 'run-1');
    assert.equal(typeof steer[0].payload.text, 'string');
    assert.equal(steer[0].payload.text, 'Please hurry up');
  } finally {
    restore();
  }
});

test('Cancelling the Steer dialog sends no steer request', async () => {
  const { requests, restore } = await mountView();
  try {
    const overlay = await openSteerDialog();
    const textarea = overlay.querySelector('textarea');
    if (textarea) textarea.value = 'never sent';
    const cancel = [...overlay.querySelectorAll('.ui-dialog-actions button')]
      .find((b) => b.textContent === 'Cancel');
    assert.ok(cancel, 'Cancel action rendered');
    cancel.click();
    await tick();
    assert.equal(requests.filter((r) => r.method === 'automation.runs.steer').length, 0);
  } finally {
    restore();
  }
});
