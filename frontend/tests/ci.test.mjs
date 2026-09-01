import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const view = await readFile(new URL('../js/views/ci.js', import.meta.url), 'utf8');
const css = await readFile(new URL('../styles/ci.css', import.meta.url), 'utf8');

test('CI view is wired in the shell', () => {
  assert.match(html, /data-view="ci"/);
  assert.match(html, /id="ci-tab-workflows"/);
  assert.match(html, /id="ci-tab-runs"/);
  assert.match(html, /id="ci-tab-schedules"/);
  assert.match(html, /id="ci-tab-events"/);
  assert.match(html, /id="ci-new-btn"/);
  assert.match(html, /id="ci-blocked-banner"/);
  assert.match(html, /id="ci-enable-provider-btn"/);
  assert.match(html, /href="styles\/ci.css"/);
});

test('CI view talks to CI RPC', () => {
  for (const method of [
    'ci.list', 'ci.save', 'ci.run', 'ci.validate',
    'ci.runs.list', 'ci.runs.cancel',
  ]) {
    assert.match(view, new RegExp(method.replace('.', '\\.')));
  }
  assert.match(view, /createWizard/);
  assert.match(view, /availability === 'invalid'/);
  assert.match(view, /runBtn\.disabled = invalid/);
  assert.match(css, /\.ci-pill\.invalid/);
});
