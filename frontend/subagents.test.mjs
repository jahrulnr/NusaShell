import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { syncTranscript } from './js/views/agent/subagents.js';

const acpCSS = await readFile(new URL('./styles/acp.css', import.meta.url), 'utf8');

function makePanel() {
  const dom = new JSDOM('<!doctype html><html><body><div class="acp-run-panel"><div class="acp-run-meta"></div><div class="acp-transcript"></div></div></body></html>');
  global.window = dom.window;
  global.document = dom.window.document;
  return { dom, panel: document.querySelector('.acp-run-panel') };
}

function cleanup() {
  delete global.window;
  delete global.document;
}

test('Subagent transcript updates a growing chunk in place', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [{ kind: 'thought', text: 'The' }]);
    const firstLine = panel.querySelector('.agent-round.is-thought');
    assert.ok(firstLine);
    assert.equal(firstLine.querySelector('.agent-reasoning')._reasoningRaw, 'The');

    syncTranscript(panel, [{ kind: 'thought', text: 'The task description uses the correct paths.' }]);
    const updatedLine = panel.querySelector('.agent-round.is-thought');
    assert.strictEqual(updatedLine, firstLine, 'same logical chunk keeps its transcript row');
    assert.equal(updatedLine.querySelector('.agent-reasoning')._reasoningRaw, 'The task description uses the correct paths.');
    assert.equal(panel.querySelectorAll('.agent-round.is-thought').length, 1);
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent transcript patches same-length text without replacing its row', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [{ kind: 'text', text: 'abc' }]);
    const firstLine = panel.querySelector('.agent-round.is-text');

    syncTranscript(panel, [{ kind: 'text', text: 'xyz' }]);

    const updatedLine = panel.querySelector('.agent-round.is-text');
    assert.strictEqual(updatedLine, firstLine, 'same logical chunk keeps its transcript row');
    assert.equal(updatedLine.textContent.trim(), 'xyz');
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent transcript merges legacy token fragments and tool updates', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [
      { kind: 'thought', text: 'The' },
      { kind: 'usage', text: '1/100' },
      { kind: 'thought', text: ' task' },
      { kind: 'tool', tool_id: 'tool-1', tool_kind: 'read', tool_title: 'Read file', tool_status: 'in_progress' },
      { kind: 'tool', tool_id: 'tool-1', tool_kind: 'read', tool_title: 'Read file', tool_status: 'completed' },
      { kind: 'thought', text: 'Done' },
      { kind: 'usage', text: '2/100' },
      { kind: 'thought', text: ' checking.' },
    ]);

    const thoughts = [...panel.querySelectorAll('.agent-round.is-thought')];
    assert.equal(thoughts.length, 2);
    assert.match(thoughts[0].querySelector('.agent-reasoning')._reasoningRaw, /The task/);
    assert.match(thoughts[1].querySelector('.agent-reasoning')._reasoningRaw, /Done checking\./);
    assert.equal(panel.querySelectorAll('.agent-round.is-usage').length, 0);

    const tools = [...panel.querySelectorAll('.agent-round.is-tool')];
    assert.equal(tools.length, 1);
    assert.match(tools[0].textContent, /completed/);
    assert.equal(tools[0].querySelector('.agent-tool-terminal-action')?.textContent, 'Read file');
    assert.equal(tools[0].querySelector('.agent-tool-terminal-title')?.textContent, 'read');
    assert.equal(panel.querySelector('.acp-usage-pill')?.textContent, '2/100');
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent prompts render as user bubbles before and between assistant rounds', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [
      { kind: 'thought', text: 'planning' },
      { kind: 'prompt', text: 'steer from the parent' },
      { kind: 'text', text: 'continued work' },
    ], 'initial delegation brief');

    const userMessages = [...panel.querySelectorAll('.acp-transcript > .agent-message.user')];
    assert.equal(userMessages.length, 2);
    assert.equal(userMessages[0].querySelector('.agent-bubble').textContent, 'initial delegation brief');
    assert.equal(userMessages[1].querySelector('.agent-bubble').textContent, 'steer from the parent');
    assert.equal(userMessages[0].querySelector('.agent-bubble').classList.contains('acp-transcript'), false);
    assert.equal(panel.querySelectorAll('.acp-transcript > .agent-message.assistant').length, 2);
    assert.equal(panel.querySelectorAll('.agent-round.is-thought').length, 1);
    assert.equal(panel.querySelectorAll('.agent-round.is-text').length, 1);
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent delegation and steering prompts render the same Markdown user content', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [
      { kind: 'prompt', text: 'Inspect **the CSS** and `agent.css`.\n\n```css\n.agent-bubble {}\n```' },
    ]);

    const bubble = panel.querySelector('.acp-prompt-message .agent-bubble');
    assert.ok(bubble.querySelector('strong'), 'prompt emphasis is rendered as HTML');
    assert.ok(bubble.querySelector('code:not(pre code)'), 'prompt inline code is rendered as HTML');
    assert.ok(bubble.querySelector('pre code.language-css'), 'prompt fenced code uses the code-card renderer');
    assert.doesNotMatch(bubble.textContent, /```/, 'prompt fence markers are not shown');
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('ACP drawer keeps the mobile run picker compact and transcript horizontally contained', () => {
  const mobileRules = acpCSS.slice(acpCSS.indexOf('@media (max-width: 720px)'));
  assert.match(mobileRules, /grid-template-rows:\s*minmax\(0,\s*auto\)\s+minmax\(0,\s*1fr\)/);
  assert.match(mobileRules, /flex-direction:\s*row/);
  assert.match(mobileRules, /overflow-x:\s*auto/);
  assert.match(mobileRules, /overflow-x:\s*hidden/);
});
