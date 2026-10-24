import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createLazySelect } from '../js/ui.js';

// The model catalog carries ~2,900 entries; the lazy select must keep the
// SlimSelect option DOM capped (windowed), grow it on scroll, and still let
// search/selection reach every option in the full dataset.

const MARKUP = '<select id="pick"></select>';

function installDOMGlobals(window) {
  const names = [
    'window', 'document', 'MutationObserver', 'requestAnimationFrame',
    'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement',
    'HTMLSelectElement', 'localStorage', 'navigator', 'getComputedStyle', 'self',
  ];
  const previous = new Map();
  for (const name of names) {
    previous.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
    Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
  }
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

function harness({ count = 120, pageSize = 20, groups = 4, onChange } = {}) {
  const dom = new JSDOM(MARKUP, { url: 'http://localhost/', pretendToBeVisual: true });
  const restore = installDOMGlobals(dom.window);
  const select = dom.window.document.querySelector('#pick');
  const data = Array.from({ length: groups }, (_, g) => ({
    label: `provider-${g}`,
    options: Array.from({ length: Math.ceil(count / groups) }, (_, i) => ({
      text: `Model ${g}-${i}`,
      value: `provider-${g}:model-${g}-${i}`,
    })),
  }));
  const api = createLazySelect(select, { data, pageSize, onChange });
  const cleanup = () => {
    api.destroy();
    restore();
    dom.window.close();
  };
  return { dom, select, api, restore: cleanup };
}

function renderedOptions(dom) {
  return [...dom.window.document.querySelectorAll('.ss-option')];
}

test('the option DOM stays capped at the page size for large datasets', () => {
  const env = harness();
  try {
    assert.ok(renderedOptions(env.dom).length <= 20,
      `expected <= 20 mounted options, got ${renderedOptions(env.dom).length}`);
    // The full dataset remains reachable through the search surface.
    assert.equal(env.api.queryOptions('model 0-0').length >= 1, true);
    assert.equal(env.api.queryOptions('model 3').length >= 25, true,
      'search must cover options beyond the rendered window');
  } finally {
    env.restore();
  }
});

test('scrolling to the list end appends the next page', () => {
  const env = harness();
  try {
    const list = env.dom.window.document.querySelector('.ss-list');
    assert.ok(list, 'slim list rendered');
    const before = renderedOptions(env.dom).length;
    list.dispatchEvent(new env.dom.window.Event('scroll'));
    const after = renderedOptions(env.dom).length;
    assert.ok(after > before, `expected growth on scroll (${before} -> ${after})`);
    assert.ok(after <= 40, `second page caps at 40, got ${after}`);
    // Scrolling past the end of the dataset stops growing.
    for (let i = 0; i < 20; i += 1) list.dispatchEvent(new env.dom.window.Event('scroll'));
    assert.ok(renderedOptions(env.dom).length <= 120);
  } finally {
    env.restore();
  }
});

test('setSelected injects options outside the current window', () => {
  const env = harness({ pageSize: 10 });
  try {
    env.api.setSelected(['provider-3:model-3-29']);
    assert.deepEqual(env.api.getSelected(), ['provider-3:model-3-29']);
    const rendered = renderedOptions(env.dom).some(
      (option) => option.textContent.includes('Model 3-29'),
    );
    assert.ok(rendered, 'the selected option is mounted even though it sat beyond page 1');
  } finally {
    env.restore();
  }
});

test('search filters the whole dataset and resets on clear', async () => {
  const env = harness({ pageSize: 10 });
  try {
    const input = env.dom.window.document.querySelector('.ss-search input');
    assert.ok(input, 'search input rendered');
    // Debounced SlimSelect search: set the value, then wait past the 100ms
    // debounce before asserting the filtered window.
    input.value = 'model 2-7';
    input.dispatchEvent(new env.dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 160));
    const values = renderedOptions(env.dom).map((option) => option.textContent);
    assert.ok(values.some((text) => text.includes('Model 2-7')),
      `filtered window must contain the match, got: ${values.join(', ')}`);
    assert.ok(renderedOptions(env.dom).length <= 20, 'matches stay capped');

    input.value = '';
    input.dispatchEvent(new env.dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 160));
    const first = renderedOptions(env.dom)[0]?.textContent || '';
    assert.ok(first.includes('Model 0-0'),
      `clearing search must restore the first page, got: ${first}`);
  } finally {
    env.restore();
  }
});

test('window growth and search clear never steal the selection', async () => {
  const changes = [];
  const env = harness({ pageSize: 10, onChange: (value) => changes.push(value) });
  try {
    env.api.setSelected(['provider-3:model-3-29']);
    changes.length = 0;

    // Growing the window rebuilds the option DOM — SlimSelect's own setData
    // would fall back to the first option and fire afterChange here.
    const list = env.dom.window.document.querySelector('.ss-list');
    for (let i = 0; i < 4; i += 1) list.dispatchEvent(new env.dom.window.Event('scroll'));
    assert.deepEqual(env.api.getSelected(), ['provider-3:model-3-29'],
      'scroll growth must not reselect the first option');
    assert.deepEqual(changes, [], 'window growth must not emit a selection change');

    const input = env.dom.window.document.querySelector('.ss-search input');
    input.value = 'model 0';
    input.dispatchEvent(new env.dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 160));
    assert.deepEqual(env.api.getSelected(), ['provider-3:model-3-29'],
      'searching must keep the active selection');
    assert.deepEqual(changes, []);

    input.value = '';
    input.dispatchEvent(new env.dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 160));
    assert.deepEqual(env.api.getSelected(), ['provider-3:model-3-29'],
      'clearing search must not steal the selection');
    assert.deepEqual(changes, []);
  } finally {
    env.restore();
  }
});

test('reopening after a search restores the unfiltered first page', async () => {
  const env = harness({ pageSize: 10 });
  try {
    env.api.open();
    const input = env.dom.window.document.querySelector('.ss-search input');
    input.value = 'model 3-';
    input.dispatchEvent(new env.dom.window.Event('input', { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 160));
    env.api.close();
    // SlimSelect fires afterClose only after its 200ms closeTimeout.
    await new Promise((resolve) => setTimeout(resolve, 260));
    env.api.open();
    const values = renderedOptions(env.dom).map((option) => option.textContent);
    assert.ok(values.some((text) => text.includes('Model 0-0')),
      `reopen must show the first page again, got: ${values.join(', ')}`);
    assert.ok(!values.every((text) => text.includes('Model 3-')),
      'reopen must not keep rendering the stale search subset');
  } finally {
    env.restore();
  }
});
