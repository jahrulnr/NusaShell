import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

import { filterLauncherPlugins } from './js/views/home.js';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const homeView = await readFile(new URL('./js/views/home.js', import.meta.url), 'utf8');
const homeCSS = await readFile(new URL('./styles/home.css', import.meta.url), 'utf8');

test('Home keeps the Electron launcher markup contract', () => {
  assert.match(html, /class="search home-search"/);
  assert.match(html, /id="search-input"/);
  assert.match(html, /id="search-clear"/);
  assert.match(html, /id="launcher-category-label"/);
  assert.match(html, /id="launcher-tabs"/);
});

test('Home uses the Electron launcher controls and workbench sizing', () => {
  for (const id of ['search-input', 'search-clear', 'launcher-tabs']) {
    assert.match(homeView, new RegExp(`getElementById\\('${id}'\\)`));
  }
  assert.match(homeCSS, /grid-template-columns:\s*repeat\(auto-fill, minmax\(152px, 176px\)\)/);
  assert.match(homeCSS, /\.app-icon\s*\{[\s\S]*width:\s*128px;[\s\S]*height:\s*128px;/);
  assert.match(homeCSS, /\.app-icon::after\s*\{/);
});

test('Home search matches plugin name, id, and manifest description', () => {
  const plugins = [
    { id: 'notes.app', name: 'Notes', manifest: { description: 'Capture ideas' } },
    { id: 'mail.app', name: 'Mail', manifest: { description: 'Read messages' } },
  ];
  assert.deepEqual(filterLauncherPlugins(plugins, 'capture'), [plugins[0]]);
  assert.deepEqual(filterLauncherPlugins(plugins, 'mail.app'), [plugins[1]]);
  assert.deepEqual(filterLauncherPlugins(plugins, 'NOTES'), [plugins[0]]);
});
