import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createSteerQueue, createToolJobStrip } from '../page/conversation/strips.js';

const root = new URL('../', import.meta.url);

// One pending steer reads as a stacked upper section of the composer frame
// (Codex-style shared card), never as an independent floating overlay: no gap,
// no own border box, shadow, or blur. The row keeps icon + queued text +
// cancel; an empty queue leaves the composer untouched.

const BARE = `<div class="composer-wrap"><form id="message-form"><textarea id="message-input"></textarea></form></div>`;

test('the queued steer is a compact row inside the composer frame, not a floating sibling', async () => {
  const dom = new JSDOM(await readFile(new URL('index.html', root), 'utf8'));
  try {
    const { document } = dom.window;
    const form = document.querySelector('#message-form');
    const wrap = document.querySelector('.composer-wrap');
    const queue = document.querySelector('#agent-steer-queue');
    const toolStrip = document.querySelector('#tool-job-strip');

    assert.ok(form && wrap && queue && toolStrip);
    assert.equal(queue.parentElement, form, 'the steer row mounts inside the composer frame');
    assert.equal(queue, form.firstElementChild, 'the row stacks above every input section');
    assert.equal(wrap.contains(queue), true, 'the row still rides the PiP-moved composer stack');
    assert.equal(toolStrip.parentElement, wrap, 'the tool strip keeps its floating card above the composer');
    assert.equal(queue.getAttribute('aria-live'), 'polite');
    assert.equal(queue.getAttribute('aria-label'), 'Pending steer');

    assert.ok(queue.querySelector('.agent-steer-queue-icon'), 'the row leads with a steer icon');
    assert.ok(queue.querySelector('#agent-steer-queue-text'));
    const cancel = queue.querySelector('#agent-steer-cancel');
    assert.ok(cancel);
    assert.equal(cancel.type, 'button', 'cancel must never submit the composer');
    assert.ok(cancel.getAttribute('aria-label'));

    // One compact row: the old collapse head and state pill are gone.
    assert.equal(queue.querySelector('#agent-steer-queue-toggle'), null);
    assert.equal(queue.querySelector('#agent-steer-queue-title'), null);
    assert.equal(queue.querySelector('#agent-steer-queue-state'), null);
    assert.equal(queue.querySelector('.agent-todo-strip-head'), null);
  } finally {
    dom.window.close();
  }
});

test('the steer factory mounts the same single row inside the composer form', () => {
  const dom = new JSDOM(BARE);
  try {
    const { document } = dom.window;
    const form = document.querySelector('#message-form');
    let cancels = 0;
    const queue = createSteerQueue({ doc: document, onCancel: () => { cancels += 1; } });

    queue.show('steer the turn this way', 'steer-1');
    const section = document.querySelector('#agent-steer-queue');
    assert.ok(section, 'the factory creates the strip when static markup is absent');
    assert.equal(section.parentElement, form, 'dynamic mounts land inside the form like the static contract');
    assert.equal(section, form.firstElementChild);
    assert.equal(section.hidden, false);
    assert.equal(section.dataset.steerId, 'steer-1');

    const text = section.querySelector('#agent-steer-queue-text');
    assert.equal(text.textContent, 'steer the turn this way');
    assert.equal(text.title, 'steer the turn this way', 'truncated text keeps a hover tooltip');

    const cancel = section.querySelector('#agent-steer-cancel');
    assert.equal(cancel.hidden, false);
    assert.equal(cancel.disabled, false);
    cancel.click();
    assert.equal(cancels, 1, 'cancel still routes to onCancel');

    queue.clear();
    assert.equal(section.hidden, true, 'an empty queue restores the plain composer');
    assert.equal(cancel.hidden, true);
  } finally {
    dom.window.close();
  }
});

test('the tool job strip stays a floating sibling above the composer while the queue sits inside it', () => {
  const dom = new JSDOM(BARE);
  let jobs = null;
  try {
    const { document } = dom.window;
    const wrap = document.querySelector('.composer-wrap');
    const form = document.querySelector('#message-form');

    createSteerQueue({ doc: document }).show('queued text', 's1');
    jobs = createToolJobStrip({ doc: document });
    jobs.sync([{ toolCallId: 't1', name: 'exec', label: 'exec', startedAt: Date.now() }]);

    const strip = document.querySelector('#tool-job-strip');
    assert.ok(strip);
    assert.equal(strip.parentElement, wrap, 'the tool strip mounts at wrap level, not inside the form');
    assert.equal(strip.nextElementSibling, form, 'insert order keeps the tool strip directly above the composer');
    assert.equal(document.querySelector('#agent-steer-queue').parentElement, form);
  } finally {
    jobs?.clear(); // stops the elapsed-time interval so the test process can exit
    dom.window.close();
  }
});

test('strips.css integrates the steer row into the composer frame — no overlay chrome', async () => {
  const css = await readFile(new URL('page/conversation/strips.css', root), 'utf8');

  assert.doesNotMatch(css, /\.composer-wrap\s*>\s*\.agent-steer-queue/,
    'the steer queue no longer floats above the composer as its own card');

  const rules = css.match(/[^{}]*\.agent-steer-queue[^{]*\{[^}]*\}/g) || [];
  assert.ok(rules.length, 'strips.css owns the integrated steer styling');
  for (const rule of rules) {
    assert.doesNotMatch(rule, /(?:box-shadow|backdrop-filter)\s*:\s*(?!none\b)\S/,
      `no independent drop shadow or blur on the stacked section: ${rule}`);
  }

  assert.match(css, /\.agent-steer-queue[^{]*\{[^}]*border-bottom:\s*1px solid/s,
    'a hairline divider separates the queued row from the input');
  assert.match(css, /\.agent-steer-queue-text[^}]*\{[^}]*white-space:\s*nowrap/s);
  assert.match(css, /\.agent-steer-queue-text[^}]*\{[^}]*text-overflow:\s*ellipsis/s,
    'long queued text truncates instead of overflowing the row');
});
