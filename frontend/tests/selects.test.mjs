// Guard: AGENTS.md forbids visible native <select> option menus. Every
// markup select must be wrapped with createSelect (SlimSelect).
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function walkJs(dir) {
  const out = [];
  for (const name of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, name.name);
    if (name.isDirectory()) out.push(...walkJs(path));
    else if (name.name.endsWith('.js')) out.push(path);
  }
  return out;
}

function wrapsSelect(source, id) {
  if (
    source.includes(`createSelect(document.getElementById('${id}')`)
    || source.includes(`createSelect(document.getElementById("${id}")`)
  ) {
    return true;
  }
  const assign = source.match(
    new RegExp(`(?:const|let|var)\\s+(\\w+)\\s*=\\s*document\\.getElementById\\(['"]${id}['"]\\)`),
  );
  return Boolean(assign && source.includes(`createSelect(${assign[1]}`));
}

test('every markup <select> is enhanced with createSelect', () => {
  const html = readFileSync(join(frontend, 'index.html'), 'utf8');
  const ids = [...html.matchAll(/<select\b[^>]*\bid=["']([^"']+)/g)].map((m) => m[1]);
  assert.ok(ids.length > 0, 'expected at least one <select> in index.html');

  const sources = walkJs(join(frontend, 'js')).map((path) => readFileSync(path, 'utf8'));
  const missing = ids.filter((id) => !sources.some((src) => wrapsSelect(src, id)));
  assert.deepEqual(missing, [], `native selects without SlimSelect: ${missing.join(', ')}`);
});
