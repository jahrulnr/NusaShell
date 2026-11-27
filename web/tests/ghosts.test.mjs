// Old empty failed tool rows render as one quiet count, not as cards.
import { installDOM, doc } from './dom.mjs';
installDOM();
import { test } from 'node:test';
import assert from 'node:assert/strict';

const { default: DOMPurify } = await import('/vendor/dompurify/purify.es.mjs');
if (!DOMPurify.addHook) DOMPurify.addHook = () => {};
const { html, render } = await import('/vendor/preact/htm-preact.mjs');
const { Transcript } = await import('../components/transcript.js');

const captured = (id, output = '') => ({ id, name: 'tool', type: 'delegated', status: 'failed', input: '{}', output });

function room(messages) {
  return {
    id: 'c_1', key: 'c_1', messages, live: null, pendingOpen: null, settling: false, failed: null,
    hist: { status: 'ready', more: false, earlierError: null, loadingEarlier: false },
    sel: { workspace: '' }, info: { workspace: '' },
  };
}

function mount(messages) {
  const host = doc.createElement('div');
  const scroller = { current: doc.createElement('div') };
  doc.body.appendChild(host);
  render(html`<${Transcript} room=${room(messages)} scroller=${scroller} />`, host);
  return host;
}

test('two stored placeholder rows are one quiet line, and a real failed call stays a card', () => {
  const host = mount([{
    id: 'a1', role: 'assistant',
    toolCalls: [
      captured('unknown-bare-1', ''),
      captured('agent-9c1d', 'failed'),
      { id: 'call-plain-2', name: 'Run workspace tests', type: 'delegated', status: 'failed', input: '{}', output: '' },
    ],
    parts: [
      { kind: 'tool_call', toolCallId: 'unknown-bare-1' },
      { kind: 'tool_call', toolCallId: 'agent-9c1d' },
      { kind: 'tool_call', toolCallId: 'call-plain-2' },
    ],
  }]);
  try {
    const text = host.textContent;
    assert.equal((text.match(/agent event/g) || []).length, 1);
    assert.equal(host.querySelectorAll('p').length, 1, 'the count is one line');
    assert.equal(host.querySelector('p').textContent, '2 agent events with no recorded detail');
    assert.equal(host.querySelector('p').querySelector('button'), null, 'the line does not open');
    assert.ok(text.includes('Used 1 tool'), 'the real call is still one card, and the placeholders are not');
    assert.ok(text.includes('1 failed'), 'only the real call counts as failed');
  } finally {
    render(null, host);
    host.remove();
  }
});

test('a turn with no ghosts draws no count', () => {
  const host = mount([{
    id: 'a1', role: 'assistant', content: '',
    toolCalls: [{ id: 'real', name: 'exec', input: '{"command":"ls"}', output: 'ok', status: 'completed' }],
    parts: [{ kind: 'tool_call', toolCallId: 'real' }],
  }]);
  try {
    assert.equal(host.textContent.includes('agent event'), false);
    assert.equal(host.querySelector('p'), null);
  } finally {
    render(null, host);
    host.remove();
  }
});
