import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function extractReleaseNotes(changelog, rawVersion) {
  const version = String(rawVersion ?? '').trim().replace(/^v/, '');
  if (!version) throw new Error('Release version is empty');

  const lines = changelog.replace(/\r\n/g, '\n').split('\n');
  const heading = new RegExp(`^## \\[${escapeRegExp(version)}\\](?:\\s+-\\s+.+)?\\s*$`);
  const start = lines.findIndex((line) => heading.test(line));
  if (start === -1) throw new Error(`CHANGELOG.md has no section for version ${version}`);

  const nextHeading = lines.findIndex((line, index) => index > start && /^##\s+/.test(line));
  const end = nextHeading === -1 ? lines.length : nextHeading;
  const notes = lines.slice(start + 1, end).join('\n').trim();
  if (!notes) throw new Error(`CHANGELOG.md section for version ${version} is empty`);
  return notes;
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : '';
if (import.meta.url === invokedPath) {
  const [version, changelogPath = 'CHANGELOG.md'] = process.argv.slice(2);
  try {
    process.stdout.write(`${extractReleaseNotes(readFileSync(changelogPath, 'utf8'), version)}\n`);
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  }
}
