// Behavior tests for chronological tool-card placement. Live rounds and
// persisted conversations must render the same order: terminal stacks stay
// contiguous and standalone cards split them in the owning round.
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  renderConversation,
  renderToolCallCard,
  decorateToolCard,
  mountLiveRound,
  placeToolCard,
  swapToolCard,
  sealLiveNodeBeforeSteer,
} from '../js/views/agent/render.js';

function withDocument(fn) {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    return fn(dom.window.document);
  } finally {
    globalThis.document = previousDocument;
    dom.window.close();
  }
}

function terminal(id) {
  return { id, name: 'exec', args: { command: `echo ${id}` }, status: 'ok', output: id };
}

function standalone(id) {
  return { id, name: 'subagent', args: { prompt: `task ${id}`, agent_id: 'acp_dev' }, status: 'ok', output: `Subagent run acprun_${id} completed.` };
}

// describeRound lists the owning round's children as stack groups and
// standalone ids, e.g. ['[A]', 'B', '[C]'].
function describeRound(round) {
  return [...round.children]
    .filter((child) => child.classList.contains('agent-tool-stack') || child.dataset.standalone === 'true')
    .map((child) => {
      if (child.classList.contains('agent-tool-stack')) {
        return `[${[...child.children].map((node) => node.dataset.toolCallId).join(',')}]`;
      }
      return child.dataset.toolCallId;
    });
}

test('persisted tool rounds keep terminal and standalone cards in chronological order', () => {
  withDocument(() => {
    const messages = [{
      id: 'msg_1',
      role: 'assistant',
      created_at: new Date().toISOString(),
      steps: [
        { type: 'tool_calls', tool_calls: [terminal('A')] },
        { type: 'tool_calls', tool_calls: [standalone('B')] },
        { type: 'tool_calls', tool_calls: [terminal('C')] },
        { type: 'tool_calls', tool_calls: [standalone('D')] },
        { type: 'tool_calls', tool_calls: [terminal('E')] },
      ],
    }];
    const fragment = renderConversation(messages);
    const round = fragment.querySelector('.agent-round');
    assert.deepEqual(describeRound(round), ['[A]', 'B', '[C]', 'D', '[E]']);
  });
});

test('live tool rounds match the persisted order for the same cards', () => {
  withDocument((document) => {
    const bubble = document.createElement('div');
    bubble.className = 'agent-bubble';
    document.getElementById('thread').append(bubble);
    const refs = mountLiveRound(bubble, { messageId: 'msg_live' });
    let cursor = refs.strip;
    for (const card of [terminal('A'), standalone('B'), terminal('C'), standalone('D'), terminal('E')]) {
      cursor = placeToolCard(bubble, cursor, renderToolCallCard(card)) ?? cursor;
    }
    const round = bubble.querySelector('.agent-round');
    assert.deepEqual(describeRound(round), ['[A]', 'B', '[C]', 'D', '[E]']);
  });
});

test('a terminal placed after a standalone card starts a new stack after it', () => {
  withDocument((document) => {
    const bubble = document.createElement('div');
    const refs = mountLiveRound(bubble, {});
    const a = renderToolCallCard(terminal('A'));
    const b = renderToolCallCard(standalone('B'));
    const c = renderToolCallCard(terminal('C'));
    let cursor = placeToolCard(bubble, refs.strip, a) ?? refs.strip;
    cursor = placeToolCard(bubble, cursor, b) ?? cursor;
    assert.equal(cursor, refs.strip, 'standalone does not move the insertion stack');
    cursor = placeToolCard(bubble, cursor, c) ?? cursor;
    assert.notEqual(cursor, refs.strip);
    assert.equal(c.parentElement, cursor);
    assert.equal(cursor.previousElementSibling, b, 'new stack follows the standalone card');
    assert.equal(refs.strip.contains(c), false);
  });
});

test('replacing first, middle, and last terminals keeps positions and the following append in the current round', () => {
  for (const replaced of ['A', 'B', 'C']) {
    withDocument((document) => {
      const bubble = document.createElement('div');
      const historical = mountLiveRound(bubble, { messageId: 'msg_old' });
      let historicalCursor = historical.strip;
      const cards = { A: renderToolCallCard(terminal('A')), B: renderToolCallCard(terminal('B')), C: renderToolCallCard(terminal('C')) };
      for (const id of ['A', 'B', 'C']) historicalCursor = placeToolCard(bubble, historicalCursor, cards[id]) ?? historicalCursor;

      const current = mountLiveRound(bubble, { messageId: 'msg_now' });
      let cursor = current.strip;
      cursor = placeToolCard(bubble, cursor, renderToolCallCard(terminal('X'))) ?? cursor;

      const replacement = renderToolCallCard(standalone(`S${replaced}`));
      const result = swapToolCard(cards[replaced], replacement, bubble, cursor);
      assert.equal(result, cursor, 'historical replacement keeps the current insertion cursor');

      cursor = placeToolCard(bubble, cursor, renderToolCallCard(terminal('F'))) ?? cursor;

      const [oldRound, newRound] = bubble.querySelectorAll('.agent-round');
      assert.equal(replacement.closest('.agent-round'), oldRound, `replacement of ${replaced} stays in its owning round`);
      assert.ok(describeRound(oldRound).includes(`S${replaced}`), `old round lists S${replaced}: ${describeRound(oldRound).join(' ')}`);
      assert.deepEqual(describeRound(newRound), ['[X,F]'], 'the next append lands in the current round after X');
    });
  }
});

test('replacing a terminal with a standalone card splits its stack at the old position', () => {
  withDocument((document) => {
    const bubble = document.createElement('div');
    const refs = mountLiveRound(bubble, {});
    let cursor = refs.strip;
    const cards = [terminal('A'), terminal('B'), terminal('C')].map((c) => renderToolCallCard(c));
    for (const card of cards) cursor = placeToolCard(bubble, cursor, card) ?? cursor;
    const replacement = renderToolCallCard(standalone('S'));
    const result = swapToolCard(cards[1], replacement, bubble, cursor);
    const round = bubble.querySelector('.agent-round');
    assert.deepEqual(describeRound(round), ['[A]', 'S', '[C]']);
    assert.equal(result.previousElementSibling, replacement, 'cursor moves to the trailing group');
  });
});

test('terminal replacement keeps DETAILS open state', () => {
  withDocument((document) => {
    const bubble = document.createElement('div');
    const refs = mountLiveRound(bubble, {});
    const oldCard = document.createElement('details');
    oldCard.dataset.toolCallId = 'T1';
    oldCard.open = true;
    refs.strip.append(oldCard);
    refs.strip.hidden = false;
    const newCard = document.createElement('details');
    newCard.dataset.toolCallId = 'T1';
    swapToolCard(oldCard, newCard, bubble, refs.strip);
    assert.equal(newCard.open, true);
    assert.equal(refs.strip.contains(newCard), true);
  });
});

test('every rendered tool card carries the caller tool call id', () => {
  withDocument(() => {
    const card = renderToolCallCard(standalone('B'));
    assert.equal(card.dataset.toolCallId, 'B');
    const generic = renderToolCallCard(terminal('A'));
    assert.equal(generic.dataset.toolCallId, 'A');
    const direct = decorateToolCard(globalThis.document.createElement('div'), { id: 'Z', name: 'exec', args: {}, status: 'running' });
    assert.equal(direct.dataset.toolCallId, 'Z');
  });
});

test('steer sealing keeps a round whose first terminal stack is empty but whose standalone card and later stack remain', () => {
  withDocument((document) => {
    const node = document.createElement('div');
    node.className = 'agent-message assistant';
    const bubble = document.createElement('div');
    bubble.className = 'agent-bubble';
    node.append(bubble);
    const refs = mountLiveRound(bubble, {});
    let cursor = refs.strip;
    const standaloneCard = renderToolCallCard(standalone('S1'));
    cursor = placeToolCard(bubble, cursor, standaloneCard) ?? cursor;
    cursor = placeToolCard(bubble, cursor, renderToolCallCard(terminal('T1'))) ?? cursor;

    sealLiveNodeBeforeSteer(node);

    assert.equal(bubble.querySelectorAll('.agent-round').length, 1, 'round with a later card survives the steer seal');
    assert.equal(bubble.contains(standaloneCard), true);
  });
});

test('steer sealing removes a round that holds only the waiting placeholder', () => {
  withDocument((document) => {
    const node = document.createElement('div');
    node.className = 'agent-message assistant';
    const bubble = document.createElement('div');
    bubble.className = 'agent-bubble';
    node.append(bubble);
    mountLiveRound(bubble, {});
    sealLiveNodeBeforeSteer(node);
    assert.equal(bubble.querySelectorAll('.agent-round').length, 0);
  });
});

for (const replaced of ['A', 'B', 'C']) {
  test(`current cursor swap of ${replaced} followed by an append keeps the append at the end of the round`, () => {
    withDocument((document) => {
      const bubble = document.createElement('div');
      const refs = mountLiveRound(bubble, { messageId: 'msg_cursor' });
      let cursor = refs.strip;
      const cards = { A: renderToolCallCard(terminal('A')), B: renderToolCallCard(terminal('B')), C: renderToolCallCard(terminal('C')) };
      for (const id of ['A', 'B', 'C']) cursor = placeToolCard(bubble, cursor, cards[id]) ?? cursor;

      const replacement = renderToolCallCard(standalone(`S${replaced}`));
      cursor = swapToolCard(cards[replaced], replacement, bubble, cursor);
      const appended = renderToolCallCard(terminal('F'));
      cursor = placeToolCard(bubble, cursor, appended) ?? cursor;

      const round = bubble.querySelector('.agent-round');
      assert.equal(appended.parentElement, cursor, 'the append lands in the cursor stack');
      assert.equal(cursor.parentElement, round, 'the cursor stack belongs to the current round');
      assert.equal(cursor.hidden, false, 'a previously empty trailing cursor is shown for the append');
      const expected = {
        A: [`S${replaced}`, '[B,C,F]'],
        B: ['[A]', `S${replaced}`, '[C,F]'],
        C: ['[A,B]', `S${replaced}`, '[F]'],
      }[replaced];
      assert.deepEqual(describeRound(round), expected);
    });
  });
}
