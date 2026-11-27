// The Skills page against the documented contract: stubbed fetch, the four
// labels, a level move, and the two failures. Nothing here is a live server.
import { calls, mockApi, sleep } from './env.mjs';
import { Ev, doc, installDOM } from './dom.mjs';
import { afterEach, beforeEach, test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { ApiError } from '../lib/api.js';
import { dismissToast, state } from '../lib/store.js';
import { Skills } from '../components/skills.js';
import {
  SKILL_GONE, UNTRACKED_REASON, proposalCount, skillLabel, skillMovable, skillMoveFailure, workspaceChoices,
} from '../lib/skills.js';

installDOM();

const skill = (over = {}) => ({
  name: 'release-notes',
  description: 'Use when writing release notes',
  source: 'nusashell',
  path: '/data/skills/release-notes/SKILL.md',
  digest: 'd1',
  level: 'agent',
  tracked: true,
  changed: false,
  pending: 0,
  pendingStale: 0,
  ...over,
});

const page = (items, extra = {}) => ({
  data: items,
  diagnostics: extra.diagnostics ?? [],
  pending: extra.pending ?? 0,
  pendingStale: extra.pendingStale ?? 0,
});

const until = async (ok, message, ms = 1000) => {
  const end = Date.now() + ms;
  while (!ok()) {
    if (Date.now() > end) assert.fail(message);
    await sleep(10);
  }
};

const button = (root, text) => [...root.querySelectorAll('button')].find((el) => el.textContent.trim() === text);
const submitMove = () => doc.querySelector('form').dispatchEvent(new Ev('submit', { bubbles: true }));
const rowOf = (name) => [...doc.querySelectorAll('li')].find((el) => el.textContent.includes(name));

let host;

beforeEach(() => {
  const proto = Object.getPrototypeOf(doc.createElement('div'));
  proto.showModal = function showModal() { this.open = true; };
  proto.close = function close() { this.open = false; };
  proto.onsubmit = null;
  doc.body.textContent = '';
  state.sidebarOpen = false;
  state.projects = {
    items: [
      { id: 'p1', name: 'NusaShell', folders: ['/w/proj'] },
      { id: 'p2', name: 'Mirror', folders: ['/w/proj'] },
    ],
    status: 'ready',
    error: null,
  };
  while (state.toasts[0]) dismissToast(state.toasts[0].id);
});

afterEach(() => {
  if (host) render(null, host);
  host = null;
  state.sidebarOpen = false;
  while (state.toasts[0]) dismissToast(state.toasts[0].id);
});

function mount() {
  host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${Skills} />`, host);
  return host;
}

test('the four labels, and a locked source stays locked', () => {
  assert.equal(skillLabel(skill()), 'Loop may propose');
  assert.equal(skillLabel(skill({ level: 'user' })), 'Only you');
  assert.equal(skillLabel(skill({ changed: true, level: 'user' })), 'Edited by hand');
  assert.equal(skillLabel(skill({ source: 'builtin', level: 'agent', tracked: true })), 'Locked');
  assert.equal(skillLabel(skill({ source: 'global', changed: true })), 'Locked');
  assert.equal(skillLabel(skill({ source: 'workspace', level: 'agent' })), 'Locked');
  assert.equal(skillLabel(skill({ source: 'other', level: 'agent' })), 'Loop may propose');
  assert.equal(skillMovable(skill()), true);
  assert.equal(skillMovable(skill({ changed: true, level: 'user' })), true);
  assert.equal(skillMovable(skill({ tracked: false })), false);
  assert.equal(skillMovable(skill({ source: 'workspace', level: 'agent', tracked: true })), false);
});

test('both move 404s are one sentence, and a 409 keeps the server\'s words', () => {
  const gone = new ApiError(404, 'not_found', 'the skill release-notes is no longer live — reload the page');
  const untracked = new ApiError(404, 'not_found', 'the skill release-notes has no ownership row — it is already the owner\'s');
  const stale = new ApiError(409, 'conflict', 'the skill\'s file changed since this page was opened — reload the page and try again');
  assert.equal(skillMoveFailure(gone), SKILL_GONE);
  assert.equal(skillMoveFailure(untracked), SKILL_GONE);
  assert.equal(skillMoveFailure(stale), stale.message);
  assert.equal(proposalCount(0, 0), '');
  assert.equal(proposalCount(1, 0), '1 proposal waiting');
  assert.equal(proposalCount(2, 1), '2 proposals waiting. 1 can no longer be accepted.');
});

test('two projects on one folder are one workspace choice', () => {
  const opts = workspaceChoices(state.projects.items);
  assert.deepEqual(opts, [
    { value: '', text: 'Built-in, learned, and global' },
    { value: '/w/proj', text: 'NusaShell, Mirror' },
  ]);
});

// settings.js pulls in the markdown sanitizer, whose vendor bundle degrades
// in this shim document. The nav list never renders markdown, so a no-op
// hook is enough to load it.
const { default: DOMPurify } = await import('/vendor/dompurify/purify.es.mjs');
if (!DOMPurify.addHook) DOMPurify.addHook = () => {};
const { SECTION_GROUPS } = await import('../components/settings.js');

test('Skills sits under Memory in the settings nav', () => {
  const caps = SECTION_GROUPS.find((group) => group.label === 'Capabilities').items.map((item) => item.id);
  assert.deepEqual(caps.slice(caps.indexOf('memory'), caps.indexOf('memory') + 2), ['memory', 'skills']);
});

test('the list shows each state, the path, the counts, and no proposal action', async () => {
  mockApi({
    'GET /api/skills': page([
      skill({ pending: 2, pendingStale: 1 }),
      skill({ name: 'hand-edit', description: 'Edited locally', path: '/data/skills/hand-edit/SKILL.md', level: 'user', changed: true }),
      skill({ name: 'coding-workflow', description: 'Shipped with the app', source: 'builtin', path: '/data/builtin-skills/coding-workflow/SKILL.md', level: 'user', tracked: false }),
      skill({ name: 'shared', description: 'In the home library', source: 'global', path: '/home/.agents/skills/shared/SKILL.md', level: 'user', tracked: false }),
      skill({ name: 'ws-skill', description: 'For this project', source: 'workspace', path: '/w/proj/.agents/skills/ws-skill/SKILL.md', level: 'agent', tracked: true }),
      skill({ name: 'loose-note', description: 'Written by hand', source: 'nusashell', path: '/data/skills/loose-note/SKILL.md', level: 'user', tracked: false }),
    ], {
      diagnostics: ['the name "gone" is still owned by the loop, but its file is gone'],
      pending: 3,
      pendingStale: 1,
    }),
  });
  mount();
  await until(() => doc.body.textContent.includes('hand-edit'), 'the list did not render');

  const body = doc.body.textContent;
  assert.match(body, /Loop may propose/);
  assert.match(body, /Edited by hand/);
  assert.match(body, /Only you/);
  assert.match(body, /Locked/);
  assert.match(body, /\/data\/skills\/hand-edit\/SKILL\.md/);
  const loose = rowOf('loose-note');
  assert.equal(loose.textContent.includes(UNTRACKED_REASON), true);
  assert.equal(button(loose, 'Change'), undefined);
  assert.match(body, /3 proposals waiting\. 1 can no longer be accepted\./);
  assert.match(body, /2 proposals waiting\. 1 can no longer be accepted\./);
  assert.match(body, /the name "gone" is still owned by the loop/);
  assert.equal(rowOf('coding-workflow').textContent.includes('Change'), false);
  assert.equal(rowOf('ws-skill').textContent.includes('Loop may propose'), false);
  assert.equal(rowOf('ws-skill').textContent.includes('Locked'), true);
  assert.ok(button(rowOf('hand-edit'), 'Change'));
  const controls = [...doc.querySelectorAll('button, a')].map((el) => (el.textContent || '') + (el.getAttribute('href') || ''));
  assert.equal(controls.some((text) => /accept|dismiss|reject|proposal/i.test(text)), false);
});

test('a level move sends the digest the page showed', async () => {
  let level = 'agent';
  mockApi({
    'GET /api/skills': () => page([skill({ level })]),
    'POST /api/skills/': (call) => { level = call.body.level; return [204, null]; },
  });
  mount();
  await until(() => button(doc.body, 'Change'), 'no change control');
  button(doc.body, 'Change').click();
  await until(() => button(doc.body, 'Save changes'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  assert.equal(button(doc.body, 'Save changes').getAttribute('type'), 'submit');
  assert.equal(button(doc.body, 'Save changes').getAttribute('form'), 'skill-move');
  submitMove();
  await until(() => calls.some((call) => call.method === 'POST'), 'no move was sent');

  const post = calls.find((call) => call.method === 'POST');
  assert.equal(post.url, '/api/skills/release-notes/level');
  assert.deepEqual(post.body, { level: 'user', digest: 'd1' });
  await until(() => state.toasts[0]?.message === 'Only you can change release-notes.', 'the move was not confirmed');
  await until(() => !button(doc.body, 'Save changes') && rowOf('release-notes').textContent.includes('Only you'), 'the list did not show the new level');
  assert.equal(rowOf('release-notes').textContent.includes('Loop may propose'), false);
});

test('a move uses the workspace the list was read with', async () => {
  mockApi({
    'GET /api/skills': page([skill()]),
    'POST /api/skills/': [204, null],
  });
  mount();
  await until(() => doc.querySelector('select'), 'no workspace choice');
  const select = doc.querySelector('select');
  assert.equal(select.getAttribute('id'), 'skills-workspace');
  select.value = '/w/proj';
  select.dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => calls.some((call) => call.url.includes('workspace=%2Fw%2Fproj')), 'the list was not read for the project');
  await until(() => button(doc.body, 'Change'), 'the project list did not render');
  button(doc.body, 'Change').click();
  await until(() => doc.querySelector('input[value="user"]'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  submitMove();
  await until(() => calls.some((call) => call.method === 'POST'), 'no move was sent');
  const post = calls.find((call) => call.method === 'POST');
  assert.equal(post.url, '/api/skills/release-notes/level?workspace=%2Fw%2Fproj');
  assert.deepEqual(post.body, { level: 'user', digest: 'd1' });
});

test('a 409 is the server\'s sentence, and the next decision sees the new digest', async () => {
  let digest = 'd1';
  mockApi({
    'GET /api/skills': () => page([skill({ digest })]),
    'POST /api/skills/': () => {
      digest = 'd2';
      return [409, { code: 'conflict', detail: 'the skill\'s file changed since this page was opened — reload the page and try again' }];
    },
  });
  mount();
  await until(() => button(doc.body, 'Change'), 'no change control');
  button(doc.body, 'Change').click();
  await until(() => doc.querySelector('input[value="user"]'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  submitMove();
  const detail = 'the skill\'s file changed since this page was opened — reload the page and try again';
  await until(() => doc.body.textContent.includes(detail), 'the 409 was not shown');
  await until(() => doc.querySelector('input[value="user"]').hasAttribute('disabled'), 'the radios stayed usable on the old digest');
  assert.equal(state.toasts[0]?.message, detail);
  assert.equal(calls.filter((call) => call.method === 'GET').length >= 2, true);
  assert.equal(doc.body.textContent.includes(SKILL_GONE), false);
});

test('a 404 from either cause is the one sentence, not the server\'s two', async () => {
  const details = [
    'the skill release-notes is no longer live — reload the page',
    'the skill release-notes has no ownership row — it is already the owner\'s',
  ];
  for (const detail of details) {
    if (host) render(null, host);
    mockApi({
      'GET /api/skills': page([skill()]),
      'POST /api/skills/': [404, { code: 'not_found', detail }],
    });
    mount();
    await until(() => button(doc.body, 'Change'), 'no change control');
    button(doc.body, 'Change').click();
    await until(() => doc.querySelector('input[value="user"]'), 'the dialog did not open');
    doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
    await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
    submitMove();
    await until(() => doc.body.textContent.includes(SKILL_GONE), `the 404 did not become the one sentence (${detail})`);
    assert.equal(doc.body.textContent.includes(detail), false, detail);
    assert.equal(state.toasts[0]?.message, SKILL_GONE);
  }
});

test('a slow list must not replace the workspace the owner picked', async () => {
  let release;
  mockApi({
    'GET /api/skills': (call) => {
      if (call.url.includes('workspace=')) return page([skill({ name: 'ws-skill', source: 'workspace', level: 'user', tracked: false })]);
      return new Promise((resolve) => { release = () => resolve(page([skill({ name: 'late-skill' })])); });
    },
  });
  mount();
  await until(() => calls.some((call) => call.method === 'GET' && !call.url.includes('workspace=')), 'the first read did not start');
  const select = doc.querySelector('select');
  select.value = '/w/proj';
  select.dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => doc.body.textContent.includes('ws-skill'), 'the project list did not win');
  release();
  await sleep(30);
  assert.equal(doc.body.textContent.includes('late-skill'), false);
  assert.match(doc.body.textContent, /ws-skill/);
});

test('an empty catalog and a failed read stay different', async () => {
  mockApi({ 'GET /api/skills': page([]) });
  mount();
  await until(() => doc.body.textContent.includes('No skills in this list'), 'the empty list did not say so');
  render(null, host);
  mockApi({ 'GET /api/skills': [500, { code: 'internal', detail: 'the catalog could not be read' }] });
  mount();
  await until(() => doc.body.textContent.includes('the catalog could not be read'), 'the failure was not shown');
  assert.equal(doc.body.textContent.includes('No skills in this list'), false);
});
