// Settings → Models: the one consent switch behind read_media, and the body a
// model picker writes for its role. Both are promises the server enforces —
// the leaf is a JSON boolean, and a role without an effort has no such leaf —
// so the tests hold the page to the wire, not to its own copy of the rules.
import { calls, mockApi } from './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { errorText } from '../lib/api.js';
import { readingConsent, roleLeaves, rolePatch, setReadingConsent, setRoleEffort } from '../lib/settings.js';
import { ROLES, rolePicker } from '../components/models.js';

const role = (id) => ROLES.find((r) => r.id === id);

test('an unset consent is off, and only a JSON true turns it on', () => {
  assert.equal(readingConsent(undefined), false, 'the page before the document arrives');
  assert.equal(readingConsent({}), false);
  assert.equal(readingConsent({ media: {} }), false);
  assert.equal(readingConsent({ media: { reading_consent: false } }), false);
  assert.equal(readingConsent({ media: { reading_consent: 'true' } }), false, 'a string is not consent');
  assert.equal(readingConsent({ media: { reading_consent: true } }), true);
});

test('the switch writes a JSON boolean, never the word', async () => {
  mockApi({ 'PATCH /api/settings': (call) => [200, { media: { reading_consent: call.body.values['media.reading_consent'] } }] });

  const on = await setReadingConsent(true);
  const wrote = calls.find((c) => c.method === 'PATCH');
  assert.deepEqual(wrote.body, { values: { 'media.reading_consent': true } });
  assert.equal(typeof wrote.body.values['media.reading_consent'], 'boolean', 'the string "true" is refused with a 400');
  assert.equal(readingConsent(on), true, 'the page follows the document the write answered with');

  await setReadingConsent(false);
  assert.deepEqual(calls.at(-1).body, { values: { 'media.reading_consent': false } });
});

// The switch shows the document the server holds, so a refused write must not
// move it: the error says why, and the value under the switch stays the old
// one — the page never claims a consent the server did not store.
test('a refused write answers the server’s reason and leaves the switch as it was', async () => {
  mockApi({
    'PATCH /api/settings': [400, { code: 'invalid_argument', detail: 'media.reading_consent: value must be a non-null JSON literal' }],
  });
  const doc = { media: { reading_consent: false }, models: { vision: { provider: '', model: '' } } };
  const shown = JSON.stringify(doc);

  let shownError = null;
  try {
    const answered = await setReadingConsent(true);
    doc.media = answered.media;
  } catch (e) { shownError = errorText(e); }

  assert.match(shownError ?? '', /non-null JSON literal/);
  assert.equal(readingConsent(doc), false, 'the switch still reads the value the server kept');
  assert.equal(JSON.stringify(doc), shown, 'nothing was written locally either');
});

// A picker writes one atomic change, so every key in it must be a leaf the
// server knows. Vision holds a provider and a model and nothing else: an agent
// or an effort would be refused, and the model would be lost with it.
test('the vision picker writes a provider and a model, and nothing its role cannot hold', () => {
  const vision = role('vision');
  assert.deepEqual(vision.kinds, ['text'], 'a vision model is a text model that can look, not a kind of its own');
  assert.equal(vision.needs, 'image', 'and the image input is the capability it needs');
  assert.equal(vision.agents, undefined, 'only a thread can run on an agent');
  assert.equal(vision.effort, undefined, 'an effort is for the thread-opening roles');

  assert.deepEqual(rolePatch(vision, { agent: '', provider: 'openrouter', model: 'google/gemini-2.0-flash-001' }), {
    'models.vision.provider': 'openrouter',
    'models.vision.model': 'google/gemini-2.0-flash-001',
  });
  // clearing a role sends no effort leaf either — the other non-effort roles
  // would otherwise lose their own write to a 400
  assert.deepEqual(rolePatch(vision, { agent: '', provider: '', model: '' }), {
    'models.vision.provider': '',
    'models.vision.model': '',
  });
  assert.deepEqual(rolePatch(role('transcription'), { agent: '', provider: '', model: '' }), {
    'models.transcription.provider': '',
    'models.transcription.model': '',
  });
});

test('the roles that do take an effort still write one, and clear it when it cannot apply', () => {
  const threads = role('threads');
  assert.equal(threads.agents, true);
  assert.equal(threads.effort, true);
  // an agent carries its own effort, so the role's leaf is cleared with it
  assert.deepEqual(rolePatch(threads, { agent: 'claude', provider: '', model: '' }), {
    'models.threads.provider': '',
    'models.threads.model': '',
    'models.threads.agent': 'claude',
    'models.threads.effort': '',
  });
  // a model keeps whatever effort is stored; the effort select writes its own
  assert.deepEqual(rolePatch(threads, { agent: '', provider: 'p', model: 'm' }), {
    'models.threads.provider': 'p',
    'models.threads.model': 'm',
    'models.threads.agent': '',
  });
});

// One place decides which leaves a role may write, and both writers read it:
// the model picker and the effort select. An effort select offered for a role
// the server does not know would fail its own write, after the model change.
test('a role without an effort leaf writes none, from either select', async () => {
  mockApi({ 'PATCH /api/settings': () => [200, { models: { threads: {} } }] });
  calls.length = 0;

  const vision = role('vision');
  assert.deepEqual(roleLeaves(vision), ['models.vision.provider', 'models.vision.model']);
  assert.equal(setRoleEffort(vision, 'high'), null, 'no leaf, no request');
  assert.equal(calls.filter((c) => c.method === 'PATCH').length, 0);

  const threads = role('threads');
  assert.deepEqual(roleLeaves(threads), ['models.threads.provider', 'models.threads.model', 'models.threads.agent', 'models.threads.effort']);
  await setRoleEffort(threads, 'high');
  assert.deepEqual(calls.at(-1).body, { values: { 'models.threads.effort': 'high' } });
  await setRoleEffort(threads, '');
  assert.deepEqual(calls.at(-1).body, { values: { 'models.threads.effort': '' } }, 'provider default clears it');
});

// A vision model is a text model that accepts an image, so the picker asks the
// catalog for that capability instead of a model type the server does not have.
// A model that exists but does not fit stays visible when it is the assigned
// one: "(not found)" would say the catalog lost a model it still holds.
test('the vision picker offers text models that take an image, and nothing else', () => {
  const providers = [{ id: 'openrouter', name: 'OpenRouter', models: [
    { id: 'seer', name: 'Seer', modelType: 'text', inputModalities: ['text', 'image'] },
    { id: 'blind', name: 'Blind', modelType: 'text', inputModalities: ['text'] },
    { id: 'embed', name: 'Embed', modelType: 'embedding', inputModalities: ['text', 'image'] },
    { id: 'plain', name: 'Plain', inputModalities: ['text', 'image'] },
    { id: 'nothing', name: 'Nothing', inputModalities: [] },
  ] }];
  const options = rolePicker(role('vision'), providers, [], null);
  assert.deepEqual(options.flatMap((o) => o.options || [o]).map((o) => o.text), ['Off', 'Seer', 'Plain'],
    'a text model that can see, declared or untyped; never an embedding');
});

test('an assigned vision model the filter leaves out still reads as itself', () => {
  const providers = [{ id: 'openrouter', name: 'OpenRouter', models: [
    { id: 'blind', name: 'Blind', modelType: 'text', inputModalities: ['text'] },
    { id: 'seer', name: 'Seer', modelType: 'text', inputModalities: ['image'] },
  ] }];
  const assigned = rolePicker(role('vision'), providers, [], { provider: 'openrouter', model: 'blind' });
  const shown = assigned.at(-1);
  assert.equal(shown.text, 'OpenRouter · Blind', 'its own name, no invented (not found)');
  assert.ok(!assigned.slice(0, -1).some((o) => o.options?.some((x) => x.text === 'Blind')), 'and the filter still leaves it out of the list');

  // the model this role names but the catalog no longer has says so
  const gone = rolePicker(role('vision'), providers, [], { provider: 'openrouter', model: 'retired' }).at(-1);
  assert.equal(gone.text, 'openrouter · retired (not found)');
});
