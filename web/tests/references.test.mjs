import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  referenceTrigger, reconcileReferences, liveReferences, referencesForSend,
  referencesFromStored, referencesToDraft, referenceLabel, slug,
} from '../lib/references.js';

test('the trigger is the @ or / word the caret sits in', () => {
  assert.deepEqual(referenceTrigger('see @pro', 8), { start: 4, end: 8, prefix: '@', query: 'pro' });
  assert.deepEqual(referenceTrigger('run /he', 7), { start: 4, end: 7, prefix: '/', query: 'he' });
  // The rest of the word after the caret is part of the replaced span.
  assert.equal(referenceTrigger('see @project', 7).end, 12);
  assert.equal(referenceTrigger('mail a@b', 8), null);
  assert.equal(referenceTrigger('plain', 5), null);
});

test('a selection moves with text typed before it and goes when it is touched', () => {
  const ref = { kind: 'file', label: './a.go', start: 4, end: 10 };
  const before = 'see ./a.go now';
  assert.deepEqual(reconcileReferences(before, 'so see ./a.go now', [ref], 0, 0), [{ ...ref, start: 7, end: 13 }]);
  assert.deepEqual(reconcileReferences(before, 'see ./b.go now', [ref], 6, 7), []);
  // An edit that does not match the reported range is found by diffing.
  assert.deepEqual(reconcileReferences(before, 'ok see ./a.go now', [ref], 10, 10), [{ ...ref, start: 7, end: 13 }]);
  // With no range at all the whole text counts as replaced.
  assert.deepEqual(reconcileReferences(before, 'ok see ./a.go now', [ref]), []);
});

test('selections that no longer match the draft are dropped', () => {
  const refs = [{ label: '@x', start: 0, end: 2 }, { label: '@y', start: 3, end: 5 }];
  assert.deepEqual(liveReferences('@x @z', refs).map((r) => r.label), ['@x']);
  assert.deepEqual(liveReferences('', undefined), []);
});

test('sending converts to byte offsets of the trimmed text', () => {
  const text = '  héllo @p';
  const refs = [{ kind: 'project', id: 'p1', label: '@p', start: 8, end: 10 }];
  const sent = referencesForSend(text, refs);
  // "héllo " is 7 bytes: é takes two.
  assert.deepEqual(sent, [{ kind: 'project', id: 'p1', label: '@p', start: 7, end: 9 }]);
});

test('stored byte offsets come back to editor offsets, and bad ones are dropped', () => {
  const text = 'héllo @p';
  assert.deepEqual(referencesFromStored(text, [{ label: '@p', start: 7, end: 9 }]), [{ label: '@p', start: 6, end: 8 }]);
  // An offset inside a multi-byte character, or one that points at other text.
  assert.deepEqual(referencesFromStored(text, [{ label: '@p', start: 2, end: 4 }]), []);
  assert.deepEqual(referencesFromStored(text, [{ label: '@q', start: 7, end: 9 }]), []);
  assert.deepEqual(referencesFromStored(text, null), []);
  assert.deepEqual(referencesToDraft(text, [{ label: '@p', start: 7, end: 9 }], 10), [{ label: '@p', start: 16, end: 18 }]);
});

test('file and folder labels spell their workspace path', () => {
  assert.equal(referenceLabel({ kind: 'file', detail: 'src/a.go' }, '@'), './src/a.go');
  assert.equal(referenceLabel({ kind: 'folder', detail: '/abs/dir' }, '@'), '/abs/dir');
  assert.equal(referenceLabel({ kind: 'project', name: 'web' }, '@'), '@web');
  assert.equal(slug('  My Project  '), 'my-project');
});
