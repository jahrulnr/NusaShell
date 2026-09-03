import { createHash } from 'node:crypto';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { pathToFileURL } from 'node:url';

import { validateVersion } from './version.mjs';

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function artifactKey(name, version, product) {
  const escapedVersion = escapeRegExp(version);
  if (product === 'go') {
    if (name === `nusashell-${version}-linux-x64.tar.gz`) return 'linux-x64';
    if (name === `nusashell-${version}-darwin-x64.tar.gz`) return 'darwin-x64';
    if (name === `nusashell-${version}-darwin-arm64.tar.gz`) return 'darwin-arm64';
    const windows = name.match(new RegExp(`^nusashell-${escapedVersion}-(?:win|win32)-(x64|x86_64)\\.zip$`));
    return windows ? 'win32-x64' : null;
  }
  if (product === 'electron') {
    if (name === `nusashell-electron-${version}-linux-x64.tar.gz`) return 'linux-x64';
    const windows = name.match(new RegExp(`^NusaShell-Electron-${escapedVersion}-(?:win|win32)-(x64|x86_64)\\.zip$`));
    if (windows) return 'win32-x64';
    const mac = name.match(new RegExp(`^NusaShell-Electron-${escapedVersion}-(?:darwin|mac)-(x64|arm64)\\.zip$`));
    return mac ? `darwin-${mac[1]}` : null;
  }
  if (product === 'pets') {
    const linux = name.match(new RegExp(`^nusashell-pets-${escapedVersion}-linux-(x64|arm64)\\.tar\\.gz$`));
    return linux ? `linux-${linux[1]}` : null;
  }
  throw new Error(`Unsupported release product: ${product}`);
}

async function listFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  return (await Promise.all(entries.map(async (entry) => {
    const filePath = join(directory, entry.name);
    return entry.isDirectory() ? listFiles(filePath) : [filePath];
  }))).flat();
}

export async function buildReleaseManifest(rawVersion, root, product = 'go') {
  const version = validateVersion(rawVersion);
  const manifest = { product, version, files: {} };
  for (const filePath of await listFiles(root)) {
    const name = filePath.split(/[\\/]/).pop();
    const key = artifactKey(name, version, product);
    if (!key) continue;
    if (manifest.files[key]) throw new Error(`Duplicate release payload for ${key}: ${name}`);
    manifest.files[key] = {
      name,
      sha256: createHash('sha256').update(await readFile(filePath)).digest('hex'),
    };
  }
  if (!Object.keys(manifest.files).length) throw new Error(`No release payloads found for ${version}`);
  return manifest;
}

export async function writeReleaseManifest(rawVersion, root, output, product = 'go') {
  const manifest = await buildReleaseManifest(rawVersion, root, product);
  await writeFile(output, `${JSON.stringify(manifest, null, 2)}\n`);
  return manifest;
}

const invokedPath = process.argv[1] ? pathToFileURL(process.argv[1]).href : '';
if (import.meta.url === invokedPath) {
  const [version, root = 'release-artifacts/go', output = join(root, 'latest.json'), product = 'go'] = process.argv.slice(2);
  if (!version) {
    process.stderr.write('Usage: node scripts/release-manifest.mjs <version> [root] [output] [go|electron|pets]\n');
    process.exitCode = 1;
  } else {
    try {
      const manifest = await writeReleaseManifest(version, root, output, product);
      process.stdout.write(`Wrote ${relative(process.cwd(), output)} for ${Object.keys(manifest.files).join(', ')}\n`);
    } catch (error) {
      process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
      process.exitCode = 1;
    }
  }
}
