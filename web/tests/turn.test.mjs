import { test } from 'node:test';
import assert from 'node:assert/strict';
import { newTurn, applyFrame, snapshotSegments, historyItems, liveHistory, liveSegments, joinText, group, toolSummary, toolFailed, closeStatus, toolEnding, userLayout, formatDuration, toolClock, splitReplyQuote, relayName, turnEnds, toolRoom, shortHandle } from '../lib/turn.js';

const turn = () => newTurn({ turnId: 't1', requestId: 'r1', conversationId: 'c1', kind: 'send' });
const frame = (type, payload, seq) => ({ type, payload, seq });

test('deltas extend one text segment; a tool call starts a new one after it', () => {
  const t = turn();
  applyFrame(t, frame('turn.delta', { text: 'Hel' }, 0));
  applyFrame(t, frame('turn.delta', { text: 'lo' }, 1));
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 2));
  applyFrame(t, frame('turn.delta', { text: 'done' }, 3));
  assert.deepEqual(t.segments.map((s) => [s.kind, s.text ?? s.name]), [['text', 'Hello'], ['tool', 'exec'], ['text', 'done']]);
});

test('a retry drops the tail the failed attempt streamed', () => {
  const t = turn();
  applyFrame(t, frame('turn.reasoning', { text: 'memikirkan' }, 0));
  applyFrame(t, frame('turn.delta', { text: 'setengah jawaban' }, 1));
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 2));
  applyFrame(t, frame('turn.tool_input', { callId: 'a', text: '{"command":"ls"}' }, 3));
  applyFrame(t, frame('turn.phase', { phase: 'retry_wait', retryAfterMs: 2000 }, 4));
  assert.deepEqual(t.segments, [], 'nothing the failed attempt streamed survives');
  assert.equal(t.phase.phase, 'retry_wait', 'the pause is what the status line reads');
  // the next attempt fills the transcript from empty, so the two never splice
  applyFrame(t, frame('turn.delta', { text: 'jawaban yang utuh' }, 5));
  assert.deepEqual(t.segments.map((s) => [s.kind, s.text]), [['text', 'jawaban yang utuh']]);
});

test('a retry keeps what the store already holds', () => {
  const t = turn();
  // an answer the previous step committed, with its tool row settled
  applyFrame(t, frame('turn.delta', { text: 'langkah pertama' }, 0));
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 1));
  applyFrame(t, frame('turn.tool_result', { callId: 'a', output: 'ok' }, 2));
  applyFrame(t, frame('turn.delta', { text: 'jawaban yang gagal' }, 3));
  applyFrame(t, frame('turn.tool_call', { callId: 'b', name: 'read_file' }, 4));
  applyFrame(t, frame('turn.phase', { phase: 'retry_wait', retryAfterMs: 500 }, 5));
  // the tail goes; the settled row and the text before it stay …
  assert.deepEqual(t.segments.map((s) => [s.kind, s.text ?? s.name]), [['text', 'langkah pertama'], ['tool', 'exec']]);
  assert.equal(t.segments[1].status, 'success', 'a result that landed is persistence, not attempt output');
  // … and a second wait has nothing left to drop
  applyFrame(t, frame('turn.phase', { phase: 'retry_wait', retryAfterMs: 500 }, 6));
  assert.deepEqual(t.segments.map((s) => s.kind), ['text', 'tool']);
});

test('a retry leaves a settled tool at the end alone', () => {
  const t = turn();
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 0));
  applyFrame(t, frame('turn.tool_result', { callId: 'a', output: 'ok' }, 1));
  applyFrame(t, frame('turn.phase', { phase: 'retry_wait', retryAfterMs: 100 }, 2));
  assert.deepEqual(t.segments.map((s) => [s.kind, s.status]), [['tool', 'success']]);
});

test('a replayed or doubled seq is dropped', () => {
  const t = turn();
  assert.equal(applyFrame(t, frame('turn.delta', { text: 'a' }, 0)), true);
  assert.equal(applyFrame(t, frame('turn.delta', { text: 'a' }, 0)), false);
  assert.equal(applyFrame(t, frame('turn.delta', { text: 'b' }, 0)), false);
  assert.equal(t.segments[0].text, 'a');
});

test('a tool streams output, then settles with its result', () => {
  const t = turn();
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 0));
  applyFrame(t, frame('turn.tool_input', { callId: 'a', text: '{"command":' }, 1));
  applyFrame(t, frame('turn.tool_input', { callId: 'a', text: '"ls"}' }, 2));
  applyFrame(t, frame('turn.tool_output', { callId: 'a', text: 'x' }, 3));
  assert.equal(t.segments[0].status, 'streaming');
  applyFrame(t, frame('turn.tool_result', { callId: 'a', failed: false }, 4));
  assert.equal(t.segments[0].status, 'success');
  assert.equal(t.segments[0].input, '{"command":"ls"}');
  // A late chunk after the result is data, not a return to running.
  applyFrame(t, frame('turn.tool_output', { callId: 'a', text: 'y' }, 5));
  assert.equal(t.segments[0].status, 'success');
  assert.equal(t.segments[0].output, 'xy');
});

test('a tool with no result fails when the turn ends, and pending questions are withdrawn', () => {
  const t = turn();
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 0));
  applyFrame(t, frame('turn.approval', { id: 'q1' }, 1));
  applyFrame(t, frame('turn.elicitation', { id: 'e1' }, 2));
  applyFrame(t, frame('turn.failed', { status: 500 }, 3));
  assert.equal(t.segments[0].status, 'failed');
  assert.equal(t.segments[1].status, 'withdrawn');
  assert.equal(t.segments[2].status, 'withdrawn');
  assert.equal(t.terminal.type, 'turn.failed');
});

test('an answered approval keeps the decision', () => {
  const t = turn();
  applyFrame(t, frame('turn.approval', { id: 'q1' }, 0));
  applyFrame(t, frame('turn.approval_resolved', { id: 'q1', optionId: 'allow' }, 1));
  assert.deepEqual([t.segments[0].status, t.segments[0].optionId], ['answered', 'allow']);
});

test('a plan update replaces the previous plan', () => {
  const t = turn();
  applyFrame(t, frame('turn.plan', { entries: [{ content: 'a' }] }, 0));
  applyFrame(t, frame('turn.plan', { entries: [{ content: 'b' }, { content: 'c' }] }, 1));
  assert.equal(t.segments.filter((s) => s.kind === 'plan').length, 1);
  assert.equal(t.segments[0].entries.length, 2);
});

test('steering folds into one segment until its final chunk', () => {
  const t = turn();
  applyFrame(t, frame('turn.steer', { inputId: 'i1', text: 'use ', attachments: [{ uri: 'file:///a.png', mediaType: 'image/png', modality: 'image', name: 'a.png' }] }, 0));
  applyFrame(t, frame('turn.steer', { inputId: 'i1', text: 'go', final: true }, 1));
  applyFrame(t, frame('turn.steer', { inputId: 'i2', text: 'next', final: true }, 2));
  assert.deepEqual(t.segments.map((s) => s.text), ['use go', 'next']);
  // Uploads ride the first chunk only and survive the text concatenation.
  assert.deepEqual(t.segments[0].attachments.map((a) => a.name), ['a.png']);
  assert.deepEqual(t.segments[1].attachments, []);
});

test('toolFailed reads the failure conventions of stored output', () => {
  assert.equal(toolFailed('Error: nope'), true);
  assert.equal(toolFailed('failed'), true);
  assert.equal(toolFailed('ran\n(exit 2)'), true);
  assert.equal(toolFailed('ran\n(signal: killed)'), true);
  // an interrupted run is our own doing, not the command's failure
  assert.equal(toolFailed('partial\n(interrupted by user after 12.3s — partial output above)'), false);
  assert.equal(toolFailed('ok (exit 0)'), false);
  assert.equal(toolFailed(''), false);
});

test('toolSummary names the action and its subject', () => {
  assert.deepEqual(toolSummary('exec', '{"command":"ls -la\\nmore"}'), { verb: 'Ran', detail: 'ls -la' });
  assert.deepEqual(toolSummary('read_file', '{"path":"a.go"}'), { verb: 'Read', detail: 'a.go' });
  assert.deepEqual(toolSummary('web_search', '{"query":"x"}'), { verb: 'Searched web', detail: 'x' });
  // Input still streaming is not JSON yet.
  assert.deepEqual(toolSummary('exec', '{"comm'), { verb: 'Ran', detail: '' });
  assert.deepEqual(toolSummary('custom_tool', '{"thing":"v"}'), { verb: 'custom_tool', detail: 'v' });
});

test('history flattens messages into the same segment shapes', () => {
  const items = historyItems([
    { id: 'm1', role: 'user', content: 'hi' },
    { id: 'm2', role: 'user', userType: 'system', content: 'note' },
    {
      id: 'm3', role: 'assistant', finishReason: 'interrupted',
      parts: [
        { kind: 'reasoning', text: 'think' },
        { kind: 'tool_call', toolCallId: 'c1' },
        { kind: 'text', text: 'answer' },
      ],
      toolCalls: [{ id: 'c1', name: 'exec', input: '{}', output: 'ok', status: 'done' }],
      sentFiles: [{ name: 'a.png' }],
      receipt: 'read',
    },
  ]);
  assert.deepEqual(items.map((i) => i.kind), ['user', 'note', 'reasoning', 'tool', 'text', 'interrupted', 'delivery']);
  assert.equal(items.find((i) => i.kind === 'tool').status, 'settled');
  assert.equal(items.find((i) => i.kind === 'delivery').receipt, 'read');
});

test('history keeps a workspace injection as the tools\' folder, not a row', () => {
  const items = historyItems([{
    id: 'm1', role: 'assistant',
    parts: [{ kind: 'tool_call', toolCallId: 'w' }, { kind: 'tool_call', toolCallId: 'c' }],
    toolCalls: [
      { id: 'w', type: 'injection', name: 'workspace', input: '{"path":"/proj"}' },
      { id: 'c', name: 'exec', input: '{}', output: '', status: 'pending' },
    ],
  }]);
  assert.equal(items.length, 1);
  assert.equal(items[0].cwd, '/proj');
  assert.equal(items[0].status, 'pending');
});

test('an assistant message with only content and tool calls still reads in order', () => {
  const items = historyItems([{ id: 'm', role: 'assistant', content: 'text', toolCalls: [{ id: 'c', name: 'ls', input: '{}', output: '' }] }]);
  assert.deepEqual(items.map((i) => i.kind), ['text', 'tool']);
});

test('group folds consecutive work into one activity item', () => {
  const out = group([
    { id: 'a', kind: 'text' }, { id: 'b', kind: 'reasoning' }, { id: 'c', kind: 'tool' },
    { id: 'd', kind: 'text' }, { id: 'e', kind: 'tool' },
  ]);
  assert.deepEqual(out.map((i) => i.kind), ['text', 'activity', 'text', 'activity']);
  assert.equal(out[1].items.length, 2);
});

test('a chat reply splits into what it answers and what it says', () => {
  assert.deepEqual(splitReplyQuote('[In reply to: shall we?]\nyes'), { sender: '', quote: 'shall we?', head: 25, body: 'yes' });
  const group = splitReplyQuote('Ana: [In reply to: an image]\nlook at this');
  assert.deepEqual([group.sender, group.quote, group.body], ['Ana', 'an image', 'look at this']);
  // The head's length is what the stored selections were shifted by.
  assert.equal('Ana: [In reply to: an image]\nlook at this'.slice(group.head), 'look at this');
});

test('a reply with no text of its own is all quote', () => {
  const r = splitReplyQuote('[In reply to: a video]');
  assert.deepEqual([r.quote, r.body], ['a video', '']);
});

test('a quote that holds a colon or brackets is kept whole', () => {
  const r = splitReplyQuote('Bo: [In reply to: note: [draft] v2]\nok');
  assert.deepEqual([r.sender, r.quote, r.body], ['Bo', 'note: [draft] v2', 'ok']);
});

test('other messages are not replies', () => {
  assert.equal(splitReplyQuote('Ana: hello'), null);
  assert.equal(splitReplyQuote('hello\n[In reply to: x]'), null);
  assert.equal(splitReplyQuote(''), null);
  assert.equal(splitReplyQuote(undefined), null);
});

test('a tool result lists the media the call showed the model', () => {
  const t = turn();
  const shot = { mediaType: 'image/png', modality: 'image', uri: 'file:///tmp/shot.png', name: 'shot.png' };
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'read_media' }, 0));
  applyFrame(t, frame('turn.tool_result', { callId: 'a', failed: false, attachments: [shot] }, 1));
  assert.deepEqual(t.segments[0].attachments, [shot]);

  // A call with none leaves the segment without any.
  applyFrame(t, frame('turn.tool_call', { callId: 'b', name: 'exec' }, 2));
  applyFrame(t, frame('turn.tool_result', { callId: 'b', failed: false }, 3));
  assert.equal(t.segments[1].attachments, undefined);
});

test('history gives each tool row the media its call produced', () => {
  const att = { mediaType: 'video/mp4', modality: 'video', uri: 'file:///tmp/clip.mp4', name: 'clip.mp4' };
  const items = historyItems([{
    id: 'm', role: 'assistant',
    parts: [{ kind: 'tool_call', toolCallId: 'c1' }, { kind: 'tool_call', toolCallId: 'c2' }],
    toolCalls: [
      { id: 'c1', name: 'read_media', input: '{}', output: 'loaded', status: 'done', attachments: [att] },
      { id: 'c2', name: 'exec', input: '{}', output: 'ok', status: 'done' },
    ],
  }]);
  assert.deepEqual(items[0].attachments, [att]);
  assert.deepEqual(items[1].attachments, []);
});

test('a file the agent puts in its reply shows where it came, live and from history', () => {
  const chart = { mediaType: 'image/png', modality: 'image', uri: 'file:///data/uploads/u_1/chart.png', name: 'chart.png' };
  const t = turn();
  applyFrame(t, frame('turn.delta', { text: 'Here:' }, 0));
  applyFrame(t, frame('turn.media', chart, 1));
  applyFrame(t, frame('turn.delta', { text: 'Done.' }, 2));
  assert.deepEqual(t.segments.map((s) => s.kind), ['text', 'media', 'text']);
  assert.deepEqual(t.segments[1].attachments, [chart]);

  const items = historyItems([{
    id: 'm1', role: 'assistant', content: 'Here:Done.',
    parts: [{ kind: 'text', text: 'Here:' }, { kind: 'media', text: JSON.stringify(chart) }, { kind: 'media', text: 'not json' }, { kind: 'text', text: 'Done.' }],
  }]);
  assert.deepEqual(items.map((i) => i.kind), ['text', 'media', 'text']);
  assert.deepEqual(items[1].attachments, [chart]);
});

test('a stored steer part projects its uploads, even without text', () => {
  const items = historyItems([{
    id: 'm1', role: 'assistant', content: 'done',
    parts: [
      { kind: 'steer', text: '', attachments: [{ uri: 'file:///a.png', mediaType: 'image/png', modality: 'image', name: 'a.png' }] },
      { kind: 'steer', text: 'go left', attachments: [] },
    ],
  }]);
  const steers = items.filter((i) => i.kind === 'steer');
  assert.equal(steers.length, 2);
  assert.equal(steers[0].text, '');
  assert.deepEqual(steers[0].attachments.map((a) => a.name), ['a.png']);
  assert.equal(steers[1].text, 'go left');
});

test('turn.opened adopts the server clock; a fresh turn falls back to now', () => {
  const t = turn();
  applyFrame(t, frame('turn.opened', { startedAt: 1700000000000 }, 0));
  assert.equal(t.startedAt, 1700000000000);
  const bare = turn();
  assert.ok(Math.abs(bare.startedAt - Date.now()) < 5000);
});

test('snapshotSegments rebuilds live content; live frames merge after it', () => {
  const t = turn();
  snapshotSegments(t, [
    { kind: 'text', text: 'partial ' },
    { kind: 'reasoning', text: 'hmm' },
    { kind: 'tool', callId: 'a', name: 'exec', input: '{"command":"ls"}', output: 'half', status: 'streaming' },
    { kind: 'steer', inputId: 'i1', text: 'go', final: true },
    { kind: 'plan', entries: [{ step: 'one' }] },
    { kind: 'approval', request: { id: 'q1', title: 'Run?' }, status: 'pending' },
    { kind: 'elicitation', request: { id: 'e1', message: 'name?' }, status: 'answered', action: 'accept' },
    { kind: 'media', attachments: [{ uri: 'file:///x.png' }] },
  ]);
  assert.deepEqual(t.segments.map((s) => s.kind), ['text', 'reasoning', 'tool', 'steer', 'plan', 'approval', 'elicitation', 'media']);
  // A text tail keeps merging — a frame continuing the same prose lands
  // inside the snapshot's last text segment, not beside it.
  t.segments.push({ id: 't1:snap:9', kind: 'text', text: 'still ' });
  applyFrame(t, frame('turn.delta', { text: 'writing' }, 40));
  assert.equal(t.segments[8].text, 'still writing');
  assert.equal(t.segments.length, 9);
  // Late resolutions land on the snapshot's cards, not a duplicate.
  applyFrame(t, frame('turn.approval_resolved', { id: 'q1', optionId: 'yes' }, 41));
  assert.equal(t.segments[5].status, 'answered');
  assert.equal(t.segments[5].optionId, 'yes');
  applyFrame(t, frame('turn.tool_output', { callId: 'a', text: 'way' }, 42));
  assert.equal(t.segments[2].output, 'halfway');
});

// A relayed prompt's header is drawn from the provenance the server stored
// beside it — { id, label, title } — never from the message text. The body
// is only what the sender wrote, so nothing is cut or parsed.
test('a relay header is named by its provenance, not its text', () => {
  const item = historyItems([{
    id: 'm1', role: 'user', userType: 'relay', content: 'ship it',
    relay: { id: 'c_self', label: 'otter', title: 'Planner' },
  }])[0];
  assert.equal(item.relayed, true);
  assert.deepEqual(item.relay, { id: 'c_self', label: 'otter', title: 'Planner' });
  assert.equal(item.msg.content, 'ship it', 'the body is only what the sender wrote');
  assert.equal(relayName(item.relay), '@otter');
  // label empty → title names the sender
  assert.equal(relayName({ id: 'c_A1', label: '', title: 'Untitled thing' }), 'Untitled thing');
  // both empty → a short form of the thread id
  assert.equal(relayName({ id: 'c_A1', label: '', title: '' }), 'c_A1');
  const long = relayName({ id: 'c_4TCLFPOTI7LSWB4FYSYZWY4V5N', label: '', title: '' });
  assert.equal(long, 'c_4TCLFPOTI7…', 'a long id still shortens');
  // no provenance at all renders nothing — a relay the server marks but
  // whose record predates it is a plain message
  const bare = historyItems([{ id: 'm2', role: 'user', userType: 'relay', content: 'older head-in-text' }])[0];
  assert.equal(bare.relay, undefined, 'no head to draw from data');
});

test('turnEnds marks each turn\'s last item with the Markdown the turn said', () => {
  const items = [
    { kind: 'user' },
    { kind: 'text', text: 'Looking.' },
    { kind: 'activity' },
    { kind: 'text', text: '## Done\n\n- **one**\n' },
    { kind: 'delivery' },
    { kind: 'user' },
    { kind: 'activity' },
    { kind: 'user' },
    { kind: 'text', text: 'ok' },
  ];
  assert.deepEqual([...turnEnds(items)], [[4, 'Looking.\n\n## Done\n\n- **one**'], [8, 'ok']]);
  assert.equal(turnEnds([]).size, 0);
});

test('liveHistory trims history only where the live turn replaces it', () => {
  // A relayed prompt (another thread, a chat, another browser) lands in
  // history after the turn opens. Until the room has read it, the newest
  // prompt in history belongs to the previous turn — and the answer after
  // it is content the live stream never reproduces, so a send keeps it.
  const pending = [
    { id: 'm1', kind: 'user', msg: { content: 'kalo di windows harusnya X:/ ga sih?' } },
    { id: 'm2', kind: 'text', text: 'Betul — C:\\ cuma contoh.' },
  ];
  assert.deepEqual(liveHistory(pending, { kind: 'send', adopted: true }), pending);
  const answered = [...pending, { id: 'm3', kind: 'user', msg: { content: 'and Z:?' } }, { id: 'm4', kind: 'text', text: 'also a drive' }];
  // A send never trims: its prompt is the newest message, so anything
  // history already holds is older than the live turn.
  assert.deepEqual(liveHistory(answered, { kind: 'send' }), answered);
  // A retry this tab started re-answers the newest prompt: the stored
  // answer after it is what the live tail replaces, so history stops there.
  assert.deepEqual(liveHistory(answered, { kind: 'resume' }).map((x) => x.id), ['m1', 'm2', 'm3']);
  // A retry adopted from elsewhere cannot tell its prompt from an older one
  // a stale page ends on, so it trims nothing.
  assert.deepEqual(liveHistory(answered, { kind: 'resume', adopted: true }), answered);
  // An edit replaces the message it names; history stops ahead of it.
  assert.deepEqual(liveHistory(answered, { kind: 'edit', editOf: 'm3' }).map((x) => x.id), ['m1', 'm2']);
  assert.deepEqual(liveHistory(answered, { kind: 'edit', editOf: 'gone' }), answered);
  // No live turn leaves history alone.
  assert.deepEqual(liveHistory(answered, null), answered);
});

test('a steer already in history is not painted twice', () => {
  // The server writes a steered input as an ordinary user message that
  // carries the inputId of the turn.steer command (STEER_INPUT), and the
  // journal replays the same steer. A tab that reloads mid-turn holds both.
  const messages = [
    { id: 'm1', role: 'user', content: 'start' },
    { id: 'm2', role: 'user', content: 'tambahan', inputId: 'req-1', annotations: [{ marker: 1, comment: 'catatan' }] },
  ];
  const items = historyItems(messages);
  assert.deepEqual(items.map((x) => x.kind), ['user', 'user']);
  assert.equal(items[1].steerInput, 'req-1');
  assert.equal(items[0].steerInput, '');
  const live = {
    segments: [
      { id: 't:1', kind: 'text', text: 'jalan' },
      { id: 't:2', kind: 'steer', inputId: 'req-1', text: 'tambahan' },
      { id: 't:3', kind: 'steer', inputId: 'req-2', text: 'belum ditulis' },
      { id: 't:4', kind: 'steer', text: 'dari CLI, tanpa inputId' },
      { id: 't:5', kind: 'tool', name: 'exec' },
    ],
  };
  // the row for req-1 is in history, so its live bubble is replaced by the
  // record row in the live segment's place; the others stay
  assert.deepEqual(liveSegments(live, items).map((s) => s.id), ['t:1', 'm2', 't:3', 't:4', 't:5']);
  assert.equal(liveSegments(live, items)[1].msg.annotations.length, 1, 'drawn from the record, annotations and all');
  // a page that has not been written yet leaves the live bubble in place
  assert.deepEqual(liveSegments(live, historyItems([messages[0]])).map((s) => s.id), ['t:1', 't:2', 't:3', 't:4', 't:5']);
  // nothing live, nothing to filter
  assert.deepEqual(liveSegments(null, items), []);
});

// The record row of a steer belongs where the steer landed, not where history
// parked it: the text that streamed before it must stay above it.
test('a steer the record holds is drawn where its live segment sits', () => {
  const messages = [
    { id: 'm1', role: 'user', content: 'mulai' },
    { id: 'm2', role: 'user', content: 'tambahan', inputId: 'req-x', annotations: [{ marker: 1, comment: 'catatan' }] },
  ];
  const items = historyItems(messages);
  const live = {
    segments: [
      { id: 't:1', kind: 'text', text: 'sebelum' },
      { id: 't:2', kind: 'steer', inputId: 'req-x', text: 'tambahan', final: true },
      { id: 't:3', kind: 'text', text: 'sesudah' },
    ],
  };
  const tail = liveSegments(live, items);
  assert.deepEqual(tail.map((x) => x.kind), ['text', 'user', 'text'], 'the record sits between the texts');
  assert.deepEqual(tail.map((x) => x.id), ['t:1', 'm2', 't:3']);
  assert.equal(tail[1].msg.annotations.length, 1, 'drawn from the record, annotations intact');
  // no live turn: there is no tail at all, so the row is drawn from history
  assert.deepEqual(liveSegments(null, items), []);
  assert.deepEqual(items.map((x) => x.id), ['m1', 'm2'], 'history keeps the row at its own place');
  // a steer the record does not hold keeps its live bubble
  const open = { segments: [{ id: 't:9', kind: 'steer', inputId: 'req-unknown', text: 'baru', final: true }] };
  assert.deepEqual(liveSegments(open, items).map((x) => x.id), ['t:9']);
});

// A part boundary is where the store seals a step, not where the answer
// breaks: two parts split mid-sentence or mid-fence must draw as one block.
test('neighbouring text parts are one block, and a fenced block stays whole', () => {
  const t = turn();
  snapshotSegments(t, [
    { kind: 'text', text: 'lihat ini:\n\n```js\nconst a = 1;\n' },
    { kind: 'text', text: 'const b = 2;\n```\n\nselesai' },
    { kind: 'reasoning', text: 'mempertimbangkan' },
    { kind: 'text', text: 'penutup' },
  ]);
  const joined = joinText(t.segments);
  assert.deepEqual(joined.map((x) => x.kind), ['text', 'reasoning', 'text']);
  assert.equal(joined[0].text, 'lihat ini:\n\n```js\nconst a = 1;\nconst b = 2;\n```\n\nselesai');
  assert.equal(joined[0].id, t.segments[0].id, 'the first id keeps the DOM key while the text grows');
  // copied text is what was written: no separator inserted at the part boundary
  const copied = [...turnEnds(group(joined)).values()][0];
  assert.ok(copied.startsWith('lihat ini:\n\n```js\nconst a = 1;\nconst b = 2;\n```'), copied);
  // a tool between two texts is a real boundary and keeps them apart
  const split = joinText([
    { id: 'a', kind: 'text', text: 'satu' },
    { id: 'b', kind: 'tool', callId: 'c1', name: 'exec', status: 'success' },
    { id: 'c', kind: 'text', text: 'dua' },
  ]);
  assert.deepEqual(split.map((x) => x.id), ['a', 'b', 'c']);
});

// A tab that reloads mid-turn replays the journal from its first frame while
// history already holds the steps that finished before the reload. Drawn
// together, one turn reads as two: the same prose twice, between two separate
// "Used N tools" runs that never merge.
test('a replayed step the record already holds is not drawn twice', () => {
  const messages = [
    { id: 'm1', role: 'user', content: 'go' },
    {
      id: 'm2', role: 'assistant', content: '', parts: [
        { kind: 'text', text: 'Now the tests:' },
        { kind: 'tool_call', toolCallId: 'c1' },
      ], toolCalls: [{ id: 'c1', name: 'exec', input: '{}', output: 'ok' }],
    },
  ];
  const items = historyItems(messages);
  assert.ok(items.some((x) => x.callId === 'c1'), 'the record holds the call');
  const replayed = {
    segments: [
      { id: 't:1', kind: 'text', text: 'Now the tests:' },
      { id: 't:2', kind: 'tool', callId: 'c1', name: 'exec', status: 'success' },
    ],
  };
  assert.deepEqual(liveSegments(replayed, items), [], 'the record already draws that step');

  // the replay continues past the record: only the part it does not hold stays
  const running = {
    segments: [
      { id: 't:1', kind: 'text', text: 'Now the tests:' },
      { id: 't:2', kind: 'tool', callId: 'c1', name: 'exec', status: 'success' },
      { id: 't:3', kind: 'text', text: 'hasilnya begini' },
      { id: 't:4', kind: 'tool', callId: 'c2', name: 'exec', status: 'requested' },
    ],
  };
  assert.deepEqual(liveSegments(running, items).map((s) => s.id), ['t:3', 't:4']);

  // nothing of this turn in the record — the tab's own tail is untouched
  const own = historyItems([{ id: 'm1', role: 'user', content: 'go' }]);
  assert.deepEqual(liveSegments(running, own).map((s) => s.id), ['t:1', 't:2', 't:3', 't:4']);

  // a marker saying output is missing survives: it reports the gap, it is not a copy
  const marked = {
    segments: [
      { id: 't:0', kind: 'resync', text: 'Some earlier live output is unavailable' },
      { id: 't:1', kind: 'text', text: 'Now the tests:' },
      { id: 't:2', kind: 'tool', callId: 'c1', name: 'exec', status: 'success' },
    ],
  };
  assert.deepEqual(liveSegments(marked, items).map((s) => s.kind), ['resync']);
});

test('a recalled steer leaves the live transcript, and its history row stays', () => {
  const t = newTurn({ turnId: 't1', requestId: 'r1', conversationId: 'c1', kind: 'send' });
  applyFrame(t, frame('turn.steer', { inputId: 'req-1', text: 'tambahan', final: true }, 1));
  applyFrame(t, frame('turn.steer', { inputId: 'req-2', text: 'lain', final: true }, 2));
  assert.deepEqual(t.segments.map((s) => s.inputId || s.kind), ['req-1', 'req-2']);
  applyFrame(t, frame('turn.steer_recalled', { inputId: 'req-1' }, 3));
  assert.deepEqual(t.segments.map((s) => s.inputId || s.kind), ['req-2']);
});

// A row the tab replayed or resynced never saw the call start: its own clock
// would say "0s" for a 40-second call. The runner's status line carries the
// duration it measured, so a finished row reads that instead.
test('a finished row takes its duration from the runner, not from the tab clock', () => {
  const now = Date.now();
  const replayed = {
    kind: 'tool', status: 'success',
    at: now,                                  // the frame arrived just now …
    took: 0,                                  // … so the tab's own measure is nothing
    output: 'make: *** [test] Error 1\n(exit 1, 43.2s)',
  };
  assert.equal(toolClock(replayed, now), '43s', 'the runner measured it, the tab did not');
  assert.equal(toolClock({ ...replayed, output: 'done\n(exit 0, 4.4s)' }, now), '4s');
  assert.equal(toolClock({ ...replayed, output: '(no output, exit 0, 0.3s)' }, now), '0s');
  assert.equal(toolClock({ ...replayed, output: 'x\n(signal: killed, 3.2s)' }, now), '3s');
  assert.equal(toolClock({ ...replayed, output: 'x\n(interrupted by user after 12.3s — partial output above)' }, now), '12s');

  // a running row has no status line yet: the ticker from its start is all there is
  const running = { kind: 'tool', status: 'requested', at: now - 33_000, output: '' };
  assert.equal(toolClock(running, now), '33s');
  assert.equal(toolClock(running, now + 2000), '35s');
  // a call the tab watched end, with no status line of its own, keeps its measure
  assert.equal(toolClock({ kind: 'tool', status: 'success', at: now - 9000, took: 9000, output: 'ok' }, now), '9s');
});

// A running tool row shows how long it has been running, and a finished one
// shows what it took: the difference is whether the label still moves.
test('a running tool row counts up, and stops when the call does', () => {
  const t = turn();
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 0));
  const row = t.segments[0];
  assert.ok(row.at > 0, 'the call remembers when this tab saw it start');

  // the row's label grows with the clock while it runs
  assert.equal(toolClock(row, row.at + 33_000), '33s');
  assert.equal(toolClock(row, row.at + 34_000), '34s');
  assert.equal(toolClock(row, row.at + 61_000), '1m 01s');

  // the result stores one duration; from then on the label is that, whatever
  // the clock says — nothing left to tick
  applyFrame(t, frame('turn.tool_result', { callId: 'a', output: 'ok' }, 1));
  assert.equal(row.status, 'success');
  assert.ok(row.took >= 0);
  const fixed = toolClock(row, row.at + 5_000);
  assert.equal(toolClock(row, row.at + 9_000), fixed, 'a finished row does not move');
  assert.equal(toolClock({ ...row, at: 0, took: 43_000 }, 1_000_000), '43s');

  // nothing to say about a row whose start was never seen (history)
  assert.equal(toolClock({ kind: 'tool', status: 'success' }), '');
  assert.equal(toolClock(null), '');
});

test('a call the turn closes without a result still gets a final duration', () => {
  const t = turn();
  applyFrame(t, frame('turn.tool_call', { callId: 'a', name: 'exec' }, 0));
  applyFrame(t, frame('turn.tool_output', { callId: 'a', text: 'partial' }, 1));
  applyFrame(t, frame('turn.failed', { status: 502, message: 'upstream' }, 2));
  const row = t.segments[0];
  assert.equal(row.status, 'failed');
  assert.ok(row.took >= 0, 'the clock closed with the turn');
  assert.equal(toolClock({ ...row, took: 12_400 }, 0), '12s', 'and it stays put afterwards');
});

test('durations read as a person would say them', () => {
  assert.deepEqual(
    [0, 999, 1000, 33_000, 59_900, 60_000, 61_000, 3_661_000].map(formatDuration),
    ['0s', '1s', '1s', '33s', '1m 00s', '1m 00s', '1m 01s', '1h 01m'],
  );
});

// exec closes its output with a status line that names how the process ended;
// the row reads that line and shows the output without it.
test('every status line exec writes is read, and none of it leaks into the output', () => {
  const cases = [
    ['(exit 0, 4.4s)', { kind: 'exit', code: 0, ms: 4400 }],
    ['(exit 1, 43.2s)', { kind: 'exit', code: 1, ms: 43200 }],
    ['(no output, exit 0, 0.3s)', { kind: 'exit', code: 0, ms: 300 }],
    ['(signal: killed, 3.2s)', { kind: 'signal', signal: 'killed', ms: 3200 }],
    ['(timed out after 120s — partial output above; rerun with exec_session for a long run, or raise timeout_ms)', { kind: 'timeout', after: '120s', ms: 120000 }],
    ['(interrupted by user after 12.3s — partial output above)', { kind: 'interrupted', by: 'user', ms: 12300 }],
    ['(interrupted by agent after 4.1s — partial output above)', { kind: 'interrupted', by: 'agent', ms: 4100 }],
    ['(nusashell restarted after 8.0s — partial output above)', { kind: 'interrupted', by: 'server', ms: 8000 }],
    ['(canceled after 12.3s — partial output above)', { kind: 'canceled', ms: 12300 }],
  ];
  for (const [line, close] of cases) {
    const out = closeStatus('done\n' + line);
    assert.deepEqual(out.close, close, line);
    assert.equal(out.body, 'done', line);
  }
  // a status-only result has no body at all
  assert.deepEqual(closeStatus('(no output, exit 0, 0.3s)'), { body: '', close: { kind: 'exit', code: 0, ms: 300 } });
  // the duration is optional: it says how long, never what happened
  assert.deepEqual(closeStatus('x\n(exit 2)').close, { kind: 'exit', code: 2, ms: undefined });
  assert.deepEqual(closeStatus('done\n(exit 0)').close, { kind: 'exit', code: 0, ms: undefined });
  // a stored output may end with the newline that followed the status line
  assert.deepEqual(closeStatus('done\n(exit 1, 8.4s)\n').close, { kind: 'exit', code: 1, ms: 8400 });
  // text that merely ends in parentheses is not a status line
  assert.equal(closeStatus('done\n(see the log)'), null);
  assert.equal(closeStatus('done'), null);
  assert.equal(closeStatus(''), null);
});

// A slow run's hint sits ABOVE the status line, so the last line is still the
// status the client reads. The reversed order — status, then hint — made a
// failed 25-second command render as a success (B-019); both shapes are pinned
// here.
test('a long-run hint above the status line keeps the exit code readable', () => {
  const failed = 'boom\nran 25s; use exec_session and poll for runs like this\n(exit 3, 25.0s)';
  assert.deepEqual(closeStatus(failed).close, { kind: 'exit', code: 3, ms: 25000 });
  assert.equal(toolFailed(failed), true);
  const ending = toolEnding({ status: 'settled', output: failed });
  assert.deepEqual([ending.kind, ending.code, ending.word], ['exit', 3, 'exit 3']);

  const clean = 'done\nran 21s; use exec_session and poll for runs like this\n(exit 0, 21.0s)';
  assert.equal(toolEnding({ status: 'settled', output: clean }).kind, 'ok');
  assert.equal(toolFailed(clean), false);

  // The shape the fix removed: a status line buried above a trailing hint is
  // not a status the client can read, and the call would read as a success.
  assert.equal(closeStatus('done\n(exit 3, 25.0s)\nran 25s; use exec_session and poll for runs like this'), null);
});

test('a command that exited nonzero counts as failed; one we stopped does not', () => {
  assert.equal(toolFailed('done\n(exit 0, 4.4s)'), false);
  assert.equal(toolFailed('done\n(exit 1, 43.2s)'), true);
  assert.equal(toolFailed('done\n(exit 127, 0.1s)'), true);
  assert.equal(toolFailed('(no output, exit 0, 0.3s)'), false);
  assert.equal(toolFailed('done\n(signal: killed, 3.2s)'), true, 'a signal that killed it is a failure');
  // our own hand: a cap, a Stop, a restart is not the command failing
  assert.equal(toolFailed('partial\n(timed out after 120s — partial output above; rerun with exec_session for a long run, or raise timeout_ms)'), false);
  assert.equal(toolFailed('partial\n(interrupted by user after 12.3s — partial output above)'), false);
  assert.equal(toolFailed('partial\n(interrupted by agent after 4.1s — partial output above)'), false);
  assert.equal(toolFailed('partial\n(nusashell restarted after 8.0s — partial output above)'), false);
  assert.equal(toolFailed('partial\n(canceled after 12.3s — partial output above)'), false);
  // other tools keep their own wording
  assert.equal(toolFailed('error: no such file'), true);
  assert.equal(toolFailed('all good'), false);
});

// One value says how a call ended, and the row, its footer, and the group's
// count all read it. These are the ten shapes the runner can leave behind.
test('toolEnding names every ending once', () => {
  const cases = [
    [{ status: 'streaming', at: 0 }, 'running', 'live', '', ''],
    [{ status: 'settled', output: 'done\n(exit 0, 2.1s)' }, 'ok', 'ok', '', ''],
    [{ status: 'settled', output: 'FAIL\n(exit 1, 8.4s)' }, 'exit', 'bad', 'exit 1', 'bad'],
    [{ status: 'settled', output: 'x\n(signal: killed, 3.2s)' }, 'signal', 'bad', 'killed', 'bad'],
    [{ status: 'settled', output: 'x\n(timed out after 120s — partial output above)' }, 'timeout', 'warn', 'timed out', 'warn'],
    [{ status: 'settled', output: 'x\n(interrupted by user after 12.3s — partial)' }, 'stopped', 'ring', 'stopped', 'muted'],
    [{ status: 'settled', output: 'x\n(interrupted by agent after 4.1s — partial)' }, 'stopped', 'ring', 'stopped by agent', 'muted'],
    [{ status: 'settled', output: 'x\n(nusashell restarted after 8.0s — partial)' }, 'stopped', 'ring', 'restarted', 'muted'],
    [{ status: 'settled', output: 'x\n(canceled after 12.3s — partial)' }, 'canceled', 'ring', 'canceled', 'muted'],
    [{ status: 'pending' }, 'pending', 'none', 'no result', 'muted'],
  ];
  for (const [item, kind, mark, word, tone] of cases) {
    const got = toolEnding(item);
    assert.deepEqual([got.kind, got.mark, got.word, got.tone], [kind, mark, word, tone], JSON.stringify(item));
  }
  // the backend can call a clean exit a failure — it is authoritative
  assert.equal(toolEnding({ status: 'failed', output: 'done\n(exit 0, 1.0s)' }).kind, 'failed');
  // no status line: the wording of a stored result decides
  assert.equal(toolEnding({ status: 'settled', output: 'error: no such file' }).kind, 'failed');
  assert.equal(toolEnding({ status: 'success', output: 'ok' }).kind, 'ok');
  // the footer detail each ending carries
  assert.equal(toolEnding({ status: 'settled', output: '(exit 1, 1s)' }).code, 1);
  assert.equal(toolEnding({ status: 'settled', output: '(timed out after 120s — x)' }).cap, '120s');
  assert.equal(toolEnding({ status: 'settled', output: '(interrupted by agent after 1s — x)' }).by, 'agent');
});

test('a group counts failures and timeouts, never stops', () => {
  const kinds = (items) => items.map((i) => toolEnding(i).kind);
  const one = { status: 'settled', output: 'x\n(exit 1, 1s)' };
  const timeout = { status: 'settled', output: 'x\n(timed out after 120s — y)' };
  const stop = { status: 'settled', output: 'x\n(interrupted by user after 1s — y)' };
  const cancel = { status: 'settled', output: 'x\n(canceled after 1s — y)' };
  const clean = { status: 'settled', output: 'x\n(exit 0, 1s)' };
  assert.deepEqual(kinds([clean, clean, clean, clean]), ['ok', 'ok', 'ok', 'ok']);
  assert.deepEqual(kinds([clean, one, clean, clean]), ['ok', 'exit', 'ok', 'ok']);
  assert.deepEqual(kinds([clean, timeout, clean, clean]), ['ok', 'timeout', 'ok', 'ok']);
  assert.deepEqual(kinds([clean, stop, cancel, clean]), ['ok', 'stopped', 'canceled', 'ok']);
});

// Whether a prompt came from another thread is the server's word (userType
// 'relay'), not something read back out of the text: a prompt the owner starts
// with "[From thread …]" is still theirs to edit.
test('a relayed prompt is told by its field, not by its text', () => {
  const head = '[From thread @raven Audit (c_4TCLFPOTI7LSWB4FYSYZWY4V5N)]\n[Your answer here does not reach that thread. Use the send tool.]\n\nhere is the file';

  // the server says relay: its artefacts stay inside the block, no edit
  const relayed = userLayout(true);
  assert.deepEqual(relayed, { relayed: true, extras: 'relay', editable: false });

  // an owner message that merely starts like a relay is the owner's own
  const own = userLayout(false);
  assert.deepEqual(own, { relayed: false, extras: 'own', editable: true });
  const ownItem = historyItems([{ id: 'm1', role: 'user', content: head }])[0];
  assert.equal(ownItem.relayed, false, 'the text does not decide it');
  assert.equal(userLayout(ownItem.relayed).editable, true, 'so its Edit stays');

  // a relay the server marks but whose provenance is missing (a record that
  // predates the field) is still not editable — and renders as a plain
  // message, its head not guessed from the text
  const bare = historyItems([{ id: 'm2', role: 'user', content: 'no head here', userType: 'relay' }])[0];
  assert.equal(bare.relayed, true);
  assert.equal(bare.relay, undefined);
  assert.equal(userLayout(bare.relayed).editable, false);

  // a note is not a prompt
  assert.equal(historyItems([{ id: 'm3', role: 'user', userType: 'system', content: 'note' }])[0].kind, 'note');
});

// A compaction checkpoint is a boundary, not a message: its own kind once,
// between what came before it and what followed — never a note, never a
// prompt, and the transcript pipeline leaves it in place.
test('a compaction checkpoint becomes one divider between the messages it seals', () => {
  const messages = [
    { id: 'm1', role: 'user', content: 'earlier ask' },
    { id: 'm2', role: 'assistant', content: 'earlier answer' },
    { id: 'm3', role: 'user', userType: 'compaction', content: '[COMPACTION CHECKPOINT]\n\n…summary…' },
    { id: 'm4', role: 'assistant', content: 'after the seal' },
    { id: 'm5', role: 'user', content: 'next ask' },
  ];
  // the raw flattening: one item, in place
  const kinds = historyItems(messages).map((i) => i.kind);
  assert.deepEqual(kinds, ['user', 'text', 'compaction', 'text', 'user']);
  assert.equal(kinds.indexOf('compaction'), 2, 'between the sealed turn and what followed');
  // and it survives the transcript's own pipeline untouched, once
  const piped = group(joinText(historyItems(messages))).map((i) => i.kind);
  assert.deepEqual(piped, ['user', 'text', 'compaction', 'text', 'user']);
  assert.equal(piped.filter((k) => k === 'compaction').length, 1, 'one divider per checkpoint');

  // a plain system note stays a note
  assert.equal(historyItems([{ id: 'm6', role: 'user', userType: 'system', content: 'note' }])[0].kind, 'note');
  // the owner's own and a relayed prompt are still prompts
  const relay = historyItems([{ id: 'm7', role: 'user', userType: 'relay', content: 'x' }])[0];
  assert.equal(relay.kind, 'user');
  assert.equal(relay.relayed, true);
});

// A tool row that points at another room shows a name, never a raw id: the
// label, else the title, else the last six characters of the id as a handle.
// The name is read from the tool's own result — the thread object the server
// answered with — and while the call still runs, from what the input named.
const thread = { id: 'c_LOAFF7RSKLSUR25VRMTZQIJEYI', label: 'dog', title: 'FE work' };

test('a room tool reads the label and the title from its own result', () => {
  const sent = toolRoom('conversation_send', '{"conversationId":"c_LOAFF7RSKLSUR25VRMTZQIJEYI","text":"hi"}',
    JSON.stringify({ threadId: 'c_LOAFF7RSKLSUR25VRMTZQIJEYI', delivery: 'queued', position: 1, thread }));
  assert.deepEqual(sent, { verb: 'Sent', detail: '', names: [{ label: 'dog', title: 'FE work', short: '' }] });
  // one shape for every room tool, so they cannot drift apart
  for (const name of ['conversation_stop', 'conversation_read', 'conversation_wait']) {
    const out = toolRoom(name, '{"conversationId":"c_1"}', JSON.stringify({ thread }));
    assert.deepEqual(out.names, [{ label: 'dog', title: 'FE work', short: '' }], name);
  }
});

test('a thread with no label reads as its quoted title, with no at-sign to invent a handle', () => {
  const out = toolRoom('conversation_read', '{"conversationId":"c_1"}', JSON.stringify({ thread: { id: 'c_1', title: 'Read file di luar folder' } }));
  assert.deepEqual(out.names, [{ label: '', title: 'Read file di luar folder', short: '' }]);
});

test('a thread with neither label nor title reads as the last six of its id', () => {
  const out = toolRoom('conversation_read', '{"conversationId":"c_1"}', JSON.stringify({ thread: { id: 'c_LOAFF7RSKLSUR25VRMTZQIJEYI', title: '' } }));
  assert.deepEqual(out.names, [{ label: '', title: '', short: '@QIJEYI' }]);
});

test('a row still streaming shows the short id, and the result’s label replaces it', () => {
  const input = '{"conversationId":"c_LOAFF7RSKLSUR25VRMTZQIJEYI","text":"hi"}';
  // no output yet: never blank, and never a name guessed from the id
  assert.deepEqual(toolRoom('conversation_send', input, '').names, [{ label: '', title: '', short: '@QIJEYI' }]);
  // a partial input is still the id the caller asked for, said short until the rest arrives
  assert.deepEqual(toolRoom('conversation_send', '{"conversationId":"c_LOAFF', '').names, [{ label: '', title: '', short: '@_LOAFF' }]);
  // the result carries the name, so the handle goes away
  const landed = toolRoom('conversation_send', input, JSON.stringify({ thread }));
  assert.deepEqual(landed.names, [{ label: 'dog', title: 'FE work', short: '' }]);
  // A label in the input is not a name: it is read as an opaque handle, so a
  // model that writes a room's name cannot put that name in the transcript —
  // and a call that fails never gets a result to correct it.
  assert.deepEqual(toolRoom('conversation_stop', '{"conversationId":"@dog"}', '').names, [{ label: '', title: '', short: '@dog' }]);
  assert.deepEqual(toolRoom('conversation_stop', '{"conversationId":"@jabberwocky"}', '').names, [{ label: '', title: '', short: '@rwocky' }],
    'a longer one is visibly a handle, not the name it was written as');
  assert.equal(shortHandle('c_LOAFF7RSKLSUR25VRMTZQIJEYI'), '@QIJEYI');
  assert.equal(shortHandle(''), '');
});

test('one search result naming two rooms renders two names, in one shape', () => {
  const out = toolRoom('conversation_search', '{"query":"prepend scroll"}', JSON.stringify({
    hits: [
      { threadId: 'c_A', label: 'dog', title: 'FE work', seq: 4, snippet: '…' },
      { threadId: 'c_B', label: '', title: 'Read file di luar folder', seq: 9, snippet: '…' },
      { threadId: 'c_C', title: '', seq: 2, snippet: '…' },
    ],
  }));
  assert.equal(out.verb, 'Searched');
  assert.equal(out.detail, 'prepend scroll', 'the query still says what was searched');
  assert.deepEqual(out.names, [
    { label: 'dog', title: 'FE work', short: '' },
    { label: '', title: 'Read file di luar folder', short: '' },
    { label: '', title: '', short: '@c_C' },
  ]);
});

test('a tool that names no room keeps the summary of its arguments', () => {
  assert.equal(toolRoom('exec', '{"command":"ls"}', 'out'), null);
  assert.equal(toolRoom('conversation_list', '{}', '{}'), null, 'a listing is not a row about one room');
  // a spawn answers the id it opened, before any name exists
  assert.deepEqual(toolRoom('conversation_spawn', '{"prompt":"go"}', '{"threadId":"c_NEW123456"}').names,
    [{ label: '', title: '', short: '@123456' }]);
  // an export answers a file, not a thread object
  assert.deepEqual(toolRoom('conversation_export', '{"conversationId":"c_1"}', '{"path":"out/a.jsonl","threads":1}'),
    { verb: 'Exported', detail: 'out/a.jsonl', names: [] });
});
