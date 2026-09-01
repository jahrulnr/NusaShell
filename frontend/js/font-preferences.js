// Browser-only interface typography preferences. The font files themselves
// are bundled with the frontend so this preference never depends on an OS
// font or a third-party stylesheet being available at runtime.

export const FONT_STORAGE_KEY = 'nusashell.font';
export const DEFAULT_FONT_ID = 'plex';

export const FONT_OPTIONS = Object.freeze([
  {
    id: 'plex',
    label: 'Nusa Plex',
    description: 'Space Grotesk headings + IBM Plex Sans body',
  },
  {
    id: 'inter',
    label: 'Inter',
    description: 'Neutral, compact interface text',
  },
  {
    id: 'atkinson',
    label: 'Atkinson Hyperlegible Next',
    description: 'Distinct glyphs for comfortable reading',
  },
  {
    id: 'source-sans',
    label: 'Source Sans 3',
    description: 'Open, readable text with generous forms',
  },
]);

const FONT_IDS = new Set(FONT_OPTIONS.map((option) => option.id));

function browserStorage() {
  try {
    return globalThis.localStorage;
  } catch {
    return null;
  }
}

function documentRoot() {
  return typeof document === 'undefined' ? null : document.documentElement;
}

export function normalizeFontId(value) {
  const id = typeof value === 'string' ? value : '';
  return FONT_IDS.has(id) ? id : DEFAULT_FONT_ID;
}

export function readFontPreference(storage = browserStorage()) {
  try {
    return normalizeFontId(storage?.getItem(FONT_STORAGE_KEY));
  } catch {
    return DEFAULT_FONT_ID;
  }
}

export function applyFontPreference(value, root = documentRoot()) {
  const id = normalizeFontId(value);
  if (root?.dataset) root.dataset.font = id;
  return id;
}

export function persistFontPreference(value, storage = browserStorage()) {
  const id = normalizeFontId(value);
  try {
    storage?.setItem(FONT_STORAGE_KEY, id);
  } catch {
    // Private browsing and hardened webviews can reject localStorage writes.
  }
  return id;
}

export function setFontPreference(value, storage = browserStorage(), root = documentRoot()) {
  const id = persistFontPreference(value, storage);
  applyFontPreference(id, root);
  if (typeof window !== 'undefined' && typeof window.dispatchEvent === 'function' && typeof window.CustomEvent === 'function') {
    window.dispatchEvent(new window.CustomEvent('nusashell:font-change', { detail: { fontId: id } }));
  }
  return id;
}

export function resolvedFontFamily(token = '--font-body') {
  if (typeof document === 'undefined' || typeof getComputedStyle !== 'function') return '';
  return getComputedStyle(document.documentElement).getPropertyValue(token).trim();
}
