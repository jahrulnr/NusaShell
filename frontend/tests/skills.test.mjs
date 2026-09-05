import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const skillsView = await readFile(new URL('../js/views/skills.js', import.meta.url), 'utf8');

test('Skills detail shows status, version, promote, and rollback', () => {
  assert.match(html, /id="skills-promote"/);
  assert.match(html, /id="skills-rollback"/);
  assert.match(html, /id="skills-rollback-version"/);
  assert.match(html, /id="skill-status-badge"/);
  assert.match(skillsView, /skills\.promote/);
  assert.match(skillsView, /skills\.rollback/);
  assert.doesNotMatch(skillsView, /\.pinned\b/);
  assert.doesNotMatch(skillsView, /SkillState|skill\.state|s\.state\b/);
});
