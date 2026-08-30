import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createAskCard, sealAskCard } from './js/views/ask-card.js';

function setupDom() {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previous = {
    document: globalThis.document,
    window: globalThis.window,
    HTMLElement: globalThis.HTMLElement,
  };
  globalThis.document = dom.window.document;
  globalThis.window = dom.window;
  globalThis.HTMLElement = dom.window.HTMLElement;
  return {
    dom,
    restore() {
      globalThis.document = previous.document;
      globalThis.window = previous.window;
      globalThis.HTMLElement = previous.HTMLElement;
      dom.window.close();
    },
  };
}

function mockFetch(captured) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, opts) => {
    captured.push({ url, body: JSON.parse(opts.body) });
    return { ok: true, json: async () => ({ ok: true, result: {} }) };
  };
  return () => { globalThis.fetch = original; };
}

const SINGLE = {
  question: 'Pick one',
  multi_select: false,
  allow_free_text: true,
  options: [
    { id: 'a', label: 'Option A', default: true },
    { id: 'b', label: 'Option B' },
  ],
};

const MULTI = {
  question: 'Pick any',
  multi_select: true,
  allow_free_text: true,
  options: [
    { id: 'a', label: 'Option A' },
    { id: 'b', label: 'Option B' },
  ],
};

test('ask question content renders Markdown in pending and sealed states', () => {
  const { dom, restore } = setupDom();
  try {
    const card = createAskCard('call_markdown', {
      question: 'Choose **the provider** and use `X-API-KEY`.',
      options: [{ id: 'a', label: '**Provider A**', description: 'Supports **free** search.' }],
      allow_free_text: false,
    }, { runId: 'run_1' });
    document.getElementById('thread').append(card);

    assert.ok(card.querySelector('.agent-ask-question strong'), 'question emphasis is rendered as HTML');
    assert.ok(card.querySelector('.agent-ask-question code'), 'question inline code is rendered as HTML');
    assert.ok(card.querySelector('.agent-ask-option-label strong'), 'option label emphasis is rendered as HTML');
    assert.ok(card.querySelector('.agent-ask-option-desc strong'), 'option description emphasis is rendered as HTML');

    sealAskCard(card, { answer: 'Use **Provider A** with `X-API-KEY`.' });
    assert.ok(card.querySelector('.agent-ask-answer strong'), 'sealed answer emphasis is rendered as HTML');
    assert.ok(card.querySelector('.agent-ask-answer code'), 'sealed answer inline code is rendered as HTML');
    assert.doesNotMatch(card.querySelector('.agent-ask-question').textContent, /\*\*/, 'Markdown markers are not visible in the question');
  } finally {
    restore();
  }
});

// Regression: in single-select, the default option is pre-selected. When the
// user types a custom answer, it must become the SOLE answer (via='text') —
// the pre-selected option must NOT leak into the submission, otherwise the
// model receives two answers ("Option A — custom text").
test('single-select: custom text is the sole answer, default option is not sent', async () => {
  const { dom, restore } = setupDom();
  const captured = [];
  const restoreFetch = mockFetch(captured);
  try {
    const card = createAskCard('call_1', SINGLE, { runId: 'run_1' });
    document.getElementById('thread').append(card);

    const optA = card.querySelector('[data-option-id="a"]');
    assert.equal(optA.classList.contains('is-selected'), true, 'default option pre-selected');

    card.querySelector('.agent-ask-custom-toggle').click();
    const ta = card.querySelector('.agent-ask-textarea');
    ta.value = 'do something else entirely';
    ta.dispatchEvent(new dom.window.Event('input'));

    assert.equal(optA.classList.contains('is-selected'), false, 'option deselected after typing custom text');

    card.querySelector('.agent-ask-send').click();
    await new Promise((r) => setTimeout(r, 30));

    assert.equal(captured.length, 1, 'one RPC submitted');
    const payload = captured[0].body.payload;
    assert.equal(payload.via, 'text', 'via is text for custom sole answer');
    assert.equal(payload.option_ids, undefined, 'no option_ids sent with custom text');
    assert.equal(payload.text, 'do something else entirely');
  } finally {
    restoreFetch();
    restore();
  }
});

// In single-select, clicking an option after typing custom text clears the
// custom text — the option replaces the custom answer so the visual state
// matches the submission.
test('single-select: clicking an option clears custom text', async () => {
  const { dom, restore } = setupDom();
  const captured = [];
  const restoreFetch = mockFetch(captured);
  try {
    const card = createAskCard('call_1', SINGLE, { runId: 'run_1' });
    document.getElementById('thread').append(card);

    const ta = card.querySelector('.agent-ask-textarea');
    card.querySelector('.agent-ask-custom-toggle').click();
    ta.value = 'a custom direction';
    ta.dispatchEvent(new dom.window.Event('input'));

    const optB = card.querySelector('[data-option-id="b"]');
    optB.click();

    assert.equal(ta.value, '', 'custom text cleared after picking an option');
    assert.equal(optB.classList.contains('is-selected'), true, 'option B selected');

    card.querySelector('.agent-ask-send').click();
    await new Promise((r) => setTimeout(r, 30));

    assert.equal(captured.length, 1, 'one RPC submitted');
    const payload = captured[0].body.payload;
    assert.equal(payload.via, 'option', 'via is option after picking one');
    assert.deepEqual(payload.option_ids, ['b']);
    assert.equal(payload.text, undefined, 'no text sent when option replaces custom');
  } finally {
    restoreFetch();
    restore();
  }
});

// Multi-select preserves the supplement behavior: options + custom note are
// both submitted (via='option' with option_ids and text).
test('multi-select: custom text supplements selected options', async () => {
  const { dom, restore } = setupDom();
  const captured = [];
  const restoreFetch = mockFetch(captured);
  try {
    const card = createAskCard('call_1', MULTI, { runId: 'run_1' });
    document.getElementById('thread').append(card);

    card.querySelector('[data-option-id="a"]').click();

    const ta = card.querySelector('.agent-ask-textarea');
    card.querySelector('.agent-ask-custom-toggle').click();
    ta.value = 'also consider C';
    ta.dispatchEvent(new dom.window.Event('input'));

    card.querySelector('.agent-ask-send').click();
    await new Promise((r) => setTimeout(r, 30));

    assert.equal(captured.length, 1, 'one RPC submitted');
    const payload = captured[0].body.payload;
    assert.equal(payload.via, 'option', 'multi-select keeps via=option');
    assert.deepEqual(payload.option_ids, ['a']);
    assert.equal(payload.text, 'also consider C', 'note supplements the options');
  } finally {
    restoreFetch();
    restore();
  }
});

// Single-select with only an option picked (no custom text) still submits
// via='option' with the option id.
test('single-select: option-only answer still submits via option', async () => {
  const { restore } = setupDom();
  const captured = [];
  const restoreFetch = mockFetch(captured);
  try {
    const card = createAskCard('call_1', SINGLE, { runId: 'run_1' });
    document.getElementById('thread').append(card);

    card.querySelector('.agent-ask-send').click();
    await new Promise((r) => setTimeout(r, 30));

    assert.equal(captured.length, 1, 'one RPC submitted');
    const payload = captured[0].body.payload;
    assert.equal(payload.via, 'option');
    assert.deepEqual(payload.option_ids, ['a'], 'default option submitted');
    assert.equal(payload.text, undefined);
  } finally {
    restoreFetch();
    restore();
  }
});
