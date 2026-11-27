import { existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// Vendored libraries that the DOM shim cannot run, mapped to the test double
// that keeps the same surface. See slimselect-double.mjs.
const doubles = {
  '/vendor/slim-select/slimselect.es.js': 'tests/slimselect-double.mjs',
};

// A specifier like /vendor/preact/htm-preact.mjs is a path under web/, as
// the server serves it. A real absolute file path is left alone.
export function resolve(specifier, context, next) {
  if (doubles[specifier]) return next(pathToFileURL(path.join(root, doubles[specifier])).href, context);
  if (specifier.startsWith('/') && !specifier.startsWith('//')) {
    const file = path.join(root, specifier);
    if (existsSync(file)) return next(pathToFileURL(file).href, context);
  }
  return next(specifier, context);
}
