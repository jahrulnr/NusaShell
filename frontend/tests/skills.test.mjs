import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { createSkillsPage } from '../page/skills/app.js';
import { skillIsDeletable } from '../js/views/skills.js';
import {
  buildSkillFileTree,
  detectSkillFileMime,
  filterSkills,
  formatSkillFileSize,
  isBinarySkillFile,
  skillOwnerLabel,
  skillStatusTone,
  skillVersionLabel,
  stripSkillFrontMatter,
  versionOptions,
} from '../page/skills/model.js';

const root = new URL('../', import.meta.url);

function installDOMGlobals(window) {
  const names = ['window', 'document', 'MutationObserver', 'requestAnimationFrame', 'cancelAnimationFrame', 'Node', 'HTMLElement', 'HTMLInputElement', 'HTMLTextAreaElement', 'localStorage'];
  const previous = new Map();
  for (const name of names) {
    previous.set(name, Object.getOwnPropertyDescriptor(globalThis, name));
    Object.defineProperty(globalThis, name, { configurable: true, value: window[name] });
  }
  return () => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  };
}

test('experimental Skills page exposes its own route and capability sheet', async () => {
  const html = await readFile(new URL('index.html', root), 'utf8');
  const css = await readFile(new URL('skills.css', root), 'utf8');

  assert.match(html, /data-global-route="skills"[^>]*href="#skills"/);
  assert.match(html, /id="skills-view"[^>]*aria-label="Skills library"/);
  assert.doesNotMatch(html, /id="skill-editor-meta"/);
  assert.match(html, /id="skills-search"[^>]*type="search"/);
  assert.match(html, /id="skills-list"[^>]*role="list"/);
  assert.match(html, /id="skills-inspector"/);
  assert.match(html, /id="skill-files-tree"[^>]*role="list"/);
  assert.match(html, /id="skill-file-content"/);
  assert.match(html, /id="skills-install"/);
  assert.doesNotMatch(html, /class="skills-status-key"/);
  assert.doesNotMatch(html, /class="skills-page-header"/);
  assert.match(html, /id="skill-actions-menu"[^>]*role="menu"/);
  assert.match(css, /\.skill-sheet-header\s*\{/);
  assert.doesNotMatch(css, /\.skill-sheet-heading p\s*\{/);
  assert.match(css, /\.shell\.skills-active \.context-dock\s*\{[^}]*max-width:\s*0;/);
  assert.match(css, /\.skills-detail\[hidden\]/);
  assert.match(css, /\.skill-file-children\[hidden\]/);
  assert.match(css, /\.skills-list-empty\s*\{[\s\S]*display:\s*grid;[\s\S]*gap:/);
  assert.match(css, /\.shell\.skills-active/);
  assert.match(css, /@media \(max-width: 720px\)/);
});

test('Skills model keeps search, provenance, lifecycle, and file metadata readable', () => {
  const skills = [
    { id: 'git-review', name: 'Git review', description: 'Review pull requests', category: 'engineering', owned_by: 'builtin', status: 'trusted', version: 3, active_version: 3 },
    { id: 'learned-brief', name: 'Brief writer', description: 'Draft concise briefs', category: 'writing', owned_by: 'learned', status: 'experimental', version: 2, active_version: 1 },
  ];

  assert.deepEqual(filterSkills(skills, 'pull requests'), [skills[0]]);
  assert.deepEqual(filterSkills(skills, 'LEARNED'), [skills[1]]);
  assert.equal(skillOwnerLabel(skills[0]), 'Built-in');
  assert.equal(skillOwnerLabel({ owned_by: 'plugin:notes' }), 'Plugin · notes');
  assert.equal(skillStatusTone(skills[1]), 'experimental');
  assert.equal(skillVersionLabel(skills[1]), 'v1 · latest v2');
  assert.deepEqual(versionOptions({ version: 3, active_version: 2, history: [{ version: 1 }, { version: 2 }] }), [
    { text: 'v2', value: '2' },
    { text: 'v1', value: '1' },
  ]);
  assert.equal(formatSkillFileSize(1536), '1.5 KB');
  assert.equal(detectSkillFileMime('README'), 'text/plain');
  assert.equal(detectSkillFileMime('scripts/render.py'), 'text/x-python');
  assert.equal(detectSkillFileMime('archive.tar.gz'), 'application/gzip');
  assert.equal(isBinarySkillFile('archive.zip', 'not rendered'), true);
  assert.equal(isBinarySkillFile('notes.txt', 'plain text'), false);
  assert.equal(isBinarySkillFile('notes.txt', 'binary\u0000payload'), true);
  assert.equal(stripSkillFrontMatter('---\nname: git-review\ndescription: Review\n---\n\n# Instructions'), '# Instructions');
  const tree = buildSkillFileTree([
    { path: 'SKILL.md', type: 'file', sizeBytes: 1 },
    { path: 'references', type: 'directory' },
    { path: 'references/guides', type: 'directory' },
    { path: 'references/guides/flow.md', type: 'file', sizeBytes: 2 },
  ]);
  assert.deepEqual(tree.map((node) => node.name), ['references', 'SKILL.md']);
  assert.equal(tree[0].children[0].children[0].path, 'references/guides/flow.md');
});

test('Skills page renders a selected package and opens its source through the existing RPCs', async () => {
  const dom = new JSDOM(`
    <section id="skills">
      <span id="skills-sync-state"></span><span id="skills-count"></span><span id="skills-index-count"></span><span id="skills-results"></span>
      <input id="skills-search"><button id="skills-install"></button><input id="skill-file-input">
      <div id="skills-list" role="list"></div>
      <div id="skills-empty"><h2></h2><p></p></div>
      <div id="skills-detail" hidden>
        <span id="skill-mark-letter"></span><span id="skill-detail-owner"></span><span id="skill-detail-category"></span>
        <h2 id="skill-editor-title"></h2>
        <div id="skill-lifecycle" hidden><span id="skill-status-badge"></span><span id="skill-version-meta"></span><button id="skill-actions-trigger" type="button" aria-expanded="false"></button><div id="skill-actions-menu" hidden></div><button id="skills-promote"></button><button id="skills-rollback"></button><button id="skills-delete"></button></div>
        <strong id="skill-owner-stat"></strong><strong id="skill-usage-stat"></strong><strong id="skill-last-used-stat"></strong><strong id="skill-file-count"></strong>
        <div id="skill-tree-meta"></div><div id="skill-files-tree" role="list"></div><div id="skill-tree-empty" hidden></div>
        <section id="skill-file-viewer" hidden><code id="skill-file-path"></code><span id="skill-file-size"></span><article id="skill-file-content"></article></section>
        <section id="skill-editor-empty"><strong></strong><span></span></section>
      </div>
    </section>`, { url: 'http://localhost/' });
  const restore = installDOMGlobals(dom.window);
  const calls = [];
  const skill = {
    id: 'git-review', name: 'Git review', description: 'Review a pull request.', category: 'engineering',
    status: 'trusted', version: 1, active_version: 1, owned_by: 'builtin', usage_count: 2,
    files: [
      { path: 'SKILL.md', type: 'file', sizeBytes: 12, editable: false },
      { path: 'scripts/render.py', type: 'file', sizeBytes: 24, editable: false },
      { path: 'archive.zip', type: 'file', sizeBytes: 24, editable: false },
    ],
  };
  const rpcClient = async (method, payload) => {
    calls.push({ method, payload });
    if (method === 'skills.list') return { skills: [skill] };
    if (method === 'skills.read') return { skill };
    if (method === 'skills.file.read' && payload.path === 'SKILL.md') return { content: '---\nname: git-review\ndescription: Review a pull request.\n---\n\n# Git review', sizeBytes: 12, truncated: false };
    if (method === 'skills.file.read' && payload.path === 'scripts/render.py') return { content: 'print("hello")\n', sizeBytes: 15, truncated: false };
    if (method === 'skills.file.read' && payload.path === 'archive.zip') return { content: '\u0000\u0001\u0002', sizeBytes: 24, truncated: false };
    throw new Error(`unexpected RPC ${method}`);
  };
  try {
    const page = createSkillsPage(dom.window.document.querySelector('#skills'), { rpcClient, onEvent: () => () => {} });
    page.init();
    await page.refresh();
    await page.selectSkill('git-review', 'builtin');
    await page.openSkillFile('SKILL.md');

    assert.equal(dom.window.document.querySelectorAll('#skills-list .skills-index-item').length, 1);
    assert.equal(dom.window.document.querySelector('#skill-editor-title').textContent, 'Git review');
    assert.equal(dom.window.document.querySelector('#skill-file-viewer').hidden, false);
    assert.equal(dom.window.document.querySelector('#skill-file-path').textContent, 'SKILL.md');
    assert.match(dom.window.document.querySelector('#skill-file-size').textContent, /text\/markdown/);
    assert.equal(dom.window.document.querySelector('#skill-file-content h1').textContent, 'Git review');
    assert.doesNotMatch(dom.window.document.querySelector('#skill-file-content').textContent, /name: git-review/);

    await page.openSkillFile('scripts/render.py');
    assert.equal(dom.window.document.querySelector('#skill-file-content pre').textContent, 'print("hello")\n');
    assert.match(dom.window.document.querySelector('#skill-file-size').textContent, /text\/x-python/);

    await page.openSkillFile('archive.zip');
    assert.match(dom.window.document.querySelector('#skill-file-content').textContent, /Preview not available/);
    assert.equal(dom.window.document.querySelector('#skill-file-content pre'), null);
    const actions = dom.window.document.querySelector('#skill-actions-trigger');
    actions.click();
    assert.equal(dom.window.document.querySelector('#skill-actions-menu').hidden, false);
    dom.window.document.dispatchEvent(new dom.window.Event('pointerdown', { bubbles: true }));
    assert.equal(dom.window.document.querySelector('#skill-actions-menu').hidden, true);
    assert.deepEqual(calls.map(({ method }) => method), ['skills.list', 'skills.read', 'skills.file.read', 'skills.file.read', 'skills.file.read']);
    assert.deepEqual(calls[2].payload, { id: 'git-review', owned_by: 'builtin', path: 'SKILL.md' });
  } finally {
    restore();
  }
});

// The prototype reuses the production helper, with delete eligibility aligned
// to the backend-supported builtin, learned, and user owners.
test('skillIsDeletable allows builtin, learned, and user skills but not plugin or unknown owners', () => {
  assert.equal(skillIsDeletable({ owned_by: 'learned', status: 'experimental' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'learned', status: 'trusted' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'user', origin: 'user' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'builtin' }), true);
  assert.equal(skillIsDeletable({ origin: 'builtin' }), true);
  assert.equal(skillIsDeletable({ owned_by: 'plugin:notes' }), false);
  assert.equal(skillIsDeletable({ owned_by: 'other' }), false);
  assert.equal(skillIsDeletable({ origin: 'learned' }), true);
  assert.equal(skillIsDeletable({}), false);
});
