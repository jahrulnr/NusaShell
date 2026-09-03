import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

export const SEMVER_PATTERN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;

export function validateVersion(rawVersion) {
  const version = String(rawVersion ?? '').trim();
  if (!SEMVER_PATTERN.test(version)) {
    throw new Error(`VERSION must contain a valid semantic version, got ${JSON.stringify(version)}`);
  }
  return version;
}

export async function readVersion(versionPath) {
  return validateVersion(await readFile(versionPath, 'utf8'));
}

function setVersionMetadata(document, version, pathLabel) {
  if (!document || typeof document !== 'object' || Array.isArray(document)) {
    throw new Error(`${pathLabel} must contain a JSON object`);
  }
  document.version = version;
  return document;
}

function setLockVersion(document, version, pathLabel) {
  setVersionMetadata(document, version, pathLabel);
  if (!document.packages || typeof document.packages !== 'object' || Array.isArray(document.packages)) {
    throw new Error(`${pathLabel} must contain a packages object`);
  }
  if (!document.packages[''] || typeof document.packages[''] !== 'object') {
    throw new Error(`${pathLabel} must contain root package metadata`);
  }
  document.packages[''].version = version;
  return document;
}

async function readJSON(path) {
  try {
    return JSON.parse(await readFile(path, 'utf8'));
  } catch (error) {
    throw new Error(`Cannot read JSON metadata at ${path}: ${error.message}`);
  }
}

function serialized(document) {
  return `${JSON.stringify(document, null, 2)}\n`;
}

/**
 * Synchronize Electron's package metadata with apps/electron/VERSION.
 * `check: true` is used by CI and never writes files. Go and Electron keep
 * separate VERSION files so either release stream can advance independently.
 */
export async function syncElectronVersion(repoRoot, {
  check = false,
  versionPath = resolve(repoRoot, 'apps/electron/VERSION'),
  packagePath = resolve(repoRoot, 'apps/electron/package.json'),
  lockPath = resolve(repoRoot, 'apps/electron/package-lock.json'),
} = {}) {
  const version = await readVersion(versionPath);
  const packageJSON = await readJSON(packagePath);
  const lockJSON = await readJSON(lockPath);
  const expectedPackage = serialized(setVersionMetadata(packageJSON, version, packagePath));
  const expectedLock = serialized(setLockVersion(lockJSON, version, lockPath));
  const currentPackage = await readFile(packagePath, 'utf8');
  const currentLock = await readFile(lockPath, 'utf8');
  const mismatches = [];
  if (currentPackage !== expectedPackage) mismatches.push(packagePath);
  if (currentLock !== expectedLock) mismatches.push(lockPath);

  if (check && mismatches.length) {
    throw new Error(`Electron version drift: expected ${version} in ${mismatches.join(', ')}`);
  }
  if (!check) {
    if (currentPackage !== expectedPackage) await writeFile(packagePath, expectedPackage);
    if (currentLock !== expectedLock) await writeFile(lockPath, expectedLock);
  }

  return { version, changed: mismatches.length > 0, mismatches };
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (import.meta.url === invokedPath) {
  const [command = 'read'] = process.argv.slice(2);
  const repoRoot = fileURLToPath(new URL('..', import.meta.url));
  try {
    if (command === 'read') {
      process.stdout.write(`${await readVersion(resolve(repoRoot, 'apps/electron/VERSION'))}\n`);
    } else if (command === 'read-go') {
      process.stdout.write(`${await readVersion(resolve(repoRoot, 'VERSION'))}\n`);
    } else if (command === 'read-pets') {
      process.stdout.write(`${await readVersion(resolve(repoRoot, 'apps/pets/VERSION'))}\n`);
    } else if (command === 'sync') {
      const result = await syncElectronVersion(repoRoot);
      process.stdout.write(`${result.changed ? 'Synchronized' : 'Already synchronized'} Electron version ${result.version}\n`);
    } else if (command === 'check') {
      const result = await syncElectronVersion(repoRoot, { check: true });
      process.stdout.write(`Electron version ${result.version} is synchronized\n`);
    } else {
      throw new Error('Usage: node scripts/version.mjs [read|read-go|read-pets|sync|check]');
    }
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  }
}
