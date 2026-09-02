import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const GO_PREFIXES = Object.freeze([
  'application/',
  'cmd/',
  'contracts/',
  'domain/',
  'frontend/',
  'infrastructure/',
  'resources/',
  'transport/',
]);

const ELECTRON_PREFIXES = Object.freeze(['apps/electron/']);
const SHARED_ELECTRON_PREFIXES = Object.freeze(['frontend/icons/']);

function normalizePath(file) {
  return String(file ?? '').trim().replaceAll('\\', '/').replace(/^\.\//, '');
}

function hasPrefix(path, prefixes) {
  return prefixes.some((prefix) => path.startsWith(prefix));
}

/**
 * Map changed repository paths to the product release streams they affect.
 * The Electron package is only a wrapper, so frontend/backend changes belong
 * to the Go stream; the wrapper is rebuilt only when its own package changes.
 */
export function detectReleaseStreams(files, { all = false } = {}) {
  if (all) {
    return { goChanged: true, electronChanged: true, hasChanges: true };
  }

  let goChanged = false;
  let electronChanged = false;
  for (const rawFile of Array.isArray(files) ? files : []) {
    const file = normalizePath(rawFile);
    if (!file) continue;

    if (
      file === 'VERSION' ||
      file === 'go.mod' ||
      file === 'go.sum' ||
      hasPrefix(file, GO_PREFIXES)
    ) {
      goChanged = true;
    }
    if (hasPrefix(file, ELECTRON_PREFIXES) || hasPrefix(file, SHARED_ELECTRON_PREFIXES)) {
      electronChanged = true;
    }
  }

  return { goChanged, electronChanged, hasChanges: goChanged || electronChanged };
}

const invokedPath = process.argv[1] ? pathToFileURL(process.argv[1]).href : '';
if (import.meta.url === invokedPath) {
  const all = process.argv.includes('--all');
  const files = all ? [] : readFileSync(0, 'utf8').split(/\r?\n/);
  const result = detectReleaseStreams(files, { all });
  process.stdout.write(`go_changed=${result.goChanged}\n`);
  process.stdout.write(`electron_changed=${result.electronChanged}\n`);
  process.stdout.write(`has_release_changes=${result.hasChanges}\n`);
}

