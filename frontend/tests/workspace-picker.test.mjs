// Workspace picker overlay: browse, error, cancel, and resolve flows.
// Stubs globalThis.fetch the same way route-picker.test.mjs does, since the
// picker speaks to the backend through rpc() (POST /rpc/agent/workspace/list-dirs).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { openWorkspacePicker } from '../js/views/agent/workspace-picker.js';
import { dismissOpenDialogs } from '../js/ui.js';

function makeDom() {
  const dom = new JSDOM(
    '<!doctype html><html><body><button id="workspace-trigger" type="button">pick</button></body></html>',
    { pretendToBeVisual: true },
  );
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  return dom;
}

function cleanup() {
  delete globalThis.window;
  delete globalThis.document;
}

const DIR_RESULT = {
  path: '/home/tuan/projects',
  parent: '/home/tuan',
  entries: [
    { name: 'alpha', path: '/home/tuan/projects/alpha' },
    { name: 'beta', path: '/home/tuan/projects/beta' },
  ],
  truncated: false,
};

const ok = (result) => ({ ok: true, json: async () => ({ ok: true, result }) });
const rpcError = (message, code = 'validation') => ({ ok: false, json: async () => ({ error: { message, code } }) });

function stubFetch(responses) {
  const original = globalThis.fetch;
  globalThis.fetch = async (url, opts) => {
    const body = JSON.parse(opts.body);
    const request = { url, method: body.method, payload: body.payload ?? {} };
    const respond = typeof responses === 'function'
      ? responses
      : (req) => responses[req.method] ?? ok({});
    return respond(request);
  };
  return () => { globalThis.fetch = original; };
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const open = (options) => openWorkspacePicker(options);

test('browses the initial path and resolves it on Use this folder', async () => {
  makeDom();
  const restore = stubFetch({ 'agent.workspace.list-dirs': ok(DIR_RESULT) });
  try {
    const picker = open({ initial: '/home/tuan/projects' });
    await tick();
    await tick();

    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 1);
    const dialogNode = document.querySelector('[role="dialog"]');
    assert.ok(dialogNode, 'dialog role present');
    assert.equal(dialogNode.getAttribute('aria-modal'), 'true');

    // Breadcrumbs: / home tuan projects
    assert.equal(document.querySelectorAll('.workspace-picker-crumb').length, 4);
    assert.equal(document.querySelector('.workspace-picker-path').value, '/home/tuan/projects');
    assert.equal(document.querySelectorAll('.workspace-picker-row').length, 2);

    const [, selectBtn] = document.querySelectorAll('.ui-dialog-actions button');
    selectBtn.click();
    assert.equal(await picker, '/home/tuan/projects');
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 0);
  } finally {
    restore();
    cleanup();
  }
});

test('Cancel resolves null and removes the overlay', async () => {
  makeDom();
  const restore = stubFetch({ 'agent.workspace.list-dirs': ok(DIR_RESULT) });
  try {
    const picker = open({ initial: '/home/tuan/projects' });
    await tick();
    await tick();

    const [cancelBtn] = document.querySelectorAll('.ui-dialog-actions button');
    cancelBtn.click();
    assert.equal(await picker, null);
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 0);
  } finally {
    restore();
    cleanup();
  }
});

test('Escape cancels the picker', async () => {
  makeDom();
  const restore = stubFetch({ 'agent.workspace.list-dirs': ok(DIR_RESULT) });
  try {
    const picker = open({ initial: '/home/tuan/projects' });
    await tick();
    await tick();

    document.dispatchEvent(new window.KeyboardEvent('keydown', {
      key: 'Escape',
      bubbles: true,
      cancelable: true,
    }));
    assert.equal(await picker, null);
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 0);
  } finally {
    restore();
    cleanup();
  }
});

test('router teardown (dismissOpenDialogs) cancels the picker', async () => {
  makeDom();
  const restore = stubFetch({ 'agent.workspace.list-dirs': ok(DIR_RESULT) });
  try {
    const picker = open({ initial: '/home/tuan/projects' });
    await tick();
    await tick();

    dismissOpenDialogs();
    assert.equal(await picker, null);
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 0);
  } finally {
    restore();
    cleanup();
  }
});

test('clicking a directory row loads that folder', async () => {
  makeDom();
  const seen = [];
  const restore = stubFetch(async ({ payload }) => {
    seen.push(payload);
    if (payload.path === '/home/tuan/projects') return ok(DIR_RESULT);
    return ok({ path: payload.path, parent: '/home/tuan/projects', entries: [] });
  });
  try {
    const picker = open({ initial: '/home/tuan/projects' });
    await tick();
    await tick();

    document.querySelectorAll('.workspace-picker-row')[0].click();
    await tick();
    await tick();

    assert.equal(seen[seen.length - 1].path, '/home/tuan/projects/alpha');
    assert.equal(document.querySelector('.workspace-picker-path').value, '/home/tuan/projects/alpha');
    assert.match(document.querySelector('.workspace-picker-status-text').textContent, /No subfolders/);

    document.querySelector('.ui-dialog-close').click();
    assert.equal(await picker, null);
  } finally {
    restore();
    cleanup();
  }
});

test('Go loads the typed path', async () => {
  makeDom();
  const seen = [];
  const restore = stubFetch(async ({ payload }) => {
    seen.push(payload);
    return ok({ path: payload.path || '/home/tuan', parent: '/', entries: [] });
  });
  try {
    const picker = open();
    await tick();
    await tick();

    const input = document.querySelector('.workspace-picker-path');
    input.value = '/srv/apps';
    document.querySelector('.workspace-picker-path-row button').click();
    await tick();
    await tick();

    assert.equal(seen[seen.length - 1].path, '/srv/apps');

    document.querySelector('.ui-dialog-close').click();
    assert.equal(await picker, null);
  } finally {
    restore();
    cleanup();
  }
});

test('Use this folder with an unsaved typed path loads it before resolving', async () => {
  makeDom();
  const restore = stubFetch(async ({ payload }) => ok({
    path: payload.path || '/home/tuan',
    parent: '/',
    entries: [],
  }));
  try {
    const picker = open({ initial: '/home/tuan' });
    await tick();
    await tick();

    const input = document.querySelector('.workspace-picker-path');
    input.value = '/data/repos';
    const [, selectBtn] = document.querySelectorAll('.ui-dialog-actions button');
    selectBtn.click();

    assert.equal(await picker, '/data/repos');
  } finally {
    restore();
    cleanup();
  }
});

test('listing errors stay inline with Select disabled and the popup open', async () => {
  makeDom();
  const restore = stubFetch({ 'agent.workspace.list-dirs': rpcError('Permission denied') });
  try {
    const picker = open();
    await tick();
    await tick();

    const status = document.querySelector('.workspace-picker-status-text.is-error');
    assert.ok(status, 'inline error status present');
    assert.match(status.textContent, /Permission denied/);

    const [, selectBtn] = document.querySelectorAll('.ui-dialog-actions button');
    assert.equal(selectBtn.disabled, true, 'Select disabled on error');
    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 1, 'popup stays open on error');

    document.querySelector('.ui-dialog-close').click();
    assert.equal(await picker, null);
  } finally {
    restore();
    cleanup();
  }
});

test('a failed typed-path Select keeps the popup open for correction', async () => {
  makeDom();
  const restore = stubFetch(async ({ payload }) => {
    if (payload.path === '/nope') return rpcError('No such directory');
    return ok({ path: payload.path || '/home/tuan', parent: '/', entries: [] });
  });
  try {
    const picker = open();
    await tick();
    await tick();

    const input = document.querySelector('.workspace-picker-path');
    input.value = '/nope';
    const [, selectBtn] = document.querySelectorAll('.ui-dialog-actions button');
    selectBtn.click();
    await tick();
    await tick();

    assert.equal(document.querySelectorAll('.ui-dialog-overlay').length, 1, 'popup stays open after failed select');
    assert.match(document.querySelector('.workspace-picker-status-text.is-error').textContent, /No such directory/);

    document.querySelector('.ui-dialog-close').click();
    assert.equal(await picker, null);
  } finally {
    restore();
    cleanup();
  }
});
