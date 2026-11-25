// BL-7: the four catalog types no role consumes yet are annotated in the
// Providers type select instead of silently dead-ending the model. The note
// is honest annotation, not a gate — the value still saves verbatim — and
// the chat/role pickers keep filtering on modelType exactly as before.
import './env.mjs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MODEL_TYPES, UNUSED_MODEL_TYPES } from '../lib/providers.js';
import { ROLES, rolePicker } from '../components/models.js';

const NOTE = 'not used by a role yet';
const UNUSED = ['audio', 'decision', 'realtime', 'rerank'];

test('the four role-less types carry the note', () => {
  for (const v of UNUSED) {
    const o = MODEL_TYPES.find((x) => x.value === v);
    assert.ok(o, `${v} is still offered`);
    assert.match(o.text, new RegExp(NOTE), `${v} explains itself`);
    assert.ok(UNUSED_MODEL_TYPES.has(v), `${v} is in the unused set`);
  }
});

test('the other seven types carry no note', () => {
  const kept = MODEL_TYPES.filter((o) => !UNUSED_MODEL_TYPES.has(o.value));
  assert.equal(kept.length, MODEL_TYPES.length - UNUSED.length);
  for (const o of kept) assert.doesNotMatch(o.text, new RegExp(NOTE), `${o.value || 'undeclared'} stays plain`);
});

test('every offered value is the verbatim type id — no gate on save', () => {
  for (const v of [...UNUSED, 'text', 'image', 'embedding', 'speech', 'transcription', 'video', ''])
    assert.ok(MODEL_TYPES.some((o) => o.value === v), `${v || 'undeclared'} selects the raw value`);
});

test('the unused set is exactly the types no role lists as a kind', () => {
  const consumed = new Set(ROLES.flatMap((r) => r.kinds));
  for (const o of MODEL_TYPES) {
    if (o.value === '') continue;
    assert.equal(UNUSED_MODEL_TYPES.has(o.value), !consumed.has(o.value), `${o.value}: note matches "no role uses it"`);
  }
});

test('role pickers still filter by modelType as before', () => {
  const role = ROLES.find((r) => r.id === 'chat');
  const providers = [{ id: 'p1', name: 'P1', models: [
    { id: 'a', modelType: 'audio' }, { id: 't', modelType: 'text' }, { id: 'u' },
  ] }];
  const opts = rolePicker(role, providers).flatMap((o) => o.options ?? [o]).map((o) => o.value);
  assert.ok(!opts.includes('p:p1\na'), 'an audio model is still out of the chat picker');
  assert.ok(opts.includes('p:p1\nt') && opts.includes('p:p1\nu'), 'text and undeclared still appear');
});
