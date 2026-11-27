// The composer offers a steer only when the live turn declares steerable.
// Deriving that offer from kind === 'send' fails these two renders.
import { installDOM, doc } from './dom.mjs';
installDOM();
import { mockApi, sleep } from './env.mjs';
import { before, test } from 'node:test';
import assert from 'node:assert/strict';

const { html, render } = await import('/vendor/preact/htm-preact.mjs');
const { Composer } = await import('../components/composer.js');
const { room } = await import('../lib/chat.js');

before(() => {
  mockApi({ 'GET /api/commands': { data: [] } });
});

function label(host) {
  return [...host.querySelectorAll('button')].map((b) => b.getAttribute('aria-label'));
}

async function mount(live) {
  const r = room('c_steer_' + (live?.kind || 'idle') + '_' + String(live?.steerable));
  r.id = r.key;
  r.draft = 'hello';
  r.live = live;
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${Composer} room=${r} />`, host);
  await sleep(20);
  return host;
}

test('a running send with no declaration offers the queue, not a steer', async () => {
  const host = await mount({ turnId: 't1', kind: 'send', terminal: false });
  try {
    const labels = label(host);
    assert.ok(labels.includes('Add to queue'), 'the primary action queues');
    assert.equal(labels.includes('Steer now'), false, 'kind send does not offer a steer');
    assert.equal(labels.includes('More send options'), false);
  } finally {
    render(null, host);
    host.remove();
  }
});

test('false and an empty flag offer the queue as well', async () => {
  for (const steerable of [false, '']) {
    const host = await mount({ turnId: 't1', kind: 'send', terminal: false, steerable });
    try {
      const labels = label(host);
      assert.ok(labels.includes('Add to queue'), String(steerable));
      assert.equal(labels.includes('Steer now'), false);
    } finally {
      render(null, host);
      host.remove();
    }
  }
});

test('only steerable true offers Steer now, whatever the kind', async () => {
  const host = await mount({ turnId: 't1', kind: 'edit', terminal: false, steerable: true });
  try {
    const labels = label(host);
    assert.ok(labels.includes('Steer now'), 'the declaration is what offers the steer');
    assert.equal(labels.includes('Add to queue'), false);
  } finally {
    render(null, host);
    host.remove();
  }
});
