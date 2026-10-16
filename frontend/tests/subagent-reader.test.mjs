import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const root = new URL('../', import.meta.url);

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLTextAreaElement', 'KeyboardEvent', 'MouseEvent', 'localStorage',
  ];
  const previous = new Map();
  for (const name of names) {
    previous.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
    Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
  }
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

const POPUP_MARKUP = `
  <div class="acp-popup-overlay" id="acp-popup-overlay" hidden>
    <div class="acp-popup" id="acp-popup" role="dialog" aria-modal="true" aria-labelledby="acp-popup-title">
      <div class="acp-popup-head">
        <div>
          <div class="acp-popup-kicker">Peek</div>
          <h2 id="acp-popup-title">Subagent</h2>
        </div>
        <button class="ui-dialog-close" id="acp-popup-close" type="button" aria-label="Close subagent transcript">×</button>
      </div>
      <div class="acp-popup-body" id="acp-popup-body"></div>
    </div>
  </div>`;

function flushMicrotasks(turns = 10) {
  let chain = Promise.resolve();
  for (let index = 0; index < turns; index++) chain = chain.then(() => {});
  return chain;
}

test('delegation card opens the single-run transcript reader for its own run', async () => {
  const dom = new JSDOM(`<body>${POPUP_MARKUP}<div id="host"></div></body>`, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const document = dom.window.document;
  const rpcCalls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    const body = JSON.parse(init?.body || '{}');
    rpcCalls.push({ url, body });
    return {
      ok: true,
      json: async () => ({
        ok: true,
        result: {
          run: {
            id: 'acprun_older42',
            agent_name: 'Devin',
            status: 'completed',
            workspace: '/tmp/older-run',
            prompt: 'scan the repository',
            started_at: '2026-10-01T00:00:00Z',
            updated_at: '2026-10-01T00:01:00Z',
            transcript: [{ kind: 'text', text: 'Historical answer from the old run' }],
          },
        },
      }),
    };
  };
  try {
    const { renderConversation } = await import('../js/views/agent/render.js');
    const fragment = renderConversation([{
      id: 'm1',
      role: 'assistant',
      created_at: '2026-10-01T00:00:00Z',
      content: '',
      tool_calls: [{
        id: 'tc1',
        name: 'subagent',
        status: 'ok',
        args: { agent_id: 'internal', prompt: 'scan the repository' },
        output: JSON.stringify({ runs: [{ id: 'acprun_older42', status: 'completed' }] }),
      }],
    }]);
    document.querySelector('#host').append(fragment);

    const card = document.querySelector('.agent-subagent-card');
    assert.ok(card, 'expected a delegation card in the rendered transcript');
    assert.equal(card.dataset.runId, 'acprun_older42');
    card.focus();
    card.click();

    const overlay = document.getElementById('acp-popup-overlay');
    assert.equal(overlay.hidden, false, 'reader opens instead of crashing on the missing drawer');
    await flushMicrotasks();

    assert.equal(rpcCalls.length, 1, 'historical run must be hydrated once');
    assert.equal(rpcCalls[0].url, '/rpc/acp/runs/get');
    assert.equal(rpcCalls[0].body.payload.id, 'acprun_older42');

    const panel = document.querySelector('#acp-popup-body .acp-run-panel');
    assert.ok(panel, 'reader shows the run panel after hydration');
    assert.equal(panel.dataset.runId, 'acprun_older42');
    assert.equal(document.getElementById('acp-popup-title').textContent, 'Devin');
    assert.match(panel.textContent, /Historical answer from the old run/);

    document.getElementById('acp-popup-close').click();
    assert.equal(overlay.hidden, true, 'close button hides the reader');
    assert.equal(document.activeElement, card, 'closing returns focus to the card that opened the reader');

    // Reopen through the same card: the hydrated run is cached, so no second
    // fetch is issued and the transcript re-appears immediately.
    card.click();
    assert.equal(overlay.hidden, false);
    await flushMicrotasks();
    assert.equal(rpcCalls.length, 1, 'a hydrated run is not fetched again');
    assert.equal(document.querySelector('#acp-popup-body .acp-run-panel')?.dataset.runId, 'acprun_older42');

    document.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    assert.equal(overlay.hidden, true, 'Escape closes the reader');
    assert.equal(document.activeElement, card);

    card.click();
    assert.equal(overlay.hidden, false);
    overlay.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
    assert.equal(overlay.hidden, true, 'backdrop click closes the reader');
    assert.equal(document.activeElement, card);
  } finally {
    globalThis.fetch = originalFetch;
    restore();
  }
});

test('subagent-open selects the card run instead of the newest', async () => {
  const { mountSubagentPanel, selectSubagentRun } = await import(`../page/conversation/subagent-dock.js?test=${Date.now()}`);
  const dom = new JSDOM('<section id="panel-subagents"></section>', { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const panel = dom.window.document.querySelector('#panel-subagents');
  const runs = [
    { id: 'acprun_new', name: 'Newest run', agentName: 'Devin', status: 'complete', startedAt: '2026-10-01T02:00:00Z', objective: 'newest objective' },
    { id: 'acprun_old', name: 'Older run', agentName: 'Devin', status: 'complete', startedAt: '2026-10-01T01:00:00Z', objective: 'older objective' },
  ];

  mountSubagentPanel(panel, runs);
  assert.equal(panel.dataset.activeSubagentId, 'acprun_new', 'default selection stays on the newest run');

  assert.equal(selectSubagentRun(panel, 'acprun_old'), true);
  assert.equal(panel.dataset.activeSubagentId, 'acprun_old', 'the card run becomes the selected run');
  const trigger = panel.querySelector('.subagent-trigger');
  assert.equal(trigger.querySelector('strong').textContent, 'Older run');
  assert.equal(
    panel.querySelector('[role="option"][data-subagent-id="acprun_old"]').getAttribute('aria-selected'),
    'true',
  );
  assert.equal(panel.querySelector('.subagent-objective').textContent, 'older objective');
  restore();
});

test('a run requested before the panel mounts is applied once it hydrates', async () => {
  const { mountSubagentPanel, selectSubagentRun } = await import(`../page/conversation/subagent-dock.js?pending=${Date.now()}`);
  const dom = new JSDOM('<section id="panel-subagents"></section>', { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const panel = dom.window.document.querySelector('#panel-subagents');

  assert.equal(selectSubagentRun(panel, 'acprun_late'), false, 'unmounted panel has nothing to select yet');
  mountSubagentPanel(panel, [
    { id: 'acprun_early', name: 'Early run', status: 'complete', startedAt: '2026-10-01T01:00:00Z', objective: 'early' },
  ]);
  assert.equal(panel.dataset.activeSubagentId, 'acprun_early');

  mountSubagentPanel(panel, [
    { id: 'acprun_late', name: 'Late run', status: 'complete', startedAt: '2026-10-01T02:00:00Z', objective: 'late objective' },
    { id: 'acprun_early', name: 'Early run', status: 'complete', startedAt: '2026-10-01T01:00:00Z', objective: 'early' },
  ]);
  assert.equal(panel.dataset.activeSubagentId, 'acprun_late', 'pending selection applies once the run hydrates');
  restore();
});

test('the dock and the transcript reader share one subagents.js module instance', async () => {
  const dock = await readFile(new URL('page/conversation/subagent-dock.js', root), 'utf8');
  const render = await readFile(new URL('js/views/agent/render.js', root), 'utf8');
  const dockSpecifier = dock.match(/from\s+'([^']*views\/agent\/subagents\.js[^']*)'/)?.[1];
  const renderSpecifier = render.match(/from\s+'([^']*subagents\.js[^']*)'/)?.[1];
  assert.ok(dockSpecifier && renderSpecifier, 'both modules must import subagents.js');
  assert.equal(
    new URL(dockSpecifier, 'http://x/page/conversation/').pathname,
    new URL(renderSpecifier, 'http://x/js/views/agent/').pathname,
  );
  assert.equal(
    new URL(dockSpecifier, 'http://x/page/conversation/').search,
    new URL(renderSpecifier, 'http://x/js/views/agent/').search,
    'differing query strings would load two module instances with separate run state',
  );
});

test('subagents.js does not keep the room-bound drawer', async () => {
  const source = await readFile(new URL('js/views/agent/subagents.js', root), 'utf8');
  assert.doesNotMatch(source, /acp-drawer/, 'drawer element ids are unreachable without #acp-drawer');
  assert.doesNotMatch(source, /\brenderDrawer\b/, 'renderDrawer has no live caller once the drawer is gone');
  assert.doesNotMatch(source, /\bcloseDrawer\b/, 'closeDrawer dereferenced a missing #acp-drawer');
  assert.doesNotMatch(source, /acp-dock/, 'the chip dock has no element in index.html; an early return is not a live surface');
  const css = await readFile(new URL('styles/acp.css', root), 'utf8');
  assert.doesNotMatch(css, /\.acp-dock/, 'chip-dock rules have no markup to attach to');
  assert.match(source, /export function openDrawer/, 'delegation cards still enter through openDrawer');
  assert.match(source, /function openPopup/, 'openDrawer must open the popup reader, not a second surface');
});

test('subagent-open forwards the emitted run id to the dock selection seam', async () => {
  const app = await readFile(new URL('page/conversation/app.js', root), 'utf8');
  const handler = app.match(/addEventListener\('subagent-open',[\s\S]*?\}\);/)?.[0] || '';
  assert.match(handler, /event\.detail\?\.id/, 'the emitted run id must reach the dock');
  assert.match(handler, /selectSubagentRun\(/, 'selection goes through the panel seam');
});
