import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderTodoItem } from '../js/views/agent/render.js';

function withDOM(fn) {
  const dom = new JSDOM('<!DOCTYPE html><body></body>');
  const prevDoc = globalThis.document;
  const prevEl = globalThis.el;
  globalThis.document = dom.window.document;
  try {
    return fn();
  } finally {
    globalThis.document = prevDoc;
  }
}

test('renderTodoItem renders a pending item with empty box glyph', () => {
  withDOM(() => {
    const node = renderTodoItem({ id: '1', content: 'Write tests', status: 'pending' });
    assert.match(node.className, /is-pending/);
    const glyph = node.querySelector('.agent-todo-item-glyph');
    assert.equal(glyph.textContent, '☐');
    assert.match(glyph.className, /is-pending/);
    assert.equal(node.querySelector('.agent-todo-item-content').textContent, 'Write tests');
    assert.ok(node.querySelector('.agent-todo-item-delete'), 'delete button exists');
  });
});

test('renderTodoItem renders an in_progress item with half-circle glyph', () => {
  withDOM(() => {
    const node = renderTodoItem({ id: '2', content: 'Running', status: 'in_progress' });
    assert.match(node.className, /is-in_progress/);
    const glyph = node.querySelector('.agent-todo-item-glyph');
    assert.equal(glyph.textContent, '◐');
    assert.match(glyph.className, /is-in-progress/);
  });
});

test('renderTodoItem renders a completed item with checkmark glyph', () => {
  withDOM(() => {
    const node = renderTodoItem({ id: '3', content: 'Done', status: 'completed' });
    assert.match(node.className, /is-completed/);
    const glyph = node.querySelector('.agent-todo-item-glyph');
    assert.equal(glyph.textContent, '✓');
    assert.match(glyph.className, /is-completed/);
  });
});

test('renderTodoItem defaults to pending when status is missing', () => {
  withDOM(() => {
    const node = renderTodoItem({ id: '4', content: 'No status' });
    assert.match(node.className, /is-pending/);
    assert.equal(node.querySelector('.agent-todo-item-glyph').textContent, '☐');
  });
});

test('renderTodoItem delete button calls onDelete with item id and button', () => {
  withDOM(() => {
    let calledWith = null;
    const node = renderTodoItem({ id: '5', content: 'Delete me', status: 'pending' }, (id, btn) => {
      calledWith = { id, btn };
    });
    const btn = node.querySelector('.agent-todo-item-delete');
    btn.click();
    assert.equal(calledWith.id, '5');
    assert.equal(calledWith.btn, btn);
  });
});

test('renderTodoItem delete button has accessible label with content', () => {
  withDOM(() => {
    const node = renderTodoItem({ id: '6', content: 'Fix bug #42', status: 'pending' });
    const btn = node.querySelector('.agent-todo-item-delete');
    assert.equal(btn.getAttribute('aria-label'), 'Remove task: Fix bug #42');
  });
});

test('renderTodoItem handles empty content gracefully', () => {
  withDOM(() => {
    const node = renderTodoItem({ id: '7', status: 'pending' });
    assert.equal(node.querySelector('.agent-todo-item-content').textContent, '');
  });
});

test('renderTodoItem creates a fresh DOM node each call (no shared state)', () => {
  withDOM(() => {
    const a = renderTodoItem({ id: '8', content: 'A', status: 'pending' });
    const b = renderTodoItem({ id: '8', content: 'A', status: 'pending' });
    assert.notEqual(a, b);
    assert.notEqual(a.querySelector('.agent-todo-item-delete'), b.querySelector('.agent-todo-item-delete'));
  });
});
