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
  ALREADY_DECIDED, DISMISS_CONFIRM_UNKNOWN, DISMISS_GONE, DISMISS_NOT_RECORDED, DISMISS_NOT_YET, DISMISS_READS, DISMISS_SUPERSEDED,
  LEFTOVER_ROW, MOVE_UNKNOWN, QUEUE_EMPTY, QUEUE_LOAD_FAILED, QUEUE_PAUSED, QUEUE_STILL_READING, SKILL_GONE, UNTRACKED_REASON,
  dismissClosedHelper, dismissDraft, dismissFailure, dismissSheetCopy, dismissedNotice, proposalCount, proposalNoLongerApplies, queueReadPhase,
  resetDismissDrafts, resetSkillsRoute, skillLabel, skillMovable, skillMoveFailure, skillsRouteState, workspaceChoices,
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
  changedCause: '',
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
  resetSkillsRoute();
  resetDismissDrafts();
  state.route = { view: 'home' };
});

afterEach(() => {
  if (host) render(null, host);
  host = null;
  state.sidebarOpen = false;
  while (state.toasts[0]) dismissToast(state.toasts[0].id);
  resetSkillsRoute();
  resetDismissDrafts();
  state.route = { view: 'home' };
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
  assert.equal(skillLabel(skill({ changed: true, level: 'user', changedCause: "the skill's file changed since its row was written" })), "the skill's file changed since its row was written");
  assert.equal(skillLabel(skill({ changed: true, level: 'user', changedCause: '' })), 'Only you');
  assert.equal(skillLabel(skill({ changed: false, level: 'user', changedCause: '' })), 'Only you');
  assert.equal(skillLabel(skill({ source: 'builtin', level: 'agent', tracked: true })), 'Locked');
  assert.equal(skillLabel(skill({ source: 'global', changed: true })), 'Locked');
  assert.equal(skillLabel(skill({ source: 'global', changed: true, changedCause: "the skill's file changed since its row was written" })), "the skill's file changed since its row was written");
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
const { SECTION_GROUPS, SettingsNav, SettingsView } = await import('../components/settings.js');

test('Skills sits under Memory in the settings nav', () => {
  const caps = SECTION_GROUPS.find((group) => group.label === 'Capabilities').items.map((item) => item.id);
  assert.deepEqual(caps.slice(caps.indexOf('memory'), caps.indexOf('memory') + 2), ['memory', 'skills']);
});

test('the list shows each state, the path, the counts, and no proposal action', async () => {
  mockApi({
    'GET /api/skills': page([
      skill({ pending: 2, pendingStale: 1 }),
      skill({ name: 'hand-edit', description: 'Edited locally', path: '/data/skills/hand-edit/SKILL.md', level: 'user', changed: true, changedCause: "the loop wrote this skill's file, but the decision was not recorded" }),
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
  assert.match(body, /the loop wrote this skill's file, but the decision was not recorded/);
  assert.equal(body.includes('Edited by hand'), false);
  assert.equal(body.includes('edited outside the loop'), false);
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

test('a changed row shows changedCause, and an empty cause adds no label', async () => {
  const wrote = "the loop wrote this skill's file, but the decision was not recorded";
  const drifted = "the skill's file changed since its row was written";
  mockApi({
    'GET /api/skills': page([
      skill({ name: 'quiet', level: 'user', changed: false, changedCause: '' }),
      skill({ name: 'blank', level: 'agent', changed: true, changedCause: '   ' }),
      skill({ name: 'noted', level: 'user', changed: true, changedCause: wrote }),
      skill({ name: 'drifted', level: 'agent', changed: true, changedCause: drifted }),
    ]),
  });
  mount();
  await until(() => doc.body.textContent.includes('drifted'), 'the list did not render');

  const badges = (name) => [...rowOf(name).querySelectorAll('span.rounded-md')].map((el) => el.textContent);
  assert.deepEqual(badges('quiet'), ['Only you']);
  assert.deepEqual(badges('blank'), ['Loop may propose']);
  assert.deepEqual(badges('noted'), [wrote]);
  assert.deepEqual(badges('drifted'), [drifted]);
  const body = doc.body.textContent;
  assert.equal(body.includes('Edited by hand'), false);
  assert.equal(body.includes('edited outside the loop'), false);

  button(rowOf('noted'), 'Change').click();
  await until(() => doc.querySelector('[id="skill-hand-back"]'), 'the cause did not open with the dialog');
  assert.equal(doc.querySelector('[id="skill-hand-back"]').textContent, wrote);
  assert.equal(doc.body.textContent.includes('Edited by hand'), false);
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

const skillsNavLink = () => [...doc.querySelectorAll('a')].some((a) => a.getAttribute('href') === '#/settings/skills');

function mountSettings() {
  state.route = { view: 'settings', section: 'skills' };
  host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<div><${SettingsNav} /><${SettingsView} /></div>`, host);
}

test('a server without the skills route shows neither the section nor its nav entry', async () => {
  mockApi({ 'GET /api/skills': [404, { code: 'not_found', detail: 'no such route' }] });
  mountSettings();
  await until(() => skillsRouteState() === 'missing', 'the probe did not settle on missing');
  await until(() => state.route.section === 'providers', 'the dead link did not move to the first section');
  assert.equal(skillsNavLink(), false);
  assert.equal(doc.body.textContent.includes('Who may change each skill'), false);
  assert.equal(doc.body.textContent.includes('Could not load skills'), false);
});

test('the nav has no Skills row until the route has answered', async () => {
  let answer;
  mockApi({ 'GET /api/skills': () => new Promise((resolve) => { answer = () => resolve(page([skill()])); }) });
  state.route = { view: 'settings', section: 'providers' };
  host = doc.createElement('div');
  doc.body.appendChild(host);
  render(html`<${SettingsNav} />`, host);
  await until(() => answer, 'the probe did not start');
  await sleep(30);
  assert.equal(skillsRouteState(), 'unknown');
  assert.equal(skillsNavLink(), false, 'a row showed before the route was known');
  answer();
  await until(() => skillsNavLink(), 'the row did not appear once the route answered');
});

test('a direct link to skills on a server without it lands on the first settings section', async () => {
  mockApi({ 'GET /api/skills': [404, { code: 'not_found', detail: 'no such route' }] });
  mountSettings();
  await until(() => state.route.section === 'providers', 'the dead link stayed on skills');
  assert.equal(location.hash, '#/settings/providers');
  assert.equal(doc.body.textContent.includes('Could not load skills'), false);
  assert.equal(doc.body.textContent.includes('Who may change each skill'), false);
});

test('a skills read that fails for another reason stays an error with Retry', async () => {
  mockApi({ 'GET /api/skills': [500, { code: 'internal', detail: 'the catalog could not be read' }] });
  mountSettings();
  await until(() => doc.body.textContent.includes('Could not load skills') && button(doc.body, 'Retry'), 'the failed read did not stay on the page');
  await until(() => skillsNavLink(), 'the nav did not offer Skills for a route that answered');
  assert.equal(doc.body.textContent.includes('the catalog could not be read'), true);
  assert.equal(doc.body.textContent.includes('Who may change each skill'), true);
});

test('a move with no answer is the unknown outcome, not a failure', () => {
  assert.equal(skillMoveFailure(new ApiError(0, 'network', 'Cannot reach the NusaShell server. Check the connection.')), MOVE_UNKNOWN);
  assert.equal(skillMoveFailure(new ApiError(0, 'timeout', 'The server took too long to answer.')), MOVE_UNKNOWN);
  assert.equal(skillMoveFailure(new ApiError(500, 'internal', 'the write failed')), 'the write failed');
  assert.equal(MOVE_UNKNOWN.includes('failed'), false);
  assert.equal(MOVE_UNKNOWN.includes('Cannot reach'), false);
});

test('a level move that gets no answer says the result is not known, and a reload shows the server\'s level', async () => {
  let level = 'agent';
  mockApi({
    'GET /api/skills': () => page([skill({ level })]),
    'POST /api/skills/': () => { throw new TypeError('Failed to fetch'); },
  });
  mount();
  await until(() => button(doc.body, 'Change'), 'no change control');
  button(doc.body, 'Change').click();
  await until(() => button(doc.body, 'Save changes'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  submitMove();
  const unknown = () => [...doc.querySelectorAll('p')].some((p) => p.textContent === MOVE_UNKNOWN);
  await until(unknown, 'the unknown outcome was not stated');
  assert.equal(doc.body.textContent.includes('Cannot reach'), false, 'the network sentence stayed');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false, 'an unknown outcome was shown as an error toast');
  assert.ok(button(doc.body, 'Reload list'), 'the reload is not offered beside the unknown outcome');

  // The server did write the change before the connection dropped. The
  // reload is how the owner sees that, not a second try at the move.
  level = 'user';
  const before = calls.filter((call) => call.method === 'GET').length;
  button(doc.body, 'Reload list').click();
  await until(() => !unknown(), 'the reload did not clear the unknown outcome');
  assert.ok(calls.filter((call) => call.method === 'GET').length > before, 'the reload did not read the list');
  assert.equal(button(doc.body, 'Reload list'), undefined);
  assert.match(rowOf('release-notes').textContent, /Only you/);
  assert.equal(button(doc.body, 'Save changes').hasAttribute('disabled'), true, 'the server already holds the level the owner chose');
});

test('a reload that gets no answer keeps the unknown outcome on the page', async () => {
  let reading = false;
  mockApi({
    'GET /api/skills': () => { if (reading) throw new TypeError('Failed to fetch'); return page([skill()]); },
    'POST /api/skills/': () => { throw new TypeError('Failed to fetch'); },
  });
  mount();
  await until(() => button(doc.body, 'Change'), 'no change control');
  button(doc.body, 'Change').click();
  await until(() => button(doc.body, 'Save changes'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  submitMove();
  const unknown = () => [...doc.querySelectorAll('p')].some((p) => p.textContent === MOVE_UNKNOWN);
  await until(unknown, 'the unknown outcome was not stated');

  reading = true;
  button(doc.body, 'Reload list').click();
  await until(() => doc.body.textContent.includes('Could not load skills'), 'the failed reload was not shown');
  assert.equal(unknown(), true, 'a reload that never answered settled the outcome');
  assert.ok(button(doc.body, 'Reload list'), 'the reload is gone after a reload that never answered');
});

test('a gateway 502 or 504 on a move is the unknown outcome too, unless our own problem document answers', () => {
  // The proxy may have passed the move on before it failed, so the server may have written.
  assert.equal(skillMoveFailure(new ApiError(502, 'http_502', 'Request failed (HTTP 502)')), MOVE_UNKNOWN);
  assert.equal(skillMoveFailure(new ApiError(504, 'http_504', 'Request failed (HTTP 504)')), MOVE_UNKNOWN);
  // A problem document carries its code: the server answered, and the move did not happen.
  assert.equal(skillMoveFailure(new ApiError(502, 'write_failed', 'the change was not saved', true)), 'the change was not saved');
  assert.equal(skillMoveFailure(new ApiError(504, 'write_timeout', 'the write did not finish', true)), 'the write did not finish');
});

test('a level move that a gateway answers with 504 says the result is not known, with the reload offered', async () => {
  mockApi({
    'GET /api/skills': () => page([skill()]),
    'POST /api/skills/': [504, null],
  });
  mount();
  await until(() => button(doc.body, 'Change'), 'no change control');
  button(doc.body, 'Change').click();
  await until(() => button(doc.body, 'Save changes'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  submitMove();
  const unknown = () => [...doc.querySelectorAll('p')].some((p) => p.textContent === MOVE_UNKNOWN);
  await until(unknown, 'the unknown outcome was not stated for a 504');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false, 'an unknown outcome was shown as an error toast');
  assert.ok(button(doc.body, 'Reload list'), 'the reload is not offered beside the unknown outcome');
});

test('a level move that our own problem document answers with 502 is a plain failure, and the list is read again', async () => {
  mockApi({
    'GET /api/skills': () => page([skill()]),
    'POST /api/skills/': [502, { code: 'write_failed', detail: 'the change was not saved' }],
  });
  mount();
  await until(() => button(doc.body, 'Change'), 'no change control');
  button(doc.body, 'Change').click();
  await until(() => button(doc.body, 'Save changes'), 'the dialog did not open');
  doc.querySelector('input[value="user"]').dispatchEvent(new Ev('change', { bubbles: true }));
  await until(() => !button(doc.body, 'Save changes').hasAttribute('disabled'), 'save stayed disabled');
  const before = calls.filter((call) => call.method === 'GET').length;
  submitMove();
  await until(() => state.toasts.some((t) => t.kind === 'error' && t.message === 'the change was not saved'), 'the server\'s definite answer was not shown as a failure');
  await until(() => calls.filter((call) => call.method === 'GET').length > before, 'the list was not read again after a refused move');
  assert.equal([...doc.querySelectorAll('p')].some((p) => p.textContent === MOVE_UNKNOWN), false, 'a definite answer was reported as unknown');
  assert.equal(button(doc.body, 'Reload list'), undefined, 'a reload was offered for a move the server refused');
});

const proposal = (over = {}) => ({
  id: 'sprop_open',
  op: 'create',
  kind: 'method',
  name: 'fresh-idea',
  description: 'Use when starting a fresh idea',
  target: '',
  tool: '',
  reason: 'the owner asked twice',
  evidence: { kind: 'repeat', quote: 'do it the same way', date: null },
  createdAt: '2026-11-26T12:00:00Z',
  open: true,
  staleCause: '',
  ...over,
});

const queueOf = () => doc.querySelector('[aria-label="Proposals"]');
const typeReason = (text) => {
  const field = doc.querySelector('textarea');
  field.value = text;
  field.dispatchEvent(new Ev('input', { bubbles: true }));
};
const submitDismiss = () => doc.querySelector('[id="skill-dismiss"]').dispatchEvent(new Ev('submit', { bubbles: true }));

test('a queue read is quiet, then a skeleton, then a note, then Retry, then it gives up', () => {
  assert.equal(queueReadPhase(0), 'quiet');
  assert.equal(queueReadPhase(199), 'quiet');
  assert.equal(queueReadPhase(200), 'skeleton');
  assert.equal(queueReadPhase(3999), 'skeleton');
  assert.equal(queueReadPhase(4000), 'noted');
  assert.equal(queueReadPhase(9999), 'noted');
  assert.equal(queueReadPhase(10000), 'retry');
  assert.equal(queueReadPhase(29999), 'retry');
  assert.equal(queueReadPhase(30000), 'gave-up');
});

test('a dismiss answer is the matrix sentence, and a dropped connection is not a failure', () => {
  assert.deepEqual(dismissFailure(new ApiError(0, 'network', 'Cannot reach the NusaShell server.')), { kind: 'unknown' });
  assert.deepEqual(dismissFailure(new ApiError(0, 'timeout', 'The server took too long to answer.')), { kind: 'unknown' });
  assert.deepEqual(dismissFailure(new ApiError(409, 'conflict', ALREADY_DECIDED)), { kind: 'decided' });
  assert.deepEqual(dismissFailure(new ApiError(404, 'not_found', 'no such skill proposal')), { kind: 'gone', message: DISMISS_GONE });
  assert.equal(dismissFailure(new ApiError(400, 'bad_request', 'a dismissal may not open with "superseded:", the reason the pass writes')).message, DISMISS_SUPERSEDED);
  assert.equal(dismissFailure(new ApiError(500, 'internal', 'the skill file was archived because it was still exactly as the loop wrote it, but the decision was not recorded')).message, DISMISS_NOT_RECORDED);
  assert.equal(dismissFailure(new ApiError(400, 'bad_request', 'a dismissal says why')).message, 'a dismissal says why');
  assert.equal(DISMISS_CONFIRM_UNKNOWN.includes('failed'), false);
  assert.equal(dismissedNotice('fresh-idea', true), 'Dismissed fresh-idea. The loop will not propose fresh-idea the same way again for 30 days.');
  assert.equal(dismissedNotice('fresh-idea', false), 'Dismissed fresh-idea.');
  const closed = proposal({ open: false, staleCause: 'the name is already a skill' });
  assert.equal(proposalNoLongerApplies(closed), 'No longer applies: the name is already a skill');
  assert.equal(dismissSheetCopy(proposal(), []).help, DISMISS_READS);
  assert.equal(dismissSheetCopy(closed, []).help, dismissClosedHelper('fresh-idea'));
  assert.equal(dismissSheetCopy(closed, []).help.includes(DISMISS_READS), false);
});

test('an empty queue says nothing is waiting, and it has no dismiss and no accept', async () => {
  mockApi({
    'GET /api/skills': page([]),
    'GET /api/skill-proposals': { data: [] },
  });
  mount();
  await until(() => queueOf()?.textContent.includes(QUEUE_EMPTY), 'the empty queue did not say so');
  assert.equal(queueOf().textContent.includes(DISMISS_READS), false, 'the open helper showed before a sheet, on an empty queue');
  assert.equal(button(doc.body, 'Dismiss'), undefined);
  assert.equal(button(doc.body, 'Accept'), undefined);
});

test('a slow queue shows its skeleton while the skills list is already on the page', async () => {
  mockApi({
    'GET /api/skills': page([skill()]),
    'GET /api/skill-proposals': () => new Promise(() => {}),
  });
  mount();
  await until(() => doc.body.textContent.includes('release-notes'), 'the catalog waited for the queue');
  assert.equal(queueOf().querySelector('.skeleton'), null, 'the queue drew a skeleton before its 200ms');
  assert.equal(queueOf().textContent.includes(DISMISS_READS), false);
  await sleep(260);
  assert.ok(queueOf().querySelector('.skeleton'), 'the skeleton did not appear once the wait was perceptible');
  assert.equal(queueOf().textContent.includes(QUEUE_STILL_READING), false, 'the four-second note showed with the skeleton');
  assert.equal(doc.body.textContent.includes('release-notes'), true);
});

test('a queue that fails to load says so, and Retry reads it again', async () => {
  let reads = 0;
  mockApi({
    'GET /api/skills': page([skill()]),
    'GET /api/skill-proposals': () => {
      reads += 1;
      return [500, { code: 'internal', detail: 'the queue could not be read' }];
    },
  });
  mount();
  await until(() => queueOf()?.textContent.includes(QUEUE_LOAD_FAILED), 'the failed queue was not shown');
  assert.equal(queueOf().textContent.includes('the queue could not be read'), false, 'the queue showed the server detail instead of its sentence');
  assert.equal(doc.body.textContent.includes('release-notes'), true, 'the catalog was dropped because the queue failed');
  assert.equal(button(doc.body, 'Accept'), undefined);
  button(queueOf(), 'Retry').click();
  await until(() => reads >= 2, 'Retry did not read the queue again');
});

test('a proposal that is not open shows its cause and can only be dismissed', async () => {
  mockApi({
    'GET /api/skills': page([skill({ name: 'fresh-idea', pending: 1 })], { pending: 2, pendingStale: 1 }),
    'GET /api/skill-proposals': {
      data: [
        proposal(),
        proposal({ id: 'sprop_taken', op: 'patch', name: 'release-notes', open: false, staleCause: 'the owner moved it to Only you since it was proposed.' }),
      ],
    },
  });
  mount();
  await until(() => queueOf()?.textContent.includes('No longer applies: the owner moved it to Only you since it was proposed.'), 'the closed proposal did not give its cause');
  assert.equal(button(doc.body, 'Accept'), undefined, 'accept was offered');
  assert.ok(button(rowOf('release-notes'), 'Dismiss'));
  assert.equal(rowOf('fresh-idea').textContent.includes(DISMISS_READS), false, 'the open helper was on the row');
  button(rowOf('release-notes'), 'Dismiss').click();
  await until(() => doc.querySelector('textarea'), 'the dismiss sheet did not open');
  const help = doc.querySelector('[id="dismiss-help"]').textContent;
  assert.equal(help, dismissClosedHelper('release-notes'));
  assert.equal(help.includes(DISMISS_READS), false);
  assert.match(doc.body.textContent, /Dismissing leaves the file as it is/);
  assert.equal(doc.querySelector('button[type="submit"]').hasAttribute('disabled'), true, 'Dismiss was active with no reason');
});

test('an open proposal needs a reason, and a reason the loop reserves is reworded', async () => {
  mockApi({
    'GET /api/skills': page([skill({ name: 'fresh-idea' })], { pending: 3, pendingStale: 0 }),
    'GET /api/skill-proposals': { data: [proposal()] },
    'POST /api/skill-proposals/': [400, { code: 'bad_request', detail: 'a dismissal may not open with "superseded:", the reason the pass writes for the proposals it retires' }],
  });
  mount();
  await until(() => doc.body.textContent.includes(QUEUE_PAUSED), 'three open proposals did not pause the queue');
  await until(() => rowOf('fresh-idea')?.textContent.includes(LEFTOVER_ROW), 'a create whose file is already on disk did not say so');
  button(rowOf('fresh-idea'), 'Dismiss').click();
  await until(() => doc.querySelector('[id="dismiss-help"]')?.textContent === DISMISS_READS, 'the open sheet did not say the loop reads the reason');
  assert.match(doc.body.textContent, /Dismissing moves fresh-idea's file to the archive only if it is still exactly what the loop wrote/);
  const submit = doc.querySelector('button[type="submit"]');
  assert.equal(submit.hasAttribute('disabled'), true);
  typeReason('   ');
  await sleep(20);
  assert.equal(doc.querySelector('button[type="submit"]').hasAttribute('disabled'), true, 'a blank reason was accepted');
  typeReason('superseded: the owner says so');
  await until(() => !doc.querySelector('button[type="submit"]').hasAttribute('disabled'), 'a written reason left Dismiss disabled');
  submitDismiss();
  await until(() => doc.body.textContent.includes(DISMISS_SUPERSEDED), 'the reserved prefix was not sent back to be reworded');
  assert.equal(doc.querySelector('textarea').value, 'superseded: the owner says so', 'the reason was dropped');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false);
});

test('a dismiss the server already recorded is finished, without an error', async () => {
  let rows = [proposal()];
  mockApi({
    'GET /api/skills': page([skill({ name: 'fresh-idea' })]),
    'GET /api/skill-proposals': () => ({ data: rows }),
    'POST /api/skill-proposals/': () => {
      rows = [];
      return [409, { code: 'conflict', detail: ALREADY_DECIDED }];
    },
  });
  mount();
  await until(() => rowOf('fresh-idea') && button(rowOf('fresh-idea'), 'Dismiss'), 'the proposal was not listed');
  button(rowOf('fresh-idea'), 'Dismiss').click();
  await until(() => doc.querySelector('textarea'), 'the sheet did not open');
  typeReason('already handled');
  await until(() => !doc.querySelector('button[type="submit"]').hasAttribute('disabled'), 'Dismiss stayed disabled');
  submitDismiss();
  const confirmed = 'Dismissed fresh-idea. The loop will not propose fresh-idea the same way again for 30 days.';
  await until(() => state.toasts[0]?.message === confirmed, 'a proposal already decided was not confirmed');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false, 'an already-decided proposal was reported as an error');
  assert.equal(doc.body.textContent.includes(ALREADY_DECIDED), false, 'the 409 sentence was shown as an error');
  await until(() => !queueOf().textContent.includes('Use when starting a fresh idea'), 'the decided proposal stayed in the queue');
});

test('a dismiss that gets no answer reloads, and a row that remains was not dismissed yet', async () => {
  let reads = 0;
  let release;
  mockApi({
    'GET /api/skills': page([skill({ name: 'fresh-idea' })]),
    'GET /api/skill-proposals': () => {
      reads += 1;
      if (reads > 1) return new Promise((resolve) => { release = () => resolve({ data: [proposal()] }); });
      return { data: [proposal()] };
    },
    'POST /api/skill-proposals/': () => { throw new TypeError('Failed to fetch'); },
  });
  mount();
  await until(() => rowOf('fresh-idea') && button(rowOf('fresh-idea'), 'Dismiss'), 'the proposal was not listed');
  button(rowOf('fresh-idea'), 'Dismiss').click();
  await until(() => doc.querySelector('textarea'), 'the sheet did not open');
  typeReason('try this');
  await until(() => !doc.querySelector('button[type="submit"]').hasAttribute('disabled'), 'Dismiss stayed disabled');
  submitDismiss();
  await until(() => [...doc.querySelectorAll('p')].some((p) => p.textContent === DISMISS_CONFIRM_UNKNOWN), 'the unknown outcome was not stated');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false, 'a dropped connection was shown as an error');
  assert.equal(doc.body.textContent.includes(MOVE_UNKNOWN), false, 'the level-move sentence was used for a dismiss');
  release();
  await until(() => [...doc.querySelectorAll('p')].some((p) => p.textContent === DISMISS_NOT_YET), 'a row that survived the reload was treated as dismissed');
  assert.equal(doc.querySelector('textarea').value, 'try this', 'the reason was cleared');
  assert.equal(state.toasts.some((t) => t.message?.startsWith('Dismissed')), false, 'a dismiss that did not land was confirmed');
});

test('a dismiss that gets no answer, and whose row is gone, was recorded', async () => {
  let reads = 0;
  let release;
  mockApi({
    'GET /api/skills': page([skill()]),
    'GET /api/skill-proposals': () => {
      reads += 1;
      if (reads > 1) return new Promise((resolve) => { release = () => resolve({ data: [] }); });
      return { data: [proposal()] };
    },
    'POST /api/skill-proposals/': () => { throw new TypeError('Failed to fetch'); },
  });
  mount();
  await until(() => rowOf('fresh-idea') && button(rowOf('fresh-idea'), 'Dismiss'), 'the proposal was not listed');
  button(rowOf('fresh-idea'), 'Dismiss').click();
  await until(() => doc.querySelector('textarea'), 'the sheet did not open');
  typeReason('try this');
  await until(() => !doc.querySelector('button[type="submit"]').hasAttribute('disabled'), 'Dismiss stayed disabled');
  submitDismiss();
  await until(() => release, 'the reload did not start');
  release();
  const confirmed = 'Dismissed fresh-idea. The loop will not propose fresh-idea the same way again for 30 days.';
  await until(() => state.toasts[0]?.message === confirmed, 'a row that left after the drop was not confirmed');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false);
});

test('a lapsed session keeps the dismiss reason and opens the sheet again, without sending it', async () => {
  mockApi({
    'GET /api/skills': page([]),
    'GET /api/skill-proposals': { data: [proposal()] },
    'POST /api/skill-proposals/': [401, { code: 'unauthenticated', detail: 'sign in' }],
  });
  mount();
  await until(() => rowOf('fresh-idea') && button(rowOf('fresh-idea'), 'Dismiss'), 'the proposal was not listed');
  button(rowOf('fresh-idea'), 'Dismiss').click();
  await until(() => doc.querySelector('textarea'), 'the sheet did not open');
  typeReason('keep this');
  await until(() => !doc.querySelector('button[type="submit"]').hasAttribute('disabled'), 'Dismiss stayed disabled');
  submitDismiss();
  await until(() => dismissDraft('sprop_open') === 'keep this', 'the reason was not kept');
  assert.equal(state.toasts.some((t) => t.kind === 'error'), false, 'a lapsed session was shown as a failed dismiss');
  assert.equal(calls.filter((call) => call.method === 'POST').length, 1);
  render(null, host);
  mount();
  // The stub textarea keeps the controlled value on the attribute. A browser
  // keeps it on the property. Either one is the reason the owner typed.
  await until(() => {
    const field = doc.querySelector('textarea');
    return field && (field.value === 'keep this' || field.getAttribute('value') === 'keep this');
  }, 'the sheet did not open again with the reason');
  assert.equal(calls.filter((call) => call.method === 'POST').length, 1, 'the page sent the dismissal on its own');
});
