import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { bindShellShortcuts, isTypingTarget, searchFieldForView } from '../js/shell-shortcuts.js';

function withDom(html, fn) {
  const dom = new JSDOM(html, { pretendToBeVisual: true, url: 'https://nusashell.local/#home' });
  const previous = {
    document: globalThis.document,
    window: globalThis.window,
    HTMLElement: globalThis.HTMLElement,
  };
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.HTMLElement = dom.window.HTMLElement;
  try {
    return fn(dom.window);
  } finally {
    globalThis.document = previous.document;
    globalThis.window = previous.window;
    globalThis.HTMLElement = previous.HTMLElement;
    dom.window.close();
  }
}

const fixture = `
  <section class="view home-view active" data-view="home"><input id="search-input"></section>
  <section class="view agent-view" data-view="agent">
    <input id="conversation-search">
    <textarea id="composer-input"></textarea>
    <button id="new-conversation-btn" type="button">+</button>
  </section>
  <section class="view skills-view" data-view="skills"><input id="skills-search"></section>
  <section class="view learning-view" data-view="learning"><input id="learning-search-input"></section>
`;

test('searchFieldForView maps each workspace to its search box', () => {
  assert.equal(searchFieldForView('home'), 'search-input');
  assert.equal(searchFieldForView('agent'), 'conversation-search');
  assert.equal(searchFieldForView('skills'), 'skills-search');
  assert.equal(searchFieldForView('learning'), 'learning-search-input');
});

test('slash and Ctrl/Cmd+K focus the active view search unless the user is typing', () => {
  withDom(fixture, (window) => {
    bindShellShortcuts();
    const homeSearch = document.getElementById('search-input');
    document.dispatchEvent(new window.KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true }));
    assert.equal(document.activeElement, homeSearch);

    document.querySelector('[data-view="home"]').classList.remove('active');
    document.querySelector('[data-view="agent"]').classList.add('active');
    document.dispatchEvent(new window.KeyboardEvent('keydown', {
      key: 'k', ctrlKey: true, bubbles: true, cancelable: true,
    }));
    assert.equal(document.activeElement, document.getElementById('conversation-search'));

    const composer = document.getElementById('composer-input');
    composer.focus();
    document.dispatchEvent(new window.KeyboardEvent('keydown', {
      key: '/', bubbles: true, cancelable: true,
    }));
    assert.equal(document.activeElement, composer);
  });
});

test('Ctrl/Cmd+N on the agent view creates a conversation and ignores typing targets', () => {
  withDom(fixture, (window) => {
    let created = 0;
    bindShellShortcuts({ onNewConversation: () => { created += 1; } });
    document.querySelector('[data-view="home"]').classList.remove('active');
    document.querySelector('[data-view="agent"]').classList.add('active');

    document.dispatchEvent(new window.KeyboardEvent('keydown', {
      key: 'n', metaKey: true, bubbles: true, cancelable: true,
    }));
    assert.equal(created, 1);

    document.getElementById('composer-input').focus();
    document.dispatchEvent(new window.KeyboardEvent('keydown', {
      key: 'n', metaKey: true, bubbles: true, cancelable: true,
    }));
    assert.equal(created, 1, 'does not steal Ctrl+N from the composer');
  });
});

test('isTypingTarget treats inputs, textareas, and Slim Select as typing surfaces', () => {
  withDom(`<input id="a"><div class="ss-main" id="b"></div><p id="c">x</p>`, () => {
    assert.equal(isTypingTarget(document.getElementById('a')), true);
    assert.equal(isTypingTarget(document.getElementById('b')), true);
    assert.equal(isTypingTarget(document.getElementById('c')), false);
  });
});
