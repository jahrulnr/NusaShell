import { readFile } from 'node:fs/promises';
import test from 'node:test';
import assert from 'node:assert/strict';

const workflow = await readFile(new URL('../.github/workflows/ci.yml', import.meta.url), 'utf8');

function jobSection(jobName, nextJobName) {
  const start = workflow.indexOf(`\n  ${jobName}:`);
  const end = nextJobName ? workflow.indexOf(`\n  ${nextJobName}:`, start) : workflow.length;
  assert.notEqual(start, -1, `missing ${jobName} job`);
  if (nextJobName) assert.notEqual(end, -1, `missing ${nextJobName} job after ${jobName}`);
  return workflow.slice(start, end);
}

test('publisher jobs skip an already published tag without failing', () => {
  for (const [stream, nextJob, versionMessage] of [
    ['Go', 'publish-electron', 'bump VERSION before merging another Go release.'],
    ['Electron', 'publish-release-index', 'bump apps/electron/VERSION before merging another Electron release.'],
  ]) {
    const section = jobSection(`publish-${stream.toLowerCase()}`, nextJob);
    assert.match(section, /published: \$\{\{ steps\.create_release\.outcome == 'success' \}\}/);
    assert.match(section, /id: tag_check/);
    assert.match(section, /should_publish=false/);
    assert.match(section, new RegExp(versionMessage.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    assert.doesNotMatch(section, /::error::Tag/);
    assert.doesNotMatch(section, /\n\s+exit 1/);
    assert.equal(
      (section.match(/if: steps\.tag_check\.outputs\.should_publish == 'true'/g) ?? []).length,
      2,
      `${stream} manifest and release steps must both be guarded`,
    );
  }
});

test('release index only advances for publishers that created a release', () => {
  const section = jobSection('publish-release-index', '');
  assert.match(section, /needs\.publish-go\.outputs\.published == 'true'/);
  assert.match(section, /needs\.publish-electron\.outputs\.published == 'true'/);
  assert.match(section, /GO_CHANGED: \$\{\{ needs\.publish-go\.outputs\.published == 'true' \}\}/);
  assert.match(section, /ELECTRON_CHANGED: \$\{\{ needs\.publish-electron\.outputs\.published == 'true' \}\}/);
});
