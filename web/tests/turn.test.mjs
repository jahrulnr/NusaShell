import { test } from 'node:test';
import assert from 'node:assert/strict';
import { newTurn, applyFrame, snapshotSegments, historyItems, liveHistory, group, toolSummary, toolFailed, splitReplyQuote, splitRelay, turnEnds } from '../lib/turn.js';

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
  assert.equal(toolFailed('interrupted by user'), true);
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

test('splitRelay reads the head one thread puts on a prompt to another', () => {
  // The head application/control relayHead writes (control_test.go).
  const relayed = '[From thread @otter Planner (c_self)]\n[Your answer here does not reach that thread. If it needs a reply, send one to c_self with the conversation MCP tool send.]\n\nship it';
  assert.deepEqual(splitRelay(relayed), { label: '@otter', title: 'Planner', id: 'c_self', head: relayed.length - 7, body: 'ship it' });
  assert.deepEqual(splitRelay('[From thread Untitled thing (c_A1)]\n\nhi\nthere'), { label: '', title: 'Untitled thing', id: 'c_A1', head: 37, body: 'hi\nthere' });
  assert.deepEqual(splitRelay('[From another thread]\n\nhi'), { label: '', title: '', id: '', head: 23, body: 'hi' });
  assert.equal(splitRelay('please read [From thread @x y (c_1)]'), null);
  assert.equal(splitRelay('[In reply to: x]\nyes'), null);
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
