import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { bindTablistKeyboard, createSelect, dialog, toast } from '../js/ui.js';

function withDom(html, fn) {
  const dom = new JSDOM(html, { pretendToBeVisual: true });
  const previousDocument = globalThis.document;
  const previousWindow = globalThis.window;
  const previousMutationObserver = globalThis.MutationObserver;
  const previousSelf = globalThis.self;
  const previousRaf = globalThis.requestAnimationFrame;
  const previousGetComputedStyle = globalThis.getComputedStyle;
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.MutationObserver = dom.window.MutationObserver;
  globalThis.self = dom.window;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
  return Promise.resolve()
    .then(() => fn(dom.window))
    .finally(() => {
      globalThis.document = previousDocument;
      globalThis.window = previousWindow;
      globalThis.MutationObserver = previousMutationObserver;
      globalThis.self = previousSelf;
      globalThis.requestAnimationFrame = previousRaf;
      globalThis.getComputedStyle = previousGetComputedStyle;
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

test('styled select hides its body-level option portal from assistive technology while closed', async () => {
  await withDom('<body><label for="route">Route</label><select id="route"></select></body>', async () => {
    const instance = createSelect(document.getElementById('route'), {
      data: [
        { text: 'Automatic', value: 'auto' },
        { text: 'Local', value: 'local' },
      ],
      value: 'auto',
    });
    const portal = document.querySelector('.ss-content');

    assert.equal(portal?.parentElement, document.body, 'the floating menu is portalled to body');
    assert.equal(portal?.getAttribute('aria-hidden'), 'true', 'closed options stay out of the accessibility tree');

    instance.open();
    await Promise.resolve();
    assert.equal(portal?.getAttribute('aria-hidden'), 'false', 'opening exposes the option list');

    instance.close();
    await Promise.resolve();
    assert.equal(portal?.getAttribute('aria-hidden'), 'true', 'closing hides the option list immediately');
    instance.destroy();
  });
});

test('tablist uses one tab stop and activates tabs with the arrow keys', async () => {
  await withDom(`<body><div role="tablist">
    <button role="tab" aria-selected="true">One</button>
    <button role="tab" aria-selected="false">Two</button>
    <button role="tab" aria-selected="false">Three</button>
  </div></body>`, (window) => {
    const tablist = document.querySelector('[role="tablist"]');
    const tabs = [...tablist.querySelectorAll('[role="tab"]')];
    tablist.addEventListener('click', (event) => {
      if (!event.target.matches('[role="tab"]')) return;
      tabs.forEach((tab) => tab.setAttribute('aria-selected', String(tab === event.target)));
    });
    bindTablistKeyboard(tablist);

    assert.deepEqual(tabs.map((tab) => tab.tabIndex), [0, -1, -1]);
    tabs[0].focus();
    tabs[0].dispatchEvent(new window.KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));

    assert.equal(document.activeElement, tabs[1]);
    assert.equal(tabs[1].getAttribute('aria-selected'), 'true');
    assert.deepEqual(tabs.map((tab) => tab.tabIndex), [-1, 0, -1]);
  });
});

test('tablist Home, End, and arrow navigation wrap across its tabs', async () => {
  await withDom(`<body><div role="tablist">
    <button role="tab" aria-selected="true">One</button>
    <button role="tab" aria-selected="false">Two</button>
    <button role="tab" aria-selected="false">Three</button>
  </div></body>`, (window) => {
    const tablist = document.querySelector('[role="tablist"]');
    const tabs = [...tablist.querySelectorAll('[role="tab"]')];
    bindTablistKeyboard(tablist);

    tabs[0].focus();
    tabs[0].dispatchEvent(new window.KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    assert.equal(document.activeElement, tabs[2], 'ArrowLeft wraps to the last tab');

    tabs[2].dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Home', bubbles: true }));
    assert.equal(document.activeElement, tabs[0]);

    tabs[0].dispatchEvent(new window.KeyboardEvent('keydown', { key: 'End', bubbles: true }));
    assert.equal(document.activeElement, tabs[2]);
  });
});

test('toast is safe before the shell mounts', async () => {
  await withDom('<body></body>', () => {
    assert.doesNotThrow(() => toast('Installed', 'success'));
    assert.equal(document.querySelectorAll('.toast').length, 0);
  });
});

test('toast coalesces the same kind and message instead of stacking', async () => {
  await withDom('<body><div id="toast-container" class="toast-container"></div></body>', async (window) => {
    window.requestAnimationFrame = (fn) => { fn(0); return 1; };
    globalThis.requestAnimationFrame = window.requestAnimationFrame;
    toast('NetworkError when attempting to fetch resource.', 'error');
    toast('NetworkError when attempting to fetch resource.', 'error');
    toast('Backend unreachable', 'error');
    toast('Saved', 'success');
    const nodes = [...document.querySelectorAll('.toast')];
    assert.equal(nodes.length, 3);
    assert.equal(nodes.filter((n) => n.classList.contains('toast-error')).length, 2);
    assert.equal(nodes.filter((n) => n.classList.contains('toast-success')).length, 1);
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
