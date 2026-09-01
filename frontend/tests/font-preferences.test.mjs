import assert from 'node:assert/strict';
import { access, readFile } from 'node:fs/promises';
import { test } from 'node:test';

const moduleURL = new URL('../js/font-preferences.js', import.meta.url);
const globalCSS = await readFile(new URL('../styles/global.css', import.meta.url), 'utf8');
const fontsCSS = await readFile(new URL('../fonts/fonts.css', import.meta.url), 'utf8');

test('font preference module exposes validated local font options', async () => {
  const {
    DEFAULT_FONT_ID,
    FONT_OPTIONS,
    applyFontPreference,
    normalizeFontId,
    readFontPreference,
    setFontPreference,
  } = await import(`${moduleURL.href}?test=font-options`);

  assert.equal(DEFAULT_FONT_ID, 'plex');
  assert.ok(FONT_OPTIONS.length >= 4);
  assert.ok(FONT_OPTIONS.some((option) => option.id === 'atkinson'));
  assert.ok(FONT_OPTIONS.some((option) => option.id === 'inter'));
  assert.equal(normalizeFontId('not-a-font'), DEFAULT_FONT_ID);

  const storage = {
    values: new Map([['nusashell.font', 'source-sans']]),
    getItem(key) { return this.values.get(key) ?? null; },
    setItem(key, value) { this.values.set(key, value); },
  };
  const root = { dataset: {} };

  assert.equal(readFontPreference(storage), 'source-sans');
  assert.equal(applyFontPreference('atkinson', root), 'atkinson');
  assert.equal(root.dataset.font, 'atkinson');
  assert.equal(setFontPreference('inter', storage, root), 'inter');
  assert.equal(storage.getItem('nusashell.font'), 'inter');
  assert.equal(root.dataset.font, 'inter');
});

test('font stack bundles local symbol and color emoji coverage', async () => {
  assert.match(globalCSS, /--font-symbols: 'Noto Sans Symbols 2'/);
  assert.match(globalCSS, /--font-emoji: 'Noto Color Emoji'/);
  assert.match(globalCSS, /data-font="inter"/);
  assert.doesNotMatch(globalCSS, /Segoe UI|JetBrains Mono|Cascadia Code/);
  assert.match(fontsCSS, /font-family: 'Noto Sans Symbols 2'/);
  assert.match(fontsCSS, /font-family: 'Noto Color Emoji'/);
  assert.match(fontsCSS, /tech\(color-COLRv1\)/);
  for (const filename of [
    'inter-latin.woff2',
    'atkinson-hyperlegible-next-latin.woff2',
    'source-sans-3-latin.woff2',
    'noto-sans-symbols-2-regular.ttf',
    'noto-color-emoji.ttf',
  ]) {
    await access(new URL(`../fonts/${filename}`, import.meta.url));
  }
});
