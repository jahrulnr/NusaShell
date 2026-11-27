// The command receipt says what the command is doing, not the line that
// started it: the busy label already names the action, and the fallback
// "Running /name" carries the name for anything else. A failed command keeps
// its echo — the refusal reason refers to that line and Retry resends it.
import { installDOM, doc } from './dom.mjs';
installDOM();
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { CommandReceipt, CommandFailure } from '../components/composer.js';

// The shape runCommand writes (chat.js): the line as typed, its parsed name,
// and the clock the receipt counts from.
const running = (over = {}) => ({ name: 'compact', text: '/compact', args: '', phase: 'running', error: '', retry: true, startedAt: Date.now(), ...over });

test('a running receipt names the action without echoing the command line', () => {
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    render(html`<${CommandReceipt} command=${running()} />`, host);
    assert.equal(host.textContent.includes('/compact'), false, 'the receipt must not echo the command line');
    assert.equal(host.querySelector('.font-mono'), null, 'no mono echo span in a running receipt');
    assert.ok(host.textContent.includes('Compacting'), 'the busy label still names the action');
  } finally {
    render(null, host);
    host.remove();
  }
});

test('a failed receipt keeps the echo: the refusal refers to that line and Retry resends it', () => {
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    const command = { ...running(), phase: 'failed', error: 'The thread is busy.' };
    render(html`<${CommandFailure} room=${{ command }} />`, host);
    const echo = host.querySelector('.font-mono');
    assert.ok(echo, 'the failed receipt still shows the command line');
    assert.equal(echo.textContent, '/compact', 'the echo is the line as typed');
    assert.ok(host.textContent.includes('The thread is busy.'), 'the refusal reason is shown');
  } finally {
    render(null, host);
    host.remove();
  }
});

// The receipt is the one surface a native command's status lives on. The
// header used to carry a spinner chip with the same label; it added no action
// and cost the title its width, so while a command runs the header must not
// render the busy label at all.
// chat.js pulls in the markdown sanitizer, whose vendor bundle degrades in
// this shim document. The header never renders markdown, so a no-op hook is
// enough to load the chain — sanitization itself is not exercised here.
const { default: DOMPurify } = await import('/vendor/dompurify/purify.es.mjs');
if (!DOMPurify.addHook) DOMPurify.addHook = () => {};
const { Header } = await import('../components/chat.js');

function headerRoom(items) {
  return {
    id: 'c_hdr', key: 'c_hdr', info: { title: 'Thread', workspace: '' }, command: null,
    steers: [], queue: [], files: [], quotes: [], refs: [], draft: '',
    catalog: { status: 'ready', items },
  };
}

test('a running command does not put its busy label in the header', () => {
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  try {
    const room = headerRoom([]);
    room.command = running();
    render(html`<${Header} room=${room} browser=${{ open: false }} pane=${''} setPane=${() => {}} />`, host);
    assert.equal(host.textContent.includes('Compacting'), false, 'the header must not carry the command busy label');
  } finally {
    render(null, host);
    host.remove();
  }
});

test('Compact history is the native row, and an agent-reported compact does not get that button', async () => {
  globalThis.innerHeight = 800;
  globalThis.addEventListener = globalThis.addEventListener || (() => {});
  globalThis.removeEventListener = globalThis.removeEventListener || (() => {});
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const openMenu = async () => {
    const button = [...host.querySelectorAll('button')].find((b) => b.getAttribute('aria-label') === 'Thread actions');
    assert.ok(button, 'the thread menu is not there');
    if (!host.textContent.includes('Rename')) button.click();
    await new Promise((resume) => setTimeout(resume, 0));
  };
  try {
    render(html`<${Header} room=${headerRoom([{ name: 'compact', source: 'native', enabled: true }])} browser=${{ open: false }} pane=${''} setPane=${() => {}} />`, host);
    await openMenu();
    assert.ok(host.textContent.includes('Compact history'), 'a harness compact stays in the thread menu');
    render(html`<${Header} room=${headerRoom([{ name: 'compact', source: 'agent', enabled: true }, { name: 'clear', source: 'native', enabled: true }])} browser=${{ open: false }} pane=${''} setPane=${() => {}} />`, host);
    await openMenu();
    assert.ok(host.textContent.includes('Rename'), 'the menu is open');
    assert.equal(host.textContent.includes('Compact history'), false, 'an agent compact is not the native action');
  } finally {
    render(null, host);
    host.remove();
  }
});
