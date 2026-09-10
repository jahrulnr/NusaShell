import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  codeLanguageInfo,
  codeModeFor,
  codeHljsClass,
  codeLanguageLabel,
  ensureCodeMirror,
} from '../js/code-language.js';

function makeDom() {
  const dom = new JSDOM('<!doctype html><html><body></body></html>');
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  return dom;
}

function cleanup() {
  delete globalThis.window;
  delete globalThis.document;
}

// ---------- single language map ----------

test('codeLanguageInfo resolves aliases to canonical language with mode, hljs, label', () => {
  assert.deepEqual(codeLanguageInfo('js'), { canonical: 'javascript', mode: 'text/javascript', hljs: 'javascript', label: 'JavaScript' });
  assert.deepEqual(codeLanguageInfo('mjs'), { canonical: 'javascript', mode: 'text/javascript', hljs: 'javascript', label: 'JavaScript' });
  assert.deepEqual(codeLanguageInfo('cjs'), { canonical: 'javascript', mode: 'text/javascript', hljs: 'javascript', label: 'JavaScript' });
  assert.deepEqual(codeLanguageInfo('ts'), { canonical: 'typescript', mode: 'text/typescript', hljs: 'javascript', label: 'TypeScript' });
  assert.deepEqual(codeLanguageInfo('tsx'), { canonical: 'typescript', mode: 'text/typescript', hljs: 'javascript', label: 'TypeScript' });
  assert.deepEqual(codeLanguageInfo('sh'), { canonical: 'shell', mode: 'text/x-sh', hljs: 'bash', label: 'Shell' });
  assert.deepEqual(codeLanguageInfo('bash'), { canonical: 'shell', mode: 'text/x-sh', hljs: 'bash', label: 'Shell' });
  assert.deepEqual(codeLanguageInfo('py'), { canonical: 'python', mode: 'text/x-python', hljs: 'python', label: 'Python' });
  assert.deepEqual(codeLanguageInfo('go'), { canonical: 'go', mode: 'text/x-go', hljs: 'go', label: 'Go' });
  assert.deepEqual(codeLanguageInfo('rs'), { canonical: 'rust', mode: 'text/x-rust', hljs: 'rust', label: 'Rust' });
  assert.deepEqual(codeLanguageInfo('sql'), { canonical: 'sql', mode: 'text/x-sql', hljs: 'sql', label: 'SQL' });
  assert.deepEqual(codeLanguageInfo('yml'), { canonical: 'yaml', mode: 'text/x-yaml', hljs: 'yaml', label: 'YAML' });
  assert.deepEqual(codeLanguageInfo('md'), { canonical: 'markdown', mode: 'text/x-markdown', hljs: 'markdown', label: 'Markdown' });
  assert.deepEqual(codeLanguageInfo('html'), { canonical: 'html', mode: 'htmlmixed', hljs: 'xml', label: 'HTML' });
  assert.deepEqual(codeLanguageInfo('svg'), { canonical: 'html', mode: 'htmlmixed', hljs: 'xml', label: 'HTML' });
  assert.deepEqual(codeLanguageInfo('css'), { canonical: 'css', mode: 'text/css', hljs: 'css', label: 'CSS' });
  assert.deepEqual(codeLanguageInfo('json'), { canonical: 'json', mode: 'application/json', hljs: 'json', label: 'JSON' });
});

test('codeLanguageInfo returns a plain-text fallback for unknown languages', () => {
  assert.deepEqual(codeLanguageInfo(''), { canonical: 'text', mode: 'text/plain', hljs: '', label: 'Plain text' });
  assert.deepEqual(codeLanguageInfo('unknown'), { canonical: 'unknown', mode: 'text/plain', hljs: '', label: 'Plain text' });
});

test('codeModeFor returns the CodeMirror mode string', () => {
  assert.equal(codeModeFor('js'), 'text/javascript');
  assert.equal(codeModeFor('ts'), 'text/typescript'); // CM5 javascript mode registers text/typescript
  assert.equal(codeModeFor('html'), 'htmlmixed');
  assert.equal(codeModeFor('sql'), 'text/x-sql');
  assert.equal(codeModeFor('unknown'), 'text/plain');
});

test('codeHljsClass returns the highlight.js class fragment', () => {
  assert.equal(codeHljsClass('js'), 'javascript');
  assert.equal(codeHljsClass('sh'), 'bash');
  assert.equal(codeHljsClass('html'), 'xml');
  assert.equal(codeHljsClass('unknown'), '');
});

test('codeLanguageLabel returns the human display label', () => {
  assert.equal(codeLanguageLabel('js'), 'JavaScript');
  assert.equal(codeLanguageLabel('ts'), 'TypeScript');
  assert.equal(codeLanguageLabel('sh'), 'Shell');
  assert.equal(codeLanguageLabel('unknown'), 'Plain text');
});

// ---------- ensureCodeMirror loader ----------

test('ensureCodeMirror resolves immediately when window.CodeMirror is already loaded', async () => {
  const dom = makeDom();
  const fakeCM = () => {};
  dom.window.CodeMirror = fakeCM;
  try {
    const cm = await ensureCodeMirror();
    assert.equal(cm, fakeCM, 'resolves with the existing global CodeMirror');
  } finally {
    cleanup();
  }
});

test('ensureCodeMirror injects the core script from /vendor/codemirror/ and rejects on failure', async () => {
  const dom = makeDom();
  const injected = [];
  const origAppend = dom.window.document.head.append;
  dom.window.document.head.append = function (...nodes) {
    for (const n of nodes) {
      if (n.tagName === 'SCRIPT' && n.src) injected.push(n.src);
      if (n.tagName === 'LINK' && n.href) injected.push(n.href);
    }
    return origAppend.apply(this, nodes);
  };
  // JSDOM does not fire script onload; simulate onerror on next microtask.
  const origCreate = dom.window.document.createElement.bind(dom.window.document);
  dom.window.document.createElement = (tag) => {
    const node = origCreate(tag);
    if (tag === 'script' || tag === 'link') {
      setTimeout(() => node.onerror?.(new Error('no network')), 0);
    }
    return node;
  };
  try {
    await assert.rejects(ensureCodeMirror(), /CodeMirror/, 'rejects when the script fails to load');
    assert.ok(injected.some((s) => s.includes('/vendor/codemirror/codemirror.min.js')),
      `core script injected from vendor path, got: ${injected.join(', ')}`);
  } finally {
    cleanup();
  }
});
