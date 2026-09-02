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

function versionNeedsRelease(stream, currentVersions, releaseIndex) {
  const current = String(currentVersions?.[stream] ?? '').trim();
  const released = String(releaseIndex?.[stream]?.version ?? '').trim();
  // A null pointer means the stream has never been published. It should not
  // make every documentation-only push publish both products; an explicit
  // VERSION change remains the signal for a stream's first release.
  return current !== '' && released !== '' && current !== released;
}

/**
 * Map changed repository paths to the product release streams they affect.
 * The Electron package is only a wrapper, so frontend/backend changes belong
 * to the Go stream; the wrapper is rebuilt only when its own package changes.
 * A non-null release pointer whose version differs from the checked-out
 * VERSION also keeps that stream eligible for a retry.
 */
export function detectReleaseStreams(files, {
  all = false,
  currentVersions = {},
  releaseIndex = null,
} = {}) {
  if (all) {
    return { goChanged: true, electronChanged: true, hasChanges: true };
  }

  let goChanged = versionNeedsRelease('go', currentVersions, releaseIndex);
  let electronChanged = versionNeedsRelease('electron', currentVersions, releaseIndex);
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
  const args = process.argv.slice(2);
  const all = args.includes('--all');
  const releaseIndexFlag = args.indexOf('--release-index');
  const releaseIndexPath = releaseIndexFlag >= 0 ? args[releaseIndexFlag + 1] : '';
  const files = all ? [] : readFileSync(0, 'utf8').split(/\r?\n/);
  const releaseIndex = releaseIndexPath
    ? JSON.parse(readFileSync(releaseIndexPath, 'utf8'))
    : null;
  const result = detectReleaseStreams(files, {
    all,
    currentVersions: {
      go: process.env.GO_VERSION,
      electron: process.env.ELECTRON_VERSION,
    },
    releaseIndex,
  });
  process.stdout.write(`go_changed=${result.goChanged}\n`);
  process.stdout.write(`electron_changed=${result.electronChanged}\n`);
  process.stdout.write(`has_release_changes=${result.hasChanges}\n`);
}
