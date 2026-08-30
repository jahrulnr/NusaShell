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
  isThreadAtBottom,
  syncThreadPin,
  updateScrollPin,
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

test('terminal lifecycle can sync the pin from actual thread geometry', () => {
  const thread = { scrollHeight: 1000, scrollTop: 300, clientHeight: 500 };
  const state = { pinned: true };
  assert.equal(isThreadAtBottom(thread), false);
  assert.equal(syncThreadPin(state, thread), false);
  assert.equal(state.pinned, false);

  thread.scrollTop = 476;
  assert.equal(syncThreadPin(state, thread), true);
  assert.equal(state.pinned, true);
});

test('updateScrollPin keeps the pin while content grows ahead of the follow scroll', () => {
  // Tool spam: content grows faster than the follow-scroll, so the bottom
  // distance is large even though the user never scrolled up. The pin must
  // survive (the observer keeps the marker visible → follow continues).
  const thread = { scrollHeight: 2000, scrollTop: 900, clientHeight: 500 };
  const state = { pinned: true };
  assert.equal(updateScrollPin(state, thread), true, 'growth alone never unpins');

  // A follow-scroll that lands short of the bottom (layout still settling)
  // is also not a user scroll-up.
  thread.scrollTop = 900;
  thread.scrollHeight = 1400;
  assert.equal(updateScrollPin(state, thread), true, 'downward/level movement keeps the pin');
});

test('updateScrollPin releases on a real upward scroll and re-pins at the bottom', () => {
  const thread = { scrollHeight: 2000, scrollTop: 900, clientHeight: 500 };
  const state = { pinned: true };
  assert.equal(updateScrollPin(state, thread), true);

  // User scrolls up 100px — well beyond the follow-scroll slack.
  thread.scrollTop = 800;
  assert.equal(updateScrollPin(state, thread), false);
  // Content keeps growing while the user reads; stays released.
  thread.scrollHeight = 2400;
  assert.equal(updateScrollPin(state, thread), false);

  // User returns to the bottom → re-pin.
  thread.scrollTop = 1900;
  assert.equal(updateScrollPin(state, thread), true);
});

test('updateScrollPin ignores geometry from a different thread element', () => {
  const threadA = { scrollHeight: 1000, scrollTop: 900, clientHeight: 500 };
  const threadB = { scrollHeight: 1000, scrollTop: 100, clientHeight: 500 };
  const state = { pinned: true };
  updateScrollPin(state, threadA);
  // Switching rooms hands us a fresh element with scrollTop 0 — that must
  // not read as "user scrolled to the top".
  assert.equal(updateScrollPin(state, threadB), true);
  assert.equal(state.pinned, true);
});

function msg(role, id) {
  return { role, id };
}

test('conversationTail keeps the last user bubble and the complete trailing run', () => {
  const messages = [msg('user', 'u0')];
  for (let i = 0; i < 6; i++) messages.push(msg('assistant', `a${i}`));
  const tail = conversationTail(messages, { prefixWindow: 60, keepRounds: 3 });
  assert.deepEqual(tail.visible.map((m) => m.id), ['u0', 'a0', 'a1', 'a2', 'a3', 'a4', 'a5']);
  assert.equal(tail.prefixStart, 0);
  assert.equal(tail.runStart, 1);
  assert.equal(tail.assistKeepStart, 1);
});

test('conversationTail without a user still shows the complete assistant run', () => {
  const messages = Array.from({ length: 6 }, (_, i) => msg('assistant', `a${i}`));
  const tail = conversationTail(messages, { keepRounds: 3 });
  assert.deepEqual(tail.visible.map((m) => m.id), ['a0', 'a1', 'a2', 'a3', 'a4', 'a5']);
  assert.equal(tail.runStart, 0);
  assert.equal(tail.assistKeepStart, 0);
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
  assert.equal(tail.visible.filter((m) => m.role === 'assistant' && String(m.id).startsWith('tail')).length, 10);
  assert.ok(tail.visible.some((m) => m.role === 'user'));
  assert.ok(tail.prefixStart > 0);
  assert.equal(tail.assistKeepStart, tail.runStart);
});

test('conversationTail keeps the immediate user bubble when the prefix budget is exhausted', () => {
  const messages = [];
  for (let i = 0; i < 40; i++) {
    messages.push(msg('user', `old-u${i}`), msg('assistant', `old-a${i}`));
  }
  messages.push(msg('user', 'current-user'));
  for (let i = 0; i < 6; i++) messages.push(msg('assistant', `current-a${i}`));

  const tail = conversationTail(messages, { prefixWindow: 3, keepRounds: 3 });

  assert.deepEqual(tail.visible.map((m) => m.id), ['current-user', 'current-a0', 'current-a1', 'current-a2', 'current-a3', 'current-a4', 'current-a5']);
  assert.equal(tail.prefixStart, tail.runStart - 1);
});

test('conversationTail keeps a compaction marker immediately before the live assistant run', () => {
  const messages = [];
  for (let i = 0; i < 40; i++) {
    messages.push(msg('user', `old-u${i}`), msg('assistant', `old-a${i}`));
  }
  messages.push(msg('user', '[COMPACTION CHECKPOINT] preserve this marker'));
  for (let i = 0; i < 4; i++) messages.push(msg('assistant', `post-compact-a${i}`));

  const tail = conversationTail(messages, { prefixWindow: 2, keepRounds: 3 });

  assert.deepEqual(tail.visible.map((m) => m.id), [
    '[COMPACTION CHECKPOINT] preserve this marker',
    'post-compact-a0',
    'post-compact-a1',
    'post-compact-a2',
    'post-compact-a3',
  ]);
});

test('conversationTail treats the auto-continue announcement like any assistant round and keeps the last user bubble', () => {
  const messages = [
    msg('user', 'original-user'),
    msg('assistant', 'first-assistant'),
    {
      role: 'assistant',
      id: 'auto-continue-announcement',
      tool_calls: [{ id: 'announce-x', name: 'announcement', args: { type: 'auto_continue' }, status: 'ok', output: 'notice' }],
    },
    msg('assistant', 'continued-a0'),
    msg('assistant', 'continued-a1'),
    msg('assistant', 'continued-a2'),
    msg('assistant', 'continued-a3'),
  ];

  const tail = conversationTail(messages, { prefixWindow: 2, keepRounds: 3 });

  assert.deepEqual(tail.visible.map((m) => m.id), ['original-user', 'first-assistant', 'auto-continue-announcement', 'continued-a0', 'continued-a1', 'continued-a2', 'continued-a3']);
});

test('conversationTail keeps the full live run including announcements without a live hint', () => {
  const messages = [
    msg('user', 'original-user'),
    msg('assistant', 'first-assistant'),
    {
      role: 'assistant',
      id: 'auto-continue-announcement',
      tool_calls: [{ id: 'announce-x', name: 'announcement', args: { type: 'auto_continue' }, status: 'ok', output: 'notice' }],
    },
    msg('assistant', 'continued-a0'),
    msg('assistant', 'continued-a1'),
    msg('assistant', 'continued-a2'),
    msg('assistant', 'continued-a3'),
  ];

  const tail = conversationTail(messages, { prefixWindow: 2, keepRounds: 3 });

  assert.equal(tail.assistKeepStart, tail.runStart);
  assert.deepEqual(tail.visible.map((m) => m.id), [
    'original-user',
    'first-assistant',
    'auto-continue-announcement',
    'continued-a0',
    'continued-a1',
    'continued-a2',
    'continued-a3',
  ]);
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
