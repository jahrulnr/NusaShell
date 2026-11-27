// The ACP subagent card is the parent run, grouped by keys. A call with no
// keys stays an ordinary tool row.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { newTurn, applyFrame, historyItems, liveSegments, applyLiveEnd, placeSubagents, subagentView, memberLine } from '../lib/turn.js';

const frame = (type, payload, seq) => ({ type, payload, seq });
const turn = () => newTurn({ turnId: 't', conversationId: 'c', kind: 'send' });

const runInput = JSON.stringify({ title: 'Look around', task: 'Read the tree', profile: 'fast' });

function call(id, over) {
  return { id, kind: 'tool', callId: id, name: over.name || 'run_subagent', input: over.input || '', output: over.output || '', status: over.status || 'settled', keys: over.keys || null, parent: over.parent || '', title: over.title || '', toolKind: over.toolKind || '', turnEnd: over.turnEnd || '', ownerStop: !!over.ownerStop };
}

test('a later tool_call replaces keys and does not add a row', () => {
  const t = turn();
  applyFrame(t, frame('turn.tool_call', { callId: 'p', name: 'run_subagent', keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.background': 'true' } }, 0));
  applyFrame(t, frame('turn.tool_call', { callId: 'p', name: 'run_subagent', keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.success': 'true', 'acp.subagent.summary': 'short' } }, 1));
  assert.equal(t.segments.filter((s) => s.kind === 'tool').length, 1);
  assert.equal(t.segments[0].keys['acp.subagent.success'], 'true');
  assert.equal(t.segments[0].keys['acp.subagent.summary'], 'short');
});

test('a call with no keys stays an ordinary tool row', () => {
  const rows = [call('p', { input: runInput })];
  assert.equal(placeSubagents(rows), rows);
  assert.equal(rows[0].kind, 'tool');
});

test('members join their card in arrival order, and a nested run stays its own card', () => {
  const outer = call('p', { input: runInput, keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.background': 'true' }, output: 'Background subagent started.' });
  const early = call('m1', { name: 'read_file', parent: 'run-1', toolKind: 'read', title: 'notes', input: '{"path":"a.go"}\nmore' });
  const inner = call('n', { input: JSON.stringify({ title: 'Deeper', task: 'Look again' }), keys: { 'acp.subagent.id': 'run-2', 'acp.subagent.parent': 'run-1', 'acp.subagent.depth': '2' } });
  const late = call('m2', { name: 'exec', parent: 'run-2', toolKind: 'execute', title: 'ls', input: 'ls' });
  const own = call('x', { name: 'exec', input: '{"command":"pwd"}' });
  const placed = placeSubagents([early, outer, own, inner, late], {});
  assert.deepEqual(placed.map((it) => it.kind), ['subagent', 'tool', 'subagent']);
  assert.equal(placed[0].members.length, 1);
  assert.equal(placed[0].members[0].callId, 'm1');
  assert.equal(placed[2].members[0].callId, 'm2');
  assert.equal(placed[2].members.includes(inner), false);
  const view = subagentView(placed[0]);
  assert.equal(view.status, 'Running');
  assert.equal(view.badges.includes('Background'), true);
  assert.equal(view.badges.includes('2'), false);
  assert.deepEqual(view.members, [['read', 'notes', '{"path":"a.go"}']]);
  assert.equal(view.output, 'Background subagent started.');
  assert.equal(view.summary, '');
});

test('a member whose card never appears is delegated only after the turn ends', () => {
  const member = call('m', { name: 'read_file', parent: 'missing', title: 'notes', turnEnd: '' });
  const live = placeSubagents([member], {});
  assert.equal(live[0].kind, 'tool');
  const done = placeSubagents([call('m', { name: 'read_file', parent: 'missing', title: 'notes', turnEnd: 'finished' })], {});
  assert.equal(done[0].kind, 'delegated');
  assert.equal(done[0].name, 'read_file');
  assert.equal(done[0].title, 'notes');
});

test('success true is Done and the summary is not the output', () => {
  const item = call('p', {
    input: runInput,
    output: 'the report itself',
    turnEnd: 'finished',
    keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.success': 'true', 'acp.subagent.summary': 'one line', 'acp.subagent.model': 'devin' },
  });
  const [card] = placeSubagents([item]);
  const view = subagentView(card);
  assert.equal(view.status, 'Done');
  assert.equal(view.summary, 'one line');
  assert.equal(view.output, 'the report itself');
  assert.equal(view.summary === view.output, false);
  assert.equal(view.badges.includes('devin'), true);
  assert.equal(view.badges.includes('fast'), true);
  assert.equal(JSON.stringify(view).includes('Failed'), false);
});

test('a finished turn without success true is Incomplete, including success false', () => {
  const finished = subagentView(placeSubagents([call('p', { input: runInput, turnEnd: 'finished', keys: { 'acp.subagent.id': 'run-1' }, status: 'failed' })])[0]);
  assert.equal(finished.status, 'Incomplete');
  assert.equal(finished.note, 'No report was recorded for this subagent.');
  const refused = subagentView(placeSubagents([call('p', { input: runInput, turnEnd: 'stopped', keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.success': 'false' } })])[0]);
  assert.equal(refused.status, 'Incomplete');
  assert.equal(refused.note, 'No report was recorded for this subagent.');
  assert.equal(JSON.stringify(refused).includes('Reported unsuccessful'), false);
});

test('a stopped turn names the end, and an owner stop uses the owner sentence', () => {
  const stopped = subagentView(placeSubagents([call('p', { input: runInput, turnEnd: 'stopped', keys: { 'acp.subagent.id': 'run-1' } })])[0]);
  assert.equal(stopped.note, 'The turn ended before this subagent reported.');
  const owner = subagentView(placeSubagents([call('p', { input: runInput, turnEnd: 'stopped', ownerStop: true, keys: { 'acp.subagent.id': 'run-1' } })])[0]);
  assert.equal(owner.note, 'You stopped the turn before this subagent reported.');
});

test('a background run stays Running after its tool row closes, and a blind room has no live view', () => {
  const row = call('p', { input: runInput, status: 'success', output: 'Background subagent started.', keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.background': 'true' } });
  assert.equal(subagentView(placeSubagents([row], {})[0]).status, 'Running');
  assert.equal(subagentView(placeSubagents([row], { blind: true })[0]).status, 'Running — no live view');
});

test('an empty title and task are omitted, and a missing member part is omitted', () => {
  const view = subagentView(placeSubagents([call('p', { input: '{}', keys: { 'acp.subagent.id': 'run-1' } })], {})[0]);
  assert.equal(view.title, '');
  assert.equal(view.task, '');
  assert.equal(view.members.length, 0);
  assert.deepEqual(memberLine({ toolKind: '', title: 'Only', input: '' }), ['Only']);
});

test('a reloaded history row carries the same card', () => {
  const items = historyItems([{
    id: 'a', role: 'assistant', finishReason: 'stop', content: 'done',
    toolCalls: [{
      id: 'p', name: 'run_subagent', input: runInput, output: 'the report', status: 'completed',
      keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.success': 'true', 'acp.subagent.summary': 'short' },
    }, {
      id: 'm', name: 'read_file', kind: 'read', title: 'notes', input: 'a.go', status: 'completed',
      keys: { 'acp.subagent.parent': 'run-1' },
    }],
  }]);
  const card = placeSubagents(items).find((it) => it.kind === 'subagent');
  const view = subagentView(card);
  assert.equal(view.status, 'Done');
  assert.equal(view.summary, 'short');
  assert.deepEqual(view.members, [['read', 'notes', 'a.go']]);
});

test('a key frame updates the history row the screen keeps', () => {
  const hist = historyItems([{
    id: 'a', role: 'assistant', content: '',
    toolCalls: [{ id: 'p', name: 'run_subagent', input: runInput, output: 'Background subagent started.', status: 'completed', keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.background': 'true' } }],
  }]);
  const live = {
    segments: [{ kind: 'tool', callId: 'p', name: 'run_subagent', keys: { 'acp.subagent.id': 'run-1', 'acp.subagent.success': 'true', 'acp.subagent.summary': 'landed' }, parent: '' }],
  };
  assert.equal(liveSegments(live, hist).length, 0);
  const view = subagentView(placeSubagents(hist)[0]);
  assert.equal(view.status, 'Done');
  assert.equal(view.summary, 'landed');
  live.terminal = { type: 'turn.failed' };
  live.canceling = true;
  applyLiveEnd(hist, live);
  const stopped = subagentView(placeSubagents([hist.find((it) => it.callId === 'p')])[0]);
  assert.equal(stopped.status, 'Done', 'a success key still beats the stopped turn');
});
