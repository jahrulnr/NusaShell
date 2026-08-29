import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { syncTranscript } from './js/views/agent/subagents.js';

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
    const firstLine = panel.querySelector('.acp-transcript-line.is-thought');
    assert.ok(firstLine);
    assert.equal(firstLine.textContent.trim(), 'thinkThe');

    syncTranscript(panel, [{ kind: 'thought', text: 'The task description uses the correct paths.' }]);
    const updatedLine = panel.querySelector('.acp-transcript-line.is-thought');
    assert.strictEqual(updatedLine, firstLine, 'same logical chunk keeps its transcript row');
    assert.equal(updatedLine.textContent.trim(), 'thinkThe task description uses the correct paths.');
    assert.equal(panel.querySelectorAll('.acp-transcript-line.is-thought').length, 1);
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent transcript patches same-length text without replacing its row', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [{ kind: 'text', text: 'abc' }]);
    const firstLine = panel.querySelector('.acp-transcript-line.is-text');

    syncTranscript(panel, [{ kind: 'text', text: 'xyz' }]);

    const updatedLine = panel.querySelector('.acp-transcript-line.is-text');
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

    const thoughts = [...panel.querySelectorAll('.acp-transcript-line.is-thought')];
    assert.equal(thoughts.length, 2);
    assert.match(thoughts[0].textContent, /The task/);
    assert.match(thoughts[1].textContent, /Done checking\./);
    assert.equal(panel.querySelectorAll('.acp-transcript-line.is-usage').length, 0);

    const tools = [...panel.querySelectorAll('.acp-transcript-line.is-tool')];
    assert.equal(tools.length, 1);
    assert.match(tools[0].textContent, /completed/);
    assert.equal(panel.querySelector('.acp-usage-pill')?.textContent, '2/100');
  } finally {
    dom.window.close();
    cleanup();
  }
});
