import assert from 'node:assert/strict';
import test from 'node:test';

import {
  effectiveContextWindow,
  formatContextUsage,
  inspectAttachmentContent,
  toDataURL,
  initialWindowStart,
  previousWindowStart,
  conversationTail,
} from './js/agent-ui.js';

test('context usage uses the effective model window', () => {
  assert.equal(formatContextUsage(1234, 128000), '1k/128k context');
  assert.equal(formatContextUsage(1200000, 1000000), '1.2M/1M context');
});

test('global max input tokens caps an advertised model context window', () => {
  // Model window (catalog) wins over the global cap — cap is only a
  // fallback for models not in the catalog.
  assert.equal(effectiveContextWindow(1_000_000, 200_000), 1_000_000);
  assert.equal(effectiveContextWindow(128_000, 200_000), 128_000);
  assert.equal(effectiveContextWindow(0, 200_000), 200_000);
  assert.equal(effectiveContextWindow(1_000_000, 0), 1_000_000);
});

test('initialWindowStart renders only the last window of a long conversation', () => {
  // A short conversation renders in full (start at 0).
  assert.equal(initialWindowStart(10, 60), 0);
  assert.equal(initialWindowStart(60, 60), 0);
  // A long one starts near the end so only the last `window` messages render.
  assert.equal(initialWindowStart(1000, 60), 940);
  // Defensive against bad inputs.
  assert.equal(initialWindowStart(-5, 60), 0);
  assert.equal(initialWindowStart(100, 0), 99); // window clamps to >= 1
});

test('previousWindowStart reveals one older batch at a time, clamped at 0', () => {
  assert.equal(previousWindowStart(940, 40), 900);
  assert.equal(previousWindowStart(30, 40), 0);
  assert.equal(previousWindowStart(0, 40), 0);
});

function msg(role, id) {
  return { role, id };
}

test('conversationTail keeps the last user bubble and only the last keepRounds of the trailing run', () => {
  const messages = [msg('user', 'u0')];
  for (let i = 0; i < 6; i++) messages.push(msg('assistant', `a${i}`));
  const tail = conversationTail(messages, { prefixWindow: 60, keepRounds: 3 });
  assert.deepEqual(tail.visible.map((m) => m.id), ['u0', 'a3', 'a4', 'a5']);
  assert.equal(tail.prefixStart, 0);
  assert.equal(tail.runStart, 1);
  assert.equal(tail.assistKeepStart, 4);
});

test('conversationTail without a user still shows only the last keepRounds assistants', () => {
  const messages = Array.from({ length: 6 }, (_, i) => msg('assistant', `a${i}`));
  const tail = conversationTail(messages, { keepRounds: 3 });
  assert.deepEqual(tail.visible.map((m) => m.id), ['a3', 'a4', 'a5']);
  assert.equal(tail.runStart, 0);
  assert.equal(tail.assistKeepStart, 3);
});

test('conversationTail does not drop a short trailing run', () => {
  const messages = [msg('user', 'u0'), msg('assistant', 'a0'), msg('assistant', 'a1')];
  const tail = conversationTail(messages, { keepRounds: 3 });
  assert.deepEqual(tail.visible.map((m) => m.id), ['u0', 'a0', 'a1']);
  assert.equal(tail.assistKeepStart, 1);
});

test('conversationTail prefix window leaves older complete turns behind Load older', () => {
  const messages = [];
  for (let i = 0; i < 40; i++) {
    messages.push(msg('user', `u${i}`), msg('assistant', `a${i}`));
  }
  for (let i = 0; i < 10; i++) messages.push(msg('assistant', `tail${i}`));
  const tail = conversationTail(messages, { prefixWindow: 10, keepRounds: 3 });
  assert.equal(tail.visible.at(-1).id, 'tail9');
  assert.equal(tail.visible.filter((m) => m.role === 'assistant' && String(m.id).startsWith('tail')).length, 3);
  assert.ok(tail.visible.some((m) => m.role === 'user'));
  assert.ok(tail.prefixStart > 0);
  assert.ok(tail.assistKeepStart > tail.runStart);
});

test('conversationTail honors a lowered assistKeepStart when Load older reveals more of the run', () => {
  const messages = [msg('user', 'u0'), ...Array.from({ length: 6 }, (_, i) => msg('assistant', `a${i}`))];
  const first = conversationTail(messages, { keepRounds: 3 });
  const expanded = conversationTail(messages, {
    prefixStart: first.prefixStart,
    assistKeepStart: first.assistKeepStart - 3,
  });
  assert.deepEqual(expanded.visible.map((m) => m.id), ['u0', 'a0', 'a1', 'a2', 'a3', 'a4', 'a5']);
  assert.equal(expanded.assistKeepStart, first.runStart);
});

test('attachments are detected by bytes rather than their filename or MIME type', () => {
  const png = Uint8Array.from([137, 80, 78, 71, 13, 10, 26, 10]);
  assert.deepEqual(inspectAttachmentContent(png), { type: 'image', mediaType: 'image/png' });
  assert.equal(toDataURL(png, 'image/png'), 'data:image/png;base64,iVBORw0KGgo=');
  assert.deepEqual(
    inspectAttachmentContent(new TextEncoder().encode('hello from a text attachment')),
    { type: 'text', mediaType: 'text/plain', content: 'hello from a text attachment' },
  );
  assert.equal(inspectAttachmentContent(Uint8Array.from([0, 159, 255, 1])), null);
});
