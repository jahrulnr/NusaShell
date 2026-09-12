import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, mkdir, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const execFileAsync = promisify(execFile);
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const validator = path.join(
  repoRoot,
  'resources/agent/skills/skill-creator/scripts/quick_validate.py',
);

async function runValidator(args) {
  try {
    const result = await execFileAsync('python3', [validator, ...args], {
      cwd: repoRoot,
      encoding: 'utf8',
    });
    return { status: 0, stdout: result.stdout, stderr: result.stderr };
  } catch (error) {
    return {
      status: typeof error.code === 'number' ? error.code : 1,
      stdout: error.stdout ?? '',
      stderr: error.stderr ?? '',
    };
  }
}

async function makeSkill(name, skillText, supportFiles = {}) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'nusashell-skill-validator-'));
  const skillDir = path.join(root, name);
  await mkdir(skillDir, { recursive: true });
  await writeFile(path.join(skillDir, 'SKILL.md'), skillText, 'utf8');
  for (const [relativePath, content] of Object.entries(supportFiles)) {
    const destination = path.join(skillDir, relativePath);
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, content, 'utf8');
  }
  return { root, skillDir };
}

const validSkill = `---
name: report-writer
description: Draft concise reports from supplied evidence. Use when the user asks for a report or findings summary.
compatibility: Requires the NusaShell file and terminal capabilities when the workflow needs them.
requirements:
  mcp:
    - role:files
    - role:terminal
metadata:
  version: "1"
---

# Report writer

## Purpose

Turn supplied evidence into a concise, traceable report.

## Workflow

1. Inspect the supplied evidence and separate facts from assumptions.
2. Draft the report using the requested audience and format.
3. Verify every claim against the evidence before delivering it.

See [the report checklist](references/report-checklist.md).
`;

test('validates a NusaShell package, nested requirements, and local links', async () => {
  const { root, skillDir } = await makeSkill('report-writer', validSkill, {
    'references/report-checklist.md': '# Report checklist\n\nCheck each claim.\n',
    'scripts/unused-helper.py': '# Reference-only helper\n',
  });
  try {
    const result = await runValidator(['--check-links', skillDir]);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    assert.match(result.stdout, /PASS/);
    assert.match(result.stdout, /0 errors/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('keeps TODO examples in fenced code but rejects unfinished TODO prose', async () => {
  const fencedTodo = ['```markdown', '[TODO: this is an example shown to the reader]', '```'].join(String.fromCharCode(10));
  const { root, skillDir } = await makeSkill('todo-skill', `---
name: todo-skill
description: Complete a small task. Use when the user asks for this task.
---

# Todo skill

${fencedTodo}

[TODO: this instruction is unfinished]
`);
  try {
    const result = await runValidator([skillDir]);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout, /TODO placeholder/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('rejects a malformed package name or frontmatter name mismatch', async () => {
  const { root, skillDir } = await makeSkill('Bad-Skill', `---
name: another-skill
description: This description is valid enough. Use when the matching task is requested.
---

# Body

Follow the requested workflow.
`);
  try {
    const result = await runValidator([skillDir]);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout, /lowercase|does not match directory/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('reports multiple packages as JSON and treats Codex-only allowed-tools as a warning', async () => {
  const first = await makeSkill('first-skill', `---
name: first-skill
description: Perform the first task. Use when the user requests it.
---

# First

Perform the task.
`);
  const second = await makeSkill('second-skill', `---
name: second-skill
description: Perform the second task. Use when the user requests it.
allowed-tools: [file_read]
---

# Second

Perform the task.
`);
  try {
    const result = await runValidator(['--json', first.skillDir, second.skillDir]);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    const reports = JSON.parse(result.stdout);
    assert.equal(reports.length, 2);
    assert.equal(reports[0].valid, true);
    assert.equal(reports[1].valid, true);
    assert.ok(reports[1].warnings.some((issue) => issue.code === 'runtime-field'));
  } finally {
    await Promise.all([
      rm(first.root, { recursive: true, force: true }),
      rm(second.root, { recursive: true, force: true }),
    ]);
  }
});

test('fails the optional link check for a missing local target', async () => {
  const { root, skillDir } = await makeSkill('broken-links', `---
name: broken-links
description: Check local links. Use when the user asks for a link check.
---

# Broken links

See [the missing guide](references/missing.md).
`);
  try {
    const result = await runValidator(['--check-links', skillDir]);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout, /link-missing/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('strict mode turns a compatibility warning into a failure', async () => {
  const { root, skillDir } = await makeSkill('strict-skill', `---
name: strict-skill
description: Check strict package quality. Use when the user requests it.
allowed-tools: [file_read]
---

# Strict skill

Perform the task.
`);
  try {
    const result = await runValidator(['--strict', skillDir]);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout, /runtime-field/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('rejects symlinked package files', async (t) => {
  const { root, skillDir } = await makeSkill('symlinked-skill', validSkill);
  const realTarget = path.join(root, 'real-skill.md');
  try {
    await writeFile(realTarget, validSkill, 'utf8');
    await rm(path.join(skillDir, 'SKILL.md'), { force: true });
    try {
      await symlink(realTarget, path.join(skillDir, 'SKILL.md'));
    } catch (error) {
      if (['EACCES', 'ENOTSUP', 'EPERM'].includes(error.code)) {
        t.skip('symlink creation is not available on this host');
        return;
      }
      throw error;
    }
    const result = await runValidator([skillDir]);
    assert.notEqual(result.status, 0);
    assert.match(result.stdout, /symlink/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
