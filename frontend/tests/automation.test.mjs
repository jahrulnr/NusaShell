import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const view = await readFile(new URL('../js/views/automation.js', import.meta.url), 'utf8');
const css = await readFile(new URL('../styles/automation.css', import.meta.url), 'utf8');

test('Automation view is wired in the shell', () => {
  assert.match(html, /data-view="automation"/);
  assert.match(html, /id="automation-tab-workflows"/);
  assert.match(html, /id="automation-tab-runs"/);
  assert.match(html, /id="automation-tab-schedules"/);
  assert.match(html, /id="automation-tab-events"/);
  assert.match(html, /id="automation-new-btn"/);
  assert.match(html, /id="automation-blocked-banner"/);
  assert.match(html, /id="automation-enable-provider-btn"/);
  assert.match(html, /href="styles\/automation.css"/);
});

test('Automation view talks to Automation RPC', () => {
  for (const method of [
    'automation.list', 'automation.save', 'automation.run', 'automation.validate',
    'automation.runs.list', 'automation.runs.cancel',
  ]) {
    assert.match(view, new RegExp(method.replace('.', '\\.')));
  }
  assert.match(view, /createWizard/);
  assert.match(view, /availability === 'invalid'/);
  assert.match(view, /runBtn\.disabled = invalid/);
  assert.match(css, /\.automation-pill\.invalid/);
});
