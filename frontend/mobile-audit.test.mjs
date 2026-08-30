import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const skillsCSS = await readFile(new URL('./styles/skills.css', import.meta.url), 'utf8');
const telemetryCSS = await readFile(new URL('./styles/telemetry.css', import.meta.url), 'utf8');
const automationCSS = await readFile(new URL('./styles/automation.css', import.meta.url), 'utf8');
const drawerCSS = await readFile(new URL('./styles/drawer.css', import.meta.url), 'utf8');
const providersCSS = await readFile(new URL('./styles/providers.css', import.meta.url), 'utf8');
const settingsCSS = await readFile(new URL('./styles/settings.css', import.meta.url), 'utf8');

test('Skills mobile view stacks every usable pane and keeps the workspace scrollable', () => {
  const mobileRules = skillsCSS.slice(skillsCSS.indexOf('@media (max-width: 760px)'));
  assert.match(mobileRules, /display:\s*flex/);
  assert.match(mobileRules, /flex-direction:\s*column/);
  assert.match(mobileRules, /grid-template-columns:\s*1fr\s*!important/);
  assert.match(mobileRules, /\.skills-tree-pane\s*\{[\s\S]*display:\s*flex/);
  assert.match(mobileRules, /overflow-y:\s*auto/);
});

test('Telemetry mobile cards and canvases can shrink to the single-column grid', () => {
  assert.match(telemetryCSS, /\.telemetry-body\s*\{[\s\S]*min-width:\s*0/);
  assert.match(telemetryCSS, /\.telemetry-charts\s*\{[\s\S]*min-width:\s*0/);
  assert.match(telemetryCSS, /\.telemetry-chart-card\s*\{[\s\S]*min-width:\s*0/);
  assert.match(telemetryCSS, /\.telemetry-chart-card canvas\s*\{[\s\S]*max-width:\s*100%/);
  assert.match(telemetryCSS, /\.telemetry-chart-wide\s*\{\s*grid-column:\s*span\s*1/);
});

test('Automation mobile header and tab controls stay inside the viewport', () => {
  const mobileRules = automationCSS.slice(automationCSS.indexOf('@media (max-width: 680px)'));
  assert.match(mobileRules, /flex-direction:\s*column/);
  assert.match(mobileRules, /flex-wrap:\s*wrap/);
  assert.match(mobileRules, /max-width:\s*100%/);
  assert.match(mobileRules, /overflow-x:\s*auto/);
});

test('Detail drawers never exceed a phone viewport', () => {
  assert.match(drawerCSS, /\.drawer-overlay\s*\{[\s\S]*width:\s*min\(340px,\s*100vw\)/);
  assert.match(drawerCSS, /\.mcp-drawer,[\s\S]*\.plugin-drawer\s*\{[\s\S]*width:\s*min\(340px,\s*100vw\)/);
});

test('Long mobile view headers give their actions a full-width row', () => {
  const providerMobile = providersCSS.slice(providersCSS.indexOf('@media (max-width: 480px)'));
  const settingsMobile = settingsCSS.slice(settingsCSS.indexOf('@media (max-width: 480px)'));
  assert.match(providerMobile, /\.providers-view \.view-header\s*\{[\s\S]*flex-direction:\s*column/);
  assert.match(providerMobile, /\.providers-view \.view-header-actions\s*\{[\s\S]*width:\s*100%/);
  assert.match(settingsMobile, /\.settings-view \.view-header\s*\{[\s\S]*flex-direction:\s*column/);
  assert.match(settingsMobile, /\.settings-header-actions\s*\{[\s\S]*width:\s*100%/);
});
