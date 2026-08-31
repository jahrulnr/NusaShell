import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { dialog, toast } from '../js/ui.js';

function withDom(html, fn) {
  const dom = new JSDOM(html, { pretendToBeVisual: true });
  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  return Promise.resolve()
    .then(() => fn(dom.window))
    .finally(() => {
      globalThis.document = previousDocument;
      globalThis.window = previousWindow;
      dom.window.close();
    });
}

test('dialog dismisses on Escape and restores as cancelled', async () => {
  await withDom('<body></body>', async (window) => {
    const pending = dialog({
      title: 'Rename',
      message: 'Pick a name',
      fields: [{ name: 'title', label: 'Title', value: 'Room' }],
      actions: [
        { label: 'Cancel', value: null },
        { label: 'Save', value: 'ok', primary: true },
      ],
    });
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 1);
    const overlay = document.querySelector('.ui-dialog-overlay');
    assert.equal(overlay.querySelector('[role="dialog"]').getAttribute('aria-modal'), 'true');

    window.document.dispatchEvent(new window.KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    }));

    const result = await pending;
    assert.equal(result.value, null);
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 0);
  });
});

test('toast stays visible while hovered, then dismisses after remaining timeout', async () => {
  await withDom('<body><div id="toast-container" class="toast-container"></div></body>', async (window) => {
    const originalSetTimeout = window.setTimeout;
    const originalClearTimeout = window.clearTimeout;
    const originalRaf = window.requestAnimationFrame;
    const timers = new Map();
    let nextId = 1;
    window.setTimeout = (fn, ms) => {
      const id = nextId++;
      timers.set(id, { fn, ms });
      return id;
    };
    window.clearTimeout = (id) => { timers.delete(id); };
    window.requestAnimationFrame = (fn) => { fn(0); return 1; };
    globalThis.setTimeout = window.setTimeout;
    globalThis.clearTimeout = window.clearTimeout;
    globalThis.requestAnimationFrame = window.requestAnimationFrame;

    try {
      toast('Saved', 'success', 1000);
      const node = document.querySelector('.toast');
      assert.ok(node);
      assert.equal(timers.size, 1);

      node.dispatchEvent(new window.Event('mouseenter', { bubbles: true }));
      assert.equal(timers.size, 0, 'hover pauses the dismiss timer');

      node.dispatchEvent(new window.Event('mouseleave', { bubbles: true }));
      assert.equal(timers.size, 1, 'leave restarts the dismiss timer');

      for (const { fn } of timers.values()) fn();
      node.dispatchEvent(new window.Event('transitionend'));
    } finally {
      window.setTimeout = originalSetTimeout;
      window.clearTimeout = originalClearTimeout;
      window.requestAnimationFrame = originalRaf;
      globalThis.setTimeout = originalSetTimeout;
      globalThis.clearTimeout = originalClearTimeout;
      globalThis.requestAnimationFrame = originalRaf;
    }
  });
});
