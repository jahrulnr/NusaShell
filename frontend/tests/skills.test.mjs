import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { skillIsDeletable } from '../js/views/skills.js';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const skillsView = await readFile(new URL('../js/views/skills.js', import.meta.url), 'utf8');

test('Skills detail shows status, version, promote, rollback, and delete', () => {
  const doc = new JSDOM(html).window.document;
  const deleteBtn = doc.getElementById('skills-delete');
  assert.match(html, /id="skills-promote"/);
  assert.match(html, /id="skills-rollback"/);
  assert.match(html, /id="skills-rollback-version"/);
  assert.match(html, /id="skill-status-badge"/);
  assert.ok(deleteBtn);
  assert.equal(deleteBtn.textContent.trim(), 'Delete');
  assert.equal(deleteBtn.hidden, true);
  assert.match(skillsView, /skills\.promote/);
  assert.match(skillsView, /skills\.rollback/);
  assert.match(skillsView, /skills\.delete/);
  assert.match(skillsView, /confirmDialog\(/);
  assert.doesNotMatch(skillsView, /\.pinned\b/);
  assert.doesNotMatch(skillsView, /SkillState|skill\.state|s\.state\b/);
});

test('skillIsDeletable allows learned and user skills, not builtin or plugin', () => {
  assert.equal(skillIsDeletable({ owned_by: 'learned', status: 'experimental' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'learned', status: 'trusted' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'user', origin: 'user' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'builtin' }), false);
  assert.equal(skillIsDeletable({ owned_by: 'plugin:notes' }), false);
  assert.equal(skillIsDeletable({ origin: 'learned' }), true);
  assert.equal(skillIsDeletable({}), false);
});
