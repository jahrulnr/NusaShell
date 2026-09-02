import { readFile, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

import { validateVersion } from './version.mjs';

export const RELEASE_INDEX_SCHEMA_VERSION = 1;

const STREAMS = Object.freeze({
  go: 'latest.json',
  electron: 'electron-latest.json',
});
const SAFE_TAG_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._+-]*$/;
const SAFE_MANIFEST_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]*\.json$/;

export function createReleaseIndex() {
  return {
    schemaVersion: RELEASE_INDEX_SCHEMA_VERSION,
    go: null,
    electron: null,
  };
}

function assertObject(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${label} must be an object`);
  }
}

function validateEntry(stream, entry) {
  if (entry === null) return null;
  assertObject(entry, `${stream} release entry`);

  const version = validateVersion(entry.version);
  const tag = String(entry.tag ?? '').trim();
  const expectedTagPrefix = `${stream}-v`;
  if (!tag.startsWith(expectedTagPrefix) || !SAFE_TAG_PATTERN.test(tag)) {
    throw new Error(`${stream} release tag must be a safe ${expectedTagPrefix}<version> tag`);
  }
  if (tag !== `${expectedTagPrefix}${version}`) {
    throw new Error(`${stream} release tag must match its version`);
  }

  const manifest = String(entry.manifest ?? STREAMS[stream]).trim();
  if (!SAFE_MANIFEST_PATTERN.test(manifest) || manifest !== STREAMS[stream]) {
    throw new Error(`${stream} release manifest must be ${STREAMS[stream]}`);
  }

  const releasedAt = String(entry.releasedAt ?? '').trim();
  if (!releasedAt || Number.isNaN(Date.parse(releasedAt))) {
    throw new Error(`${stream} releasedAt must be an ISO timestamp`);
  }

  return { version, tag, manifest, releasedAt };
}

export function validateReleaseIndex(index) {
  assertObject(index, 'release index');
  if (index.schemaVersion !== RELEASE_INDEX_SCHEMA_VERSION) {
    throw new Error(`Unsupported release index schema version: ${index.schemaVersion}`);
  }
  return {
    schemaVersion: RELEASE_INDEX_SCHEMA_VERSION,
    go: validateEntry('go', index.go ?? null),
    electron: validateEntry('electron', index.electron ?? null),
  };
}

/**
 * Update one or both product pointers without changing the other stream.
 * This is intentionally a pointer document: release assets stay immutable in
 * their stream-specific GitHub release, while this file tells installers
 * which release is current for each product.
 */
export function updateReleaseIndex(index, updates, releasedAt = new Date().toISOString()) {
  const current = validateReleaseIndex({ ...createReleaseIndex(), ...index });
  assertObject(updates, 'release index updates');
  const next = { ...current };

  for (const stream of Object.keys(STREAMS)) {
    if (!(stream in updates)) continue;
    const update = updates[stream];
    assertObject(update, `${stream} release update`);
    next[stream] = validateEntry(stream, {
      ...update,
      manifest: STREAMS[stream],
      releasedAt: update.releasedAt ?? releasedAt,
    });
  }

  return next;
}

export function releaseUpdatesFromEnvironment(environment = process.env) {
  const updates = {};
  if (environment.GO_CHANGED === 'true') {
    updates.go = { version: environment.GO_VERSION, tag: `go-v${environment.GO_VERSION}` };
  }
  if (environment.ELECTRON_CHANGED === 'true') {
    updates.electron = {
      version: environment.ELECTRON_VERSION,
      tag: `electron-v${environment.ELECTRON_VERSION}`,
    };
  }
  return updates;
}

function serialized(index) {
  return `${JSON.stringify(index, null, 2)}\n`;
}

async function readIndex(path) {
  try {
    return JSON.parse(await readFile(path, 'utf8'));
  } catch (error) {
    throw new Error(`Cannot read release index at ${path}: ${error.message}`);
  }
}

const invokedPath = process.argv[1] ? pathToFileURL(process.argv[1]).href : '';
if (import.meta.url === invokedPath) {
  const [command, indexPath, updatesJSON = '{}'] = process.argv.slice(2);
  try {
    if (!indexPath || !['update', 'update-from-env'].includes(command)) {
      throw new Error('Usage: node scripts/release-index.mjs update <path> [updates-json]');
    }
    const updates = command === 'update-from-env'
      ? releaseUpdatesFromEnvironment()
      : JSON.parse(updatesJSON);
    const current = await readIndex(indexPath);
    const updated = updateReleaseIndex(current, updates);
    await writeFile(indexPath, serialized(updated));
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  }
}
