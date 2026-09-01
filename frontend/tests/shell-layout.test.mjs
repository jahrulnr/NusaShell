import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { JSDOM } from 'jsdom';
import { test } from 'node:test';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');
const layoutCSS = await readFile(new URL('../styles/layout.css', import.meta.url), 'utf8');
const agentCSS = await readFile(new URL('../styles/agent.css', import.meta.url), 'utf8');

test('global identity and settings live in the vertical sidebar', () => {
  const document = new JSDOM(html).window.document;
  const titlebar = document.querySelector('.titlebar');
  const sidebar = document.querySelector('.sidebar');

  assert.ok(titlebar, 'the mobile shell still needs a titlebar host');
  assert.ok(sidebar, 'the global sidebar should exist');
  assert.equal(titlebar.querySelector('.brand'), null, 'desktop brand should not consume a horizontal titlebar row');
  assert.equal(titlebar.querySelector('#nav-settings-btn'), null, 'settings should be owned by the sidebar');
  assert.equal(titlebar.querySelector('#pwa-install-btn'), null, 'install should be owned by the sidebar');
  assert.ok(sidebar.querySelector('.brand'), 'sidebar should expose the NusaShell identity');
  assert.ok(sidebar.querySelector('#conn-fill'), 'sidebar should expose connection state');
  assert.ok(sidebar.querySelector('#nav-settings-btn'), 'sidebar should expose settings');
  assert.ok(sidebar.querySelector('#pwa-install-btn'), 'sidebar should expose install when available');
});

test('mini-window control is scoped to the active agent room info', () => {
  const document = new JSDOM(html).window.document;
  const miniWindow = document.getElementById('mini-window-btn');
  const roomInfo = document.getElementById('agent-room-info');
  const titlebar = document.querySelector('.titlebar');

  assert.ok(miniWindow, 'mini-window control should remain available');
  assert.ok(roomInfo?.contains(miniWindow), 'mini-window should sit with room diagnostics');
  assert.equal(titlebar?.contains(miniWindow), false, 'mini-window should leave global titlebar');
});

test('desktop shell hides the empty titlebar while mobile keeps the navigation affordance', () => {
  assert.match(layoutCSS, /\.titlebar\s*\{[\s\S]*?display:\s*none;/,
    'desktop should reclaim the titlebar height');
  assert.match(layoutCSS, /@media \(max-width: 680px\)[\s\S]*?\.titlebar\s*\{[\s\S]*?display:\s*flex;/,
    'mobile should keep a compact titlebar for the drawer toggle');
  assert.match(layoutCSS, /\.sidebar-brand\s*\{/, 'sidebar identity needs an explicit layout owner');
  assert.match(layoutCSS, /\.sidebar-actions\s*\{/, 'sidebar utility controls need an explicit layout owner');
});

test('mobile drawer keeps its hamburger above the backdrop for a reliable close toggle', () => {
  const mobileRules = layoutCSS.slice(layoutCSS.indexOf('@media (max-width: 680px)'));
  assert.match(
    mobileRules,
    /\.mobile-nav-toggle\s*\{[\s\S]*?display:\s*inline-flex;[\s\S]*?position:\s*relative;[\s\S]*?z-index:\s*80;/,
    'the mobile toggle must remain clickable while the backdrop is open'
  );
});

test('room info controls stay compact and aligned as one control group', () => {
  assert.match(agentCSS, /\.agent-room-info-controls\s*\{[\s\S]*?display:\s*flex;[\s\S]*?align-items:\s*center;/,
    'room info and mini-window controls should share one compact row');
  assert.match(agentCSS, /\.agent-room-info-mini\s*\{[\s\S]*?width:\s*30px;[\s\S]*?height:\s*30px;/,
    'mini-window should have a compact, touchable target');
});
