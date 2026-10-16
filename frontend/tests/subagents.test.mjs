import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import {
  openDrawer,
  sortRunsNewestFirst,
  syncTranscript,
  runDisplayName,
  runStatusText,
  getSubagentFollow,
  applySubagentFollowIntent,
  bindTranscriptFollow,
  resetSubagentFollowForTests,
} from '../js/views/agent/subagents.js';

const acpCSS = await readFile(new URL('../styles/acp.css', import.meta.url), 'utf8');
const subagentsView = await readFile(new URL('../js/views/agent/subagents.js', import.meta.url), 'utf8');

test('runDisplayName prefers title over agent_name', () => {
  assert.equal(runDisplayName({ title: 'Inspect pets', agent_name: 'Codex' }), 'Inspect pets');
  assert.equal(runDisplayName({ title: '  ', agent_name: 'Codex' }), 'Codex');
  assert.equal(runDisplayName({ agent_name: 'Codex' }), 'Codex');
  assert.equal(runDisplayName({}), 'ACP');
});

test('live subagent status shows activity and elapsed time', () => {
  const started = Date.parse('2026-09-16T20:00:00Z');
  assert.equal(
    runStatusText({ status: 'running', activity: 'thinking', started_at: '2026-09-16T20:00:00Z' }, started + 65_000),
    'running · thinking · 1m 5s',
  );
  assert.equal(
    runStatusText({ status: 'completed', activity: 'thinking', started_at: '2026-09-16T20:00:00Z' }, started + 65_000),
    'done',
  );
  assert.match(
    runStatusText({ status: 'running', activity: 'thinking', started_at: '2026-09-16T20:00:00Z', updated_at: '2026-09-16T20:00:00Z' }, started + 65_000),
    /last event 1m 5s ago/,
  );
});

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

test('ACP runs are ordered newest first, including legacy records without timestamps', () => {
  const ordered = sortRunsNewestFirst([
    { id: 'old', started_at: '2026-08-30T08:00:00Z' },
    { id: 'new', started_at: '2026-08-30T08:02:00Z' },
    { id: 'legacy-a' },
    { id: 'legacy-b' },
  ]);
  assert.deepEqual(
    ordered.map((run) => run.id),
    ['new', 'old', 'legacy-b', 'legacy-a'],
  );
});

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
    assert.equal(tools[0].querySelector('.agent-tool-event-title')?.textContent, 'Read file');
    assert.ok(tools[0].querySelector('.agent-tool-event-summary-text'), 'summary line renders');
    assert.ok(tools[0].querySelector('.agent-tool-event-details'), 'raw request/output fold renders');
    assert.equal(panel.querySelector('.acp-usage-pill')?.textContent, '2/100');
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent tool rows carry agent-provided input and output', () => {
  const { dom, panel } = makePanel();
  try {
    const input = '{\n  "path": "/home/u/.agents/skills/fullstack-guardian/SKILL.md"\n}';
    syncTranscript(panel, [
      {
        kind: 'tool', tool_id: 'tool-io', tool_kind: 'read', tool_title: 'Read SKILL.md',
        tool_status: 'in_progress', tool_input: input,
      },
    ]);
    const rows = [...panel.querySelectorAll('.agent-round.is-tool')];
    assert.equal(rows.length, 1);
    assert.equal(rows[0].querySelector('.agent-tool-event-title')?.textContent, 'Read SKILL.md');
    assert.match(rows[0].querySelector('.agent-tool-event-path')?.textContent || '', /fullstack-guardian\/SKILL\.md/);

    syncTranscript(panel, [
      {
        kind: 'tool', tool_id: 'tool-io', tool_kind: 'read', tool_title: 'Read SKILL.md',
        tool_status: 'completed', tool_input: input, tool_output: 'name: fullstack-guardian\n# Fullstack Guardian',
      },
    ]);
    const updated = panel.querySelector('.agent-round.is-tool');
    assert.strictEqual(updated, rows[0], 'same tool row patched in place');
    assert.equal(updated.querySelector('.agent-tool-event-title')?.textContent, 'Read SKILL.md');
    assert.match(updated.querySelector('.agent-tool-event-output')?.textContent || '', /Fullstack Guardian/);
    assert.match(updated.textContent, /completed/);
  } finally {
    dom.window.close();
    cleanup();
  }
});

test('Subagent prompts render as isolated prompt events before and between assistant rounds', () => {
  const { dom, panel } = makePanel();
  try {
    syncTranscript(panel, [
      { kind: 'thought', text: 'planning' },
      { kind: 'prompt', text: 'steer from the parent' },
      { kind: 'text', text: 'continued work' },
    ], 'initial delegation brief');

    const promptMessages = [...panel.querySelectorAll('.acp-transcript > .acp-prompt-message')];
    assert.equal(promptMessages.length, 2);
    assert.equal(promptMessages[0].querySelector('.subagent-prompt-body').textContent.trim(), 'initial delegation brief');
    assert.equal(promptMessages[1].querySelector('.subagent-prompt-body').textContent.trim(), 'steer from the parent');
    assert.equal(promptMessages[0].classList.contains('user'), false, 'ACP prompt must not inherit the main user bubble');
    assert.equal(promptMessages[0].querySelector('.agent-bubble').classList.contains('acp-transcript'), false);
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

test('ACP prompt styling is bounded and scoped away from the main user bubble', () => {
  assert.match(acpCSS, /\.acp-run-panel \.acp-prompt-message \{[^}]*align-self:\s*stretch;/s);
  assert.match(acpCSS, /\.acp-run-panel \.subagent-prompt:not\(\.is-collapsed\) > \.subagent-prompt-body \{[^}]*max-height:\s*min\(/s);
  assert.match(acpCSS, /\.acp-run-panel \.subagent-prompt:not\(\.is-collapsed\) > \.subagent-prompt-body \{[^}]*overflow-y:\s*auto;/s);
  assert.match(acpCSS, /\.acp-popup\s*\{[^}]*max-height:\s*min\(86vh,\s*720px\);[^}]*overflow:\s*auto;/s);
});

test('subagent follow pin is per run id, independent across rooms', () => {
  resetSubagentFollowForTests();
  getSubagentFollow('run-a').pinned = false;
  assert.equal(getSubagentFollow('run-a').pinned, false);
  assert.equal(getSubagentFollow('run-b').pinned, true, 'other run stays pinned');
});

test('subagent upward scroll intent unpins only that run', () => {
  resetSubagentFollowForTests();
  const scroller = {
    scrollTop: 0,
    scrollHeight: 2000,
    clientHeight: 400,
  };
  applySubagentFollowIntent('run-a', scroller, 'up');
  assert.equal(getSubagentFollow('run-a').pinned, false);
  assert.equal(getSubagentFollow('run-b').pinned, true);
  // Returning to the bottom re-arms that run only.
  scroller.scrollTop = 1600;
  applySubagentFollowIntent('run-a', scroller, 'down');
  assert.equal(getSubagentFollow('run-a').pinned, true);
  assert.equal(getSubagentFollow('run-b').pinned, true);
});

test('subagent follow store is keyed by run id, not a shared scroller WeakMap', () => {
  assert.match(subagentsView, /followByRunId\s*=\s*new Map/);
  assert.doesNotMatch(subagentsView, /followStates\s*=\s*new WeakMap/);
  assert.match(subagentsView, /applySubagentFollowIntent\(/);
  assert.match(subagentsView, /getSubagentFollow\(runId\)/);
  // Direction-aware unpin — geometry-only scroll cannot detach, so every real
  // input path must carry intent: wheel, touch drag (mobile has no wheel),
  // keyboard, and an active scrollbar drag.
  assert.match(subagentsView, /addEventListener\('wheel'/);
  assert.match(subagentsView, /addEventListener\('touchmove'/);
  assert.match(subagentsView, /addEventListener\('keydown'/);
  assert.match(subagentsView, /pointerdown[\s\S]*?scrollbarDragging\s*=\s*true/);
  assert.match(subagentsView, /applySubagentFollowIntent\(runId, scroller, direction\)/);
  assert.match(subagentsView, /updateScrollPin\(getSubagentFollow\(runId\), scroller, 24, \{ direction:/);
});

test('subagent transcript releases the run pin on an upward touch scroll', () => {
  resetSubagentFollowForTests();
  const dom = new JSDOM(
    '<!doctype html><html><body><div class="acp-popup" id="acp-popup">' +
    '<div class="acp-run-panel" data-run-id="run-a"><div class="agent-thread acp-transcript"></div></div>' +
    '</div></body></html>',
  );
  global.window = dom.window;
  global.document = dom.window.document;
  try {
    const scroller = document.querySelector('#acp-popup');
    bindTranscriptFollow(document.querySelector('.acp-run-panel'));
    assert.equal(getSubagentFollow('run-a').pinned, true);
    const touch = (type, y) => {
      const event = new dom.window.Event(type, { bubbles: true });
      event.touches = [{ clientY: y }];
      scroller.dispatchEvent(event);
    };
    touch('touchstart', 200);
    touch('touchmove', 320); // finger drags down = reading older output
    assert.equal(getSubagentFollow('run-a').pinned, false, 'upward read must release the pin');
    touch('touchend', 320);
    touch('touchstart', 320);
    touch('touchmove', 40); // swipe up = back toward the live tail
    assert.equal(getSubagentFollow('run-a').pinned, true, 'returning to the bottom re-arms follow');
  } finally {
    cleanup();
    resetSubagentFollowForTests();
  }
});

const POPUP_HTML = `<!doctype html><html><body>
  <section id="panel-subagents"></section>
  <div class="acp-popup-overlay" id="acp-popup-overlay" hidden>
    <div class="acp-popup" id="acp-popup" role="dialog" aria-modal="true" aria-labelledby="acp-popup-title">
      <h2 id="acp-popup-title">Subagent</h2>
      <button id="acp-popup-close" type="button">Close</button>
      <div id="acp-popup-body"></div>
    </div>
  </div>
</body></html>`;

test('openDrawer opens the popup reader for the requested run', async () => {
  const dom = new JSDOM(POPUP_HTML, { url: 'http://localhost/' });
  global.window = dom.window;
  global.document = dom.window.document;
  global.HTMLElement = dom.window.HTMLElement;
  const previousFetch = globalThis.fetch;
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ ok: true, result: { run: {
    id: 'acprun_popup', agent_name: 'Devin', status: 'completed', prompt: 'scan', transcript: [{ kind: 'text', text: 'done' }],
  } } }) });
  try {
    openDrawer('acprun_popup');
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(document.getElementById('acp-popup-overlay').hidden, false);
    assert.equal(document.getElementById('acp-popup-title').textContent, 'Devin');
    assert.match(document.getElementById('acp-popup-body').textContent, /done/);
    document.getElementById('acp-popup-close').click();
    assert.equal(document.getElementById('acp-popup-overlay').hidden, true);
  } finally {
    globalThis.fetch = previousFetch;
    dom.window.close();
    cleanup();
  }
});
