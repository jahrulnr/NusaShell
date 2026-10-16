import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const skillsCSS = await readFile(new URL('../skills.css', import.meta.url), 'utf8');
const telemetryCSS = await readFile(new URL('../telemetry.css', import.meta.url), 'utf8');
const automationCSS = await readFile(new URL('../automation.css', import.meta.url), 'utf8');
const shellCSS = await readFile(new URL('../shell.css', import.meta.url), 'utf8');
const providersCSS = await readFile(new URL('../providers.css', import.meta.url), 'utf8');
const settingsCSS = await readFile(new URL('../settings.css', import.meta.url), 'utf8');

test('Skills mobile view stacks every usable pane and keeps the workspace scrollable', () => {
  const mobileRules = skillsCSS.slice(skillsCSS.indexOf('@media (max-width: 720px)'));
  assert.match(mobileRules, /\.skills-page\s*\{[^}]*overflow-y:\s*auto/);
  assert.match(mobileRules, /\.skills-workspace\s*\{[^}]*display:\s*block;[^}]*overflow:\s*visible/);
  assert.match(mobileRules, /\.skill-sheet-body\s*\{[^}]*flex-direction:\s*column/);
});

test('Telemetry mobile cards and canvases can shrink to the single-column grid', () => {
  assert.match(telemetryCSS, /\.telemetry-dashboard\s*\{[^}]*min-width:\s*0/);
  assert.match(telemetryCSS, /\.telemetry-panel\s*\{[^}]*min-width:\s*0/);
  assert.match(telemetryCSS, /\.telemetry-chart-frame canvas\s*\{[^}]*width:\s*100%/);
  const mobile = telemetryCSS.slice(telemetryCSS.indexOf('@media (max-width: 850px)'));
  assert.match(mobile, /\.telemetry-dashboard\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/);
});

test('Automation mobile header and workbench stay inside the viewport', () => {
  const mobileRules = automationCSS.slice(automationCSS.indexOf('@media (max-width: 760px)'));
  assert.match(mobileRules, /\.automation-topbar\s*\{[^}]*flex-direction:\s*column/);
  assert.match(mobileRules, /\.automation-topbar-actions\s*\{[^}]*flex-wrap:\s*wrap/);
  assert.match(mobileRules, /\.automation-workbench\s*\{[^}]*flex-direction:\s*column/);
});

test('The mobile shell keeps the full viewport height now that the titlebar is gone', () => {
  const mobileRules = shellCSS.slice(shellCSS.indexOf('@media (max-width: 680px)'));
  assert.doesNotMatch(mobileRules, /\.shell\s*\{[^}]*height:\s*calc\(100% - \d+px\)/);
  assert.match(shellCSS, /^\.shell\s*\{[^}]*height:\s*100%/m);
  assert.match(shellCSS, /\.context-dock\s*\{[^}]*width:\s*min\(340px,\s*calc\(100vw - 52px\)\)/);
  assert.match(shellCSS, /\.global-nav\s*\{[^}]*width:\s*min\(244px,\s*calc\(100vw - 28px\)\)/);
});

test('Long mobile view headers give their actions a full-width row', () => {
  const providerNarrow = providersCSS.slice(providersCSS.indexOf('@media (max-width: 980px)'));
  const providerPhone = providersCSS.slice(providersCSS.indexOf('@media (max-width: 430px)'));
  const settingsMobile = settingsCSS.slice(settingsCSS.indexOf('@media (max-width: 860px)'));
  assert.match(providerNarrow, /\.providers-detail-header\s*\{[^}]*flex-direction:\s*column/);
  assert.match(providerPhone, /\.providers-detail-actions\s*\{[^}]*width:\s*100%/);
  assert.match(settingsMobile, /\.settings-topbar\s*\{[^}]*align-items:\s*flex-start;[^}]*flex-wrap:\s*wrap/);
});
