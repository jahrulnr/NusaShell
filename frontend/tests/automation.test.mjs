import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const view = await readFile(new URL('../page/automation/app.js', import.meta.url), 'utf8');
const model = await readFile(new URL('../page/automation/model.js', import.meta.url), 'utf8');
const css = await readFile(new URL('../automation.css', import.meta.url), 'utf8');

test('Automation view is wired in the shell', () => {
  assert.match(html, /data-view="automation"/);
  assert.match(html, /id="automation-create"/);
  assert.match(html, /id="automation-list"/);
  assert.match(html, /id="automation-list-count"/);
  assert.match(html, /id="automation-inspector"/);
  assert.match(html, /id="automation-run-detail"/);
  assert.match(html, /data-automation-mode="workflows"/);
  assert.match(html, /data-automation-mode="runs"/);
  assert.match(html, /href="\.\/automation\.css\?v=/);
});

test('Automation view talks to Automation RPC', () => {
  for (const method of [
    'automation.list', 'automation.save', 'automation.run', 'automation.validate',
    'automation.runs.list', 'automation.runs.cancel', 'automation.runs.steer',
  ]) {
    assert.match(view, new RegExp(method.replaceAll('.', '\\.')));
  }
  assert.match(model, /availability === 'invalid'/);
  assert.match(view, /canRunWorkflow/);
  assert.match(view, /canSteerRun/);
  assert.match(css, /\.automation-status-invalid/);
  assert.match(css, /\.automation-callout\.is-invalid/);
});

test('Automation mode switch is a bounded two-lane control', () => {
  assert.match(html, /class="automation-mode-switch" role="group" aria-label="Show automation records"/);
  assert.match(css, /\.automation-mode-switch\s*\{[^}]*padding:\s*3px;[^}]*border:\s*1px solid var\(--line\);/);
  const mobile = css.slice(css.indexOf('@media (max-width: 760px)'));
  assert.match(mobile, /\.automation-topbar\s*\{[^}]*flex-direction:\s*column/);
  assert.match(mobile, /\.automation-topbar-actions\s*\{[^}]*flex-wrap:\s*wrap/);
});

test('Automation mode buttons press state and the inspector stay in sync', () => {
  assert.match(html, /data-automation-mode="workflows"[^>]*aria-pressed="true"/);
  assert.match(html, /data-automation-mode="runs"[^>]*aria-pressed="false"/);
  assert.match(html, /id="automation-inspector"[^>]*aria-label="Automation details"/);
  assert.match(view, /button\.setAttribute\('aria-pressed', String\(pressed\)\)/);
});
