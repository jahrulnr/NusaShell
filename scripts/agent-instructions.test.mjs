import assert from 'node:assert/strict';
import { readdir, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const fileReadLimitBytes = 32 * 1024;
const safeChainLimitBytes = 24 * 1024;
const ignoredDirs = new Set([
  '.experimental', '.git', '.nusashell', '.playwright-mcp', 'bin',
  'node_modules', 'release',
]);

async function findAgentFiles(dir = repoRoot) {
  const found = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (entry.isDirectory() && !ignoredDirs.has(entry.name)) {
      found.push(...await findAgentFiles(path.join(dir, entry.name)));
    } else if (entry.isFile() && entry.name === 'AGENTS.md') {
      found.push(path.join(dir, entry.name));
    }
  }
  return found;
}

function instructionChain(file, knownFiles) {
  const chain = [];
  let dir = path.dirname(file);
  while (dir.startsWith(repoRoot)) {
    const candidate = path.join(dir, 'AGENTS.md');
    if (knownFiles.has(candidate)) chain.push(candidate);
    if (dir === repoRoot) break;
    dir = path.dirname(dir);
  }
  return chain.reverse();
}

test('AGENTS.md files fit one read and leave merged-chain headroom', async () => {
  const files = await findAgentFiles();
  const knownFiles = new Set(files);
  const sizes = new Map();

  for (const file of files) {
    const bytes = (await stat(file)).size;
    sizes.set(file, bytes);
    assert.ok(bytes < fileReadLimitBytes,
      `${path.relative(repoRoot, file)} is ${bytes} bytes, file_read defaults to ${fileReadLimitBytes}`);
    const content = await readFile(file, 'utf8');
    assert.ok(content.trim(), `${path.relative(repoRoot, file)} must not be empty`);
  }

  for (const file of files) {
    const chain = instructionChain(file, knownFiles);
    const bytes = chain.reduce((sum, item) => sum + sizes.get(item), 0);
    assert.ok(bytes < safeChainLimitBytes,
      `${chain.map((item) => path.relative(repoRoot, item)).join(' + ')} is ${bytes} bytes; `
      + `keep below ${safeChainLimitBytes} to leave headroom under the 32 KiB discovery cap`);
  }
});
