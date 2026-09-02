'use strict';

const fs = require('node:fs');
const http = require('node:http');
const https = require('node:https');
const net = require('node:net');
const path = require('node:path');

const LOOPBACK_HOSTS = new Set(['127.0.0.1', '::1', '[::1]', 'localhost']);

function normalizeLoopbackURL(rawValue) {
  const value = String(rawValue || '').trim();
  if (!value) throw new Error('NusaShell URL is empty.');

  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error(`Invalid NusaShell URL: ${value}`);
  }

  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('NusaShell URL must use http or https.');
  }
  if (!LOOPBACK_HOSTS.has(url.hostname.toLowerCase())) {
    throw new Error('NusaShell Electron only loads a loopback URL.');
  }
  if (url.username || url.password) {
    throw new Error('NusaShell URL must not contain credentials.');
  }

  // The server root is the canonical entry point. Preserve an explicit path
  // for fixture/dev servers, but always give loadURL a stable trailing slash.
  if (!url.pathname) url.pathname = '/';
  return url;
}

function isSameOriginURL(candidateValue, appURL) {
  try {
    const candidate = new URL(candidateValue);
    const base = appURL instanceof URL ? appURL : new URL(appURL);
    return candidate.origin === base.origin;
  } catch {
    return false;
  }
}

function isExternalHTTPURL(value) {
  try {
    const url = new URL(value);
    return (url.protocol === 'http:' || url.protocol === 'https:') && !url.username && !url.password;
  } catch {
    return false;
  }
}

const defaultElectronSandboxPath = path.join(__dirname, '..', 'node_modules', 'electron', 'dist', 'chrome-sandbox');

function electronDevArgs({
  platform = process.platform,
  sandboxPath = defaultElectronSandboxPath,
  statSync = fs.statSync,
} = {}) {
  // This helper is used only by development and renderer-test launchers. The
  // packaged main process never adds --no-sandbox.
  if (platform !== 'linux') return [];
  try {
    const stat = statSync(sandboxPath);
    const mode = stat.mode & 0o7777;
    if (stat.isFile() && stat.uid === 0 && mode === 0o4755) return [];
  } catch {
    // A fresh npm install may not have materialized the helper yet.
  }
  return ['--no-sandbox'];
}

function withWindowsExtension(candidate, platform = process.platform) {
  return platform === 'win32' && !path.extname(candidate) ? `${candidate}.exe` : candidate;
}

function firstExistingFile(candidates, {
  platform = process.platform,
  statSync = fs.statSync,
} = {}) {
  for (const candidate of candidates) {
    const variants = [candidate];
    if (platform === 'win32' && !candidate.toLowerCase().endsWith('.exe')) {
      variants.push(`${candidate}.exe`);
      if (!candidate.toLowerCase().endsWith('.cmd') && !candidate.toLowerCase().endsWith('.bat')) {
        variants.push(`${candidate}.cmd`);
      }
    }
    for (const variant of variants) {
      try {
        if (statSync(variant).isFile()) return variant;
      } catch {
        // The next candidate is authoritative when this one is absent.
      }
    }
  }
  return null;
}

function pathEntries(environment, platform) {
  const rawPath = environment.PATH || environment.Path || '';
  return rawPath.split(platform === 'win32' ? ';' : ':').filter(Boolean);
}

function externalBackendCandidates({ platform, environment }) {
  const home = environment.HOME || environment.USERPROFILE || '';
  const candidates = [];

  if (platform === 'win32') {
    const localAppData = environment.LOCALAPPDATA || '';
    if (localAppData) {
      candidates.push(
        path.join(localAppData, 'Programs', 'NusaShell', 'current', 'nusashell.exe'),
        path.join(localAppData, 'Programs', 'NusaShell', 'current', 'nusashell'),
      );
    }
  } else if (home) {
    candidates.push(
      path.join(home, '.local', 'share', 'nusashell', 'current', 'nusashell'),
      path.join(home, 'Library', 'Application Support', 'nusashell', 'current', 'nusashell'),
    );
  }

  for (const directory of pathEntries(environment, platform)) {
    candidates.push(path.join(directory, withWindowsExtension('nusashell', platform)));
    if (platform === 'win32') candidates.push(path.join(directory, 'nusashell.cmd'));
  }

  if (platform !== 'win32') {
    candidates.push('/usr/local/bin/nusashell', '/usr/bin/nusashell');
  }
  return candidates;
}

function resolveBackendPath({
  explicitPath,
  packaged,
  resourcesPath,
  repositoryRoot,
  platform = process.platform,
  environment = process.env,
  statSync = fs.statSync,
}) {
  if (explicitPath) {
    const explicit = path.resolve(explicitPath);
    return firstExistingFile([explicit], { platform, statSync });
  }

  if (packaged) {
    // Electron is deliberately only a UI wrapper. The Go core is installed
    // separately and must be discoverable outside the packaged resources.
    return firstExistingFile(externalBackendCandidates({ platform, environment }), { platform, statSync });
  }

  return firstExistingFile([
    path.join(repositoryRoot, 'apps', 'electron', 'runtime', withWindowsExtension('nusashell', platform)),
    path.join(repositoryRoot, 'bin', withWindowsExtension('nusashell', platform)),
    ...externalBackendCandidates({ platform, environment }),
  ], { platform, statSync });
}

function buildBackendEnvironment(baseEnvironment, port, packaged) {
  const environment = {
    ...baseEnvironment,
    NUSASHELL_HOST: '127.0.0.1',
    NUSASHELL_PORT: String(port),
  };

  // A packaged app always serves its embedded frontend. Leaving the disk
  // development switch inherited from a user's shell would make the child
  // depend on a source checkout that is not present beside the app.
  if (packaged) delete environment.NUSASHELL_DEV;
  // The wrapper owns the loopback boundary; never allow a child started by it
  // to widen the listener through an inherited remote-access override.
  delete environment.NUSASHELL_ALLOW_REMOTE;
  return environment;
}

function getFreePort(host = '127.0.0.1') {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen(0, host, () => {
      const address = server.address();
      const port = typeof address === 'object' && address ? address.port : 0;
      server.close((closeError) => closeError ? reject(closeError) : resolve(port));
    });
  });
}

function probeURL(rawURL, timeoutMs = 1000) {
  const url = rawURL instanceof URL ? rawURL : new URL(rawURL);
  const client = url.protocol === 'https:' ? https : http;
  return new Promise((resolve, reject) => {
    const request = client.get(url, (response) => {
      response.resume();
      response.once('end', () => {
        if (response.statusCode >= 200 && response.statusCode < 400) {
          resolve(response.statusCode);
        } else {
          reject(new Error(`NusaShell returned HTTP ${response.statusCode}.`));
        }
      });
    });
    request.once('error', reject);
    request.setTimeout(timeoutMs, () => {
      request.destroy(new Error('NusaShell health probe timed out.'));
    });
  });
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForURL(rawURL, { timeoutMs = 30000, intervalMs = 100, probeTimeoutMs = 1000, isStopped = () => false } = {}) {
  const url = rawURL instanceof URL ? rawURL : new URL(rawURL);
  const deadline = Date.now() + timeoutMs;
  let lastError = null;

  while (Date.now() < deadline) {
    if (isStopped()) throw new Error('NusaShell backend exited before becoming ready.');
    try {
      await probeURL(url, probeTimeoutMs);
      return url;
    } catch (error) {
      lastError = error;
    }
    await delay(Math.min(intervalMs, Math.max(1, deadline - Date.now())));
  }

  throw new Error(`Timed out waiting for NusaShell at ${url.origin}. ${lastError?.message || ''}`.trim());
}

module.exports = {
  buildBackendEnvironment,
  electronDevArgs,
  firstExistingFile,
  getFreePort,
  isExternalHTTPURL,
  isSameOriginURL,
  normalizeLoopbackURL,
  probeURL,
  resolveBackendPath,
  waitForURL,
};
