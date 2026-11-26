import { calls, mockApi } from './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { api, ApiError, errorText, onUnauthenticated, setCsrf } from '../lib/api.js';

test('a failed call becomes an ApiError with the server\'s code and detail', async () => {
  mockApi({ 'GET /api/providers': [409, { code: 'conflict', detail: 'name taken', title: 'Conflict' }] });
  await assert.rejects(api.providers(), (err) => {
    assert.ok(err instanceof ApiError);
    assert.deepEqual([err.status, err.code, err.message], [409, 'conflict', 'name taken']);
    return true;
  });
});

test('a non-JSON failure still says something usable', async () => {
  mockApi({ 'GET /api/providers': () => [502, null] });
  await assert.rejects(api.providers(), (err) => err.code === 'http_502' && /HTTP 502/.test(err.message));
});

test('a network failure names the server, not the browser\'s error', async () => {
  mockApi({ 'GET /api/providers': () => { throw new TypeError('Failed to fetch'); } });
  await assert.rejects(api.providers(), (err) => err.status === 0 && err.code === 'network' && /nusashell serve/.test(err.message));
});

test('a 204 answers null', async () => {
  mockApi({ 'DELETE /api/agents/a': [204, null] });
  assert.equal(await api.deleteAgent('a'), null);
});

test('state-changing calls carry the CSRF token; reads do not', async () => {
  setCsrf('tok');
  mockApi({ 'GET /api/settings': {}, 'PATCH /api/settings': {} });
  await api.settings();
  await api.setSettings({ 'models.utility.model': 'm' });
  assert.equal(calls[0].headers['X-CSRF-Token'], undefined);
  assert.equal(calls[1].headers['X-CSRF-Token'], 'tok');
  setCsrf('');
});

test('settings are written as one {values} document', async () => {
  mockApi({ 'PATCH /api/settings': {} });
  await api.setSettings({ 'models.utility.provider': 'p', 'models.utility.model': 'm' });
  await api.setSetting('server.port', 1234);
  assert.deepEqual(calls[0].body, { values: { 'models.utility.provider': 'p', 'models.utility.model': 'm' } });
  assert.deepEqual(calls[1].body, { values: { 'server.port': 1234 } });
});

test('a 401 unauthenticated ends the session, except from the auth calls themselves', async () => {
  let ended = 0;
  onUnauthenticated(() => { ended++; });
  mockApi({
    'GET /api/providers': [401, { code: 'unauthenticated', detail: 'sign in' }],
    'GET /api/auth/': [401, { code: 'unauthenticated', detail: 'sign in' }],
  });
  await assert.rejects(api.providers());
  assert.equal(ended, 1);
  await assert.rejects(api.authStatus());
  assert.equal(ended, 1);
});

test('starting a thread\'s browser posts the address to that thread', async () => {
  mockApi({ 'POST /api/conversations/c_1/browser': { open: true, page: { url: 'https://example.com/' } } });
  const res = await api.startBrowser('c_1', 'example.com');
  assert.equal(calls[0].url, '/api/conversations/c_1/browser');
  assert.deepEqual(calls[0].body, { url: 'example.com' });
  assert.equal(res.open, true);
});

test('the skills page reads one workspace root; none asks for the owner\'s roots only', async () => {
  mockApi({ 'GET /api/skills': { data: [], diagnostics: [] } });
  await api.skills('/w/a');
  await api.skills();
  assert.equal(calls[0].url, '/api/skills?workspace=%2Fw%2Fa');
  assert.equal(calls[1].url, '/api/skills');
});

test('a level move names the skill, the level, the digest the page read, and the workspace', async () => {
  mockApi({ 'POST /api/skills/': [204, null] });
  assert.equal(await api.moveSkillLevel('deploy check', { workspace: '/w/a', level: 'agent', digest: 'sha256:abc' }), null);
  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].url, '/api/skills/deploy%20check/level?workspace=%2Fw%2Fa');
  assert.deepEqual(calls[0].body, { level: 'agent', digest: 'sha256:abc' });
});

test('a move on a file that changed since the page loaded rejects with its 409, not a silent success', async () => {
  // The caller branches on the status to read the digest again; swallowing
  // the error here would leave the row holding the old digest.
  mockApi({ 'POST /api/skills/': [409, { code: 'conflict', detail: 'the skill changed since this page loaded' }] });
  await assert.rejects(api.moveSkillLevel('deploy', { level: 'agent', digest: 'old' }), (err) =>
    err instanceof ApiError && err.status === 409 && /changed since/.test(err.message));
});

test('errorText reads an ApiError, any Error, or a bare value', () => {
  assert.equal(errorText(new ApiError(500, 'x', 'boom')), 'boom');
  assert.equal(errorText(new Error('plain')), 'plain');
  assert.equal(errorText('text'), 'text');
});
