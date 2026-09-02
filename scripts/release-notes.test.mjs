import assert from 'node:assert/strict';
import test from 'node:test';

import { extractReleaseNotes } from './release-notes.mjs';

test('extractReleaseNotes returns only the requested changelog section', () => {
  const changelog = '# Changelog\n\n## [2.4.6] - 2026-09-02\n\n### Fixed\n\n- Installer metadata.\n\n## [2.4.5] - 2026-09-01\n\n- Older.\n';
  assert.equal(extractReleaseNotes(changelog, 'v2.4.6'), '### Fixed\n\n- Installer metadata.');
});

test('extractReleaseNotes fails for missing or empty release sections', () => {
  assert.throws(() => extractReleaseNotes('## [2.4.5]\n\n- Older.', '2.4.6'), /no section/);
  assert.throws(() => extractReleaseNotes('## [2.4.6]\n\n## [2.4.5]', '2.4.6'), /section.*empty/);
});
