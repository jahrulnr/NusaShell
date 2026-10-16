// Historical list/stream freshness (stale acp.runs.list responses, gap frames,
// terminal revival) lived on the conversation EventSource inside this module.
// That stream now belongs to page/conversation/app.js and is covered by
// tests/subagent-stream.test.mjs. What remains here is the popup reader's
// on-demand acp.runs.get: a successful load paints the run, a failure shows
// the retry copy once, and a cached run is not fetched again.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { openDrawer } from '../js/views/agent/subagents.js';

const POPUP_HTML = `<!doctype html><html><body>
  <section id="panel-subagents"></section>
  <div class="acp-popup-overlay" id="acp-popup-overlay" hidden>
    <div class="acp-popup" id="acp-popup" role="dialog" aria-modal="true" aria-labelledby="acp-popup-title">
      <h2 id="acp-popup-title">Subagent</h2>
      <button id="acp-popup-close" type="button">×</button>
      <div class="acp-popup-body" id="acp-popup-body"></div>
    </div>
  </div>
</body></html>`;

const RETRY_TEXT = 'Unable to load this transcript. Close and try again.';

function install(dom) {
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    HTMLElement: globalThis.HTMLElement,
    fetch: globalThis.fetch,
  };
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  return () => {
    globalThis.window = previous.window;
    globalThis.document = previous.document;
    globalThis.HTMLElement = previous.HTMLElement;
    globalThis.fetch = previous.fetch;
    dom.window.close();
  };
}

function rpcResult(result) {
  return { ok: true, json: async () => ({ ok: true, result }) };
}

test('openDrawer loads a historical run through acp.runs.get and paints its status', async () => {
  const dom = new JSDOM(POPUP_HTML, { url: 'http://localhost/' });
  const restore = install(dom);
  const calls = [];
  globalThis.fetch = async (_url, init) => {
    const body = JSON.parse(init.body);
    calls.push(body);
    return rpcResult({
      run: {
        id: 'acprun_loaded',
        agent_name: 'Devin',
        status: 'completed',
        prompt: 'live-prompt',
        updated_at: new Date().toISOString(),
        transcript: [{ kind: 'text', text: 'finished' }],
      },
    });
  };
  try {
    openDrawer('acprun_loaded');
    assert.equal(document.getElementById('acp-popup-overlay').hidden, false);
    assert.match(document.getElementById('acp-popup-body').textContent, /Loading transcript/);
    await new Promise((resolve) => setTimeout(resolve, 20));
    const panel = document.querySelector('#acp-popup-body .acp-run-panel[data-run-id="acprun_loaded"]');
    assert.ok(panel, 'transcript panel is mounted from the get');
    assert.equal(panel.querySelector('.acp-status-pill').textContent, 'done');
    assert.match(panel.textContent, /live-prompt/);
    assert.match(panel.textContent, /finished/);
    assert.equal(calls.filter((call) => call.method === 'acp.runs.get').length, 1);

    openDrawer('acprun_loaded');
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(calls.filter((call) => call.method === 'acp.runs.get').length, 1, 'a cached run is not fetched again');
    document.getElementById('acp-popup-close').click();
    assert.equal(document.getElementById('acp-popup-overlay').hidden, true);
  } finally {
    restore();
  }
});

test('a genuine get failure shows the retry text and is not retried automatically', async () => {
  const dom = new JSDOM(POPUP_HTML, { url: 'http://localhost/' });
  const restore = install(dom);
  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: false, status: 500, json: async () => ({ ok: false, error: { message: 'run not found' } }) };
  };
  try {
    openDrawer('acprun_missing');
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.match(document.getElementById('acp-popup-body').textContent, new RegExp(RETRY_TEXT.replace(/[.]/g, '\\.')));
    assert.equal(calls, 1);
    await new Promise((resolve) => setTimeout(resolve, 30));
    assert.equal(calls, 1, 'no unbounded retry');
  } finally {
    restore();
  }
});
