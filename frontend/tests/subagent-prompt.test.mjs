import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const root = new URL('../', import.meta.url);

const LONG_PATH = '/media/jahrulnr/storage/workspace/NusaShell/.experimental/widget-conversation/page/conversation/subagent-dock.js';

function longDelegation(tail = 'TAIL-SENTINEL') {
  return [
    '**Delegation brief**',
    '',
    'Review the responsive drawer and dock behaviour at narrow widths.',
    `- Touch ${LONG_PATH} and keep the prompt readable.`,
    '- Preserve the reader follow state while streaming deltas.',
    '- Keep Markdown, inline code, and editor actions working.',
    '',
    tail,
  ].join('\n');
}

function mountedDom() {
  const dom = new JSDOM('<section id="panel"></section>', { url: 'http://localhost/' });
  return { dom, panel: dom.window.document.querySelector('#panel') };
}

async function dockModule(tag) {
  return import(`../page/conversation/subagent-dock.js?prompt-card-${tag}=${Date.now()}`);
}

function objectiveCard(panel) {
  const card = panel.querySelector('.subagent-objective-card');
  return card && {
    card,
    label: card.querySelector('.subagent-prompt-label'),
    toggle: card.querySelector('.subagent-prompt-toggle'),
    body: card.querySelector('.subagent-prompt-body.subagent-objective'),
  };
}

test('dock objective renders as a bounded prompt card with an accessible expand control', async () => {
  const { mountSubagentPanel } = await dockModule('objective');
  const { panel } = mountedDom();
  mountSubagentPanel(panel, [{
    id: 'run-a', name: 'Devin', agentName: 'Devin', status: 'running', objective: longDelegation(), transcript: [],
  }]);

  const { card, label, toggle, body } = objectiveCard(panel);
  assert.equal(label.textContent, 'Objective', 'the card announces the prompt identity');
  assert.ok(toggle && toggle.hidden === false, 'a long prompt offers an expand control');
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');
  assert.equal(toggle.textContent, 'Show full prompt');
  assert.ok(body.id, 'the body is addressable by the toggle');
  assert.equal(toggle.getAttribute('aria-controls'), body.id);
  assert.equal(card.classList.contains('is-collapsed'), true, 'long prompts start in the preview bound');
  assert.ok(body.querySelector('strong'), 'Markdown rendering is preserved');
  assert.equal(body.textContent.includes('subagent-dock.js'), true, 'long paths render inside the card');

  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');
  assert.equal(toggle.textContent, 'Collapse');
  assert.equal(card.classList.contains('is-collapsed'), false);
  assert.equal(body.textContent.includes('TAIL-SENTINEL'), true, 'the full raw prompt is reachable after expanding');

  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'), 'false');
  assert.equal(card.classList.contains('is-collapsed'), true);
});

test('dock objective keeps short prompts unframed by a toggle', async () => {
  const { mountSubagentPanel } = await dockModule('short');
  const { panel } = mountedDom();
  mountSubagentPanel(panel, [{
    id: 'run-short', name: 'Devin', status: 'running', objective: 'Check narrow layouts.', transcript: [],
  }]);

  const { card, toggle, body } = objectiveCard(panel);
  assert.equal(card.classList.contains('is-collapsed'), false, 'short prompts never clamp');
  assert.equal(toggle.hidden, true, 'short prompts do not offer a dead control');
  assert.equal(body.textContent, 'Check narrow layouts.');
});

test('dock objective preserves expansion across deltas and restores it per run on switch', async () => {
  const { mountSubagentPanel } = await dockModule('runs');
  const { panel } = mountedDom();
  const runA = {
    id: 'run-a', name: 'Devin', status: 'running', objective: longDelegation('ALPHA-TAIL'),
    transcript: [{ kind: 'text', text: 'First live chunk.' }],
  };
  const runB = {
    id: 'run-b', name: 'Cursor', status: 'complete', objective: `${longDelegation('BRAVO-TAIL')} different wording`,
    transcript: [{ kind: 'text', text: 'Done.' }],
  };

  mountSubagentPanel(panel, [runA]);
  const { toggle } = objectiveCard(panel);
  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');

  mountSubagentPanel(panel, [{ ...runA, transcript: [
    { kind: 'text', text: 'First live chunk.' },
    { kind: 'text', text: 'Second live chunk.' },
  ] }]);
  let parts = objectiveCard(panel);
  assert.equal(parts.toggle.getAttribute('aria-expanded'), 'true', 'expansion survives a same-run delta');

  mountSubagentPanel(panel, [runA, runB]);
  panel.querySelector('[role="option"][data-subagent-id="run-b"]').click();
  parts = objectiveCard(panel);
  assert.equal(parts.body.textContent.includes('BRAVO-TAIL'), true, 'the switched run shows its own prompt');
  assert.equal(parts.body.textContent.includes('ALPHA-TAIL'), false);
  assert.equal(parts.toggle.getAttribute('aria-expanded'), 'false', 'a run never seen expanded starts collapsed');

  panel.querySelector('.subagent-trigger').click();
  panel.querySelector('[role="option"][data-subagent-id="run-a"]').click();
  parts = objectiveCard(panel);
  assert.equal(parts.body.textContent.includes('ALPHA-TAIL'), true);
  assert.equal(parts.toggle.getAttribute('aria-expanded'), 'true', 'switching back restores that run\'s expansion choice');
});

function transcriptPanel(dom) {
  return dom.window.document.querySelector('.acp-run-panel');
}

async function syncIn(dom, syncTranscript, panel, transcript, prompt = '', promptAt = '') {
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    syncTranscript(panel, transcript, prompt, promptAt);
  } finally {
    if (previousDocument === undefined) delete globalThis.document;
    else globalThis.document = previousDocument;
  }
}

test('delegation and steering prompts share one expandable card in the transcript', async () => {
  const { syncTranscript } = await import('../js/views/agent/subagents.js');
  const dom = new JSDOM('<div class="acp-run-panel"><div class="acp-transcript"></div></div>', { url: 'http://localhost/' });
  const panel = transcriptPanel(dom);
  const steer = '**Steer** — now also verify 863px and confirm ' + LONG_PATH;
  const transcript = [
    { kind: 'text', text: 'Working on it.' },
    { kind: 'prompt', text: steer },
    { kind: 'text', text: 'On the new pass.' },
  ];

  await syncIn(dom, syncTranscript, panel, transcript, longDelegation(), '2026-10-13T01:00:00Z');

  const prompts = panel.querySelectorAll('.acp-prompt-message');
  assert.equal(prompts.length, 2, 'initial delegation and steer are both prompt cards');
  const [delegation, steering] = prompts;
  assert.equal(delegation.querySelector('.subagent-prompt-label').textContent, 'Delegation');
  assert.equal(steering.querySelector('.subagent-prompt-label').textContent, 'Steering');

  const toggle = delegation.querySelector('.subagent-prompt-toggle');
  const body = delegation.querySelector('.subagent-prompt-body');
  assert.ok(body.classList.contains('agent-bubble-text'), 'the prompt body keeps the bubble text surface');
  assert.equal(toggle.getAttribute('aria-controls'), body.id);
  assert.equal(delegation.querySelector('.subagent-prompt').classList.contains('is-collapsed'), true);

  toggle.click();
  assert.equal(body.textContent.includes('TAIL-SENTINEL'), true);
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');

  const steerCard = steering.querySelector('.subagent-prompt');
  assert.equal(steerCard.classList.contains('is-collapsed'), false, 'the shorter steer fits without a toggle');
  assert.equal(steering.querySelector('.subagent-prompt-toggle').hidden, true);
  assert.equal(steering.querySelector('.subagent-prompt-body').textContent.includes('subagent-dock.js'), true);
});

test('repeated sync keeps prompt controls, expansion, and avoids re-rendering an unchanged card', async () => {
  const { syncTranscript } = await import('../js/views/agent/subagents.js');
  const dom = new JSDOM('<div class="acp-run-panel"><div class="acp-transcript"></div></div>', { url: 'http://localhost/' });
  const panel = transcriptPanel(dom);
  const initial = longDelegation();

  await syncIn(dom, syncTranscript, panel, [{ kind: 'text', text: 'First chunk.' }], initial, '2026-10-13T01:00:00Z');
  const message = panel.querySelector('.acp-prompt-message');
  const toggle = message.querySelector('.subagent-prompt-toggle');
  const body = message.querySelector('.subagent-prompt-body');
  const strong = body.querySelector('strong');
  toggle.click();
  assert.equal(toggle.getAttribute('aria-expanded'), 'true');

  await syncIn(dom, syncTranscript, panel, [
    { kind: 'text', text: 'First chunk.' },
    { kind: 'text', text: 'Second chunk.' },
  ], initial, '2026-10-13T01:00:00Z');

  const sameMessage = panel.querySelector('.acp-prompt-message');
  assert.equal(sameMessage, message, 'the prompt message node is patched, not replaced');
  assert.equal(sameMessage.querySelector('.subagent-prompt-toggle'), toggle, 'the expand control survives the refresh');
  assert.equal(sameMessage.querySelector('.subagent-prompt-body'), body);
  assert.equal(sameMessage.querySelector('.subagent-prompt-label').textContent, 'Delegation',
    'a refresh must not overwrite the card header with prompt markup');
  assert.equal(body.querySelector('strong'), strong, 'an unchanged prompt body is not re-rendered per chunk');
  assert.equal(toggle.getAttribute('aria-expanded'), 'true', 'expansion survives transcript refresh');
});

test('a changed prompt re-renders its body without clobbering the card controls', async () => {
  const { syncTranscript } = await import('../js/views/agent/subagents.js');
  const dom = new JSDOM('<div class="acp-run-panel"><div class="acp-transcript"></div></div>', { url: 'http://localhost/' });
  const panel = transcriptPanel(dom);

  await syncIn(dom, syncTranscript, panel, [{ kind: 'prompt', text: longDelegation('V1-TAIL'), at: '2026-10-13T01:01:00Z' }], '', '');
  const message = panel.querySelector('.acp-prompt-message');
  const toggle = message.querySelector('.subagent-prompt-toggle');
  toggle.click();

  await syncIn(dom, syncTranscript, panel, [{ kind: 'prompt', text: longDelegation('V2-TAIL'), at: '2026-10-13T01:02:00Z' }], '', '');

  assert.equal(message.querySelector('.subagent-prompt-toggle'), toggle, 'control survives a content update');
  assert.equal(message.querySelector('.subagent-prompt-label').textContent, 'Steering');
  assert.equal(message.querySelector('.subagent-prompt-body').textContent.includes('V2-TAIL'), true);
  assert.equal(message.querySelector('.subagent-prompt-body').textContent.includes('V1-TAIL'), false);
  assert.equal(toggle.getAttribute('aria-expanded'), 'true', 'an expanded card stays expanded across a text change');
});

test('dock transcript renders steer prompts with the same card chrome', async () => {
  const { mountSubagentPanel } = await dockModule('steer');
  const { panel } = mountedDom();
  mountSubagentPanel(panel, [{
    id: 'run-steer', name: 'Devin', status: 'running', objective: 'Initial objective.',
    transcript: [
      { kind: 'text', text: 'Working.' },
      { kind: 'prompt', text: longDelegation('STEER-TAIL') },
    ],
  }]);

  const prompt = panel.querySelector('.subagent-transcript .acp-prompt-message .subagent-prompt');
  assert.ok(prompt, 'steer prompts keep card chrome inside the dock transcript');
  assert.equal(prompt.querySelector('.subagent-prompt-label').textContent, 'Steering');
  const toggle = prompt.querySelector('.subagent-prompt-toggle');
  assert.equal(toggle.hidden, false);
  assert.equal(prompt.classList.contains('is-collapsed'), true);
  toggle.click();
  assert.equal(prompt.querySelector('.subagent-prompt-body').textContent.includes('STEER-TAIL'), true);
});

test('dock transcript never duplicates the objective as a second initial prompt', async () => {
  const { mountSubagentPanel } = await dockModule('dedupe');
  const { panel } = mountedDom();
  mountSubagentPanel(panel, [{
    id: 'run-dedupe', name: 'Devin', status: 'running', objective: 'Only show me once.',
    transcript: [{ kind: 'text', text: 'Streaming.' }],
  }]);

  assert.equal(panel.querySelectorAll('.subagent-objective-card').length, 1, 'the dock has one objective card');
  assert.equal(panel.querySelector('.subagent-transcript .acp-prompt-message'), null,
    'the transcript does not re-render the initial prompt the objective card already shows');
});

test('prompt card styles bound the preview and expanded scroll regions', async () => {
  const dockCSS = await readFile(new URL('dock.css', root), 'utf8');
  const acpCSS = await readFile(new URL('styles/acp.css', root), 'utf8');

  assert.match(dockCSS, /\.subagent-prompt-head\s*\{/);
  assert.match(dockCSS, /\.subagent-prompt-toggle\s*\{/);
  assert.match(dockCSS, /\.subagent-prompt\.is-collapsed[\s\S]*?max-height/);
  assert.match(dockCSS, /\.subagent-prompt-body[\s\S]*?overflow-wrap:\s*anywhere/);

  assert.match(acpCSS, /\.subagent-prompt-head\s*\{/);
  assert.match(acpCSS, /\.subagent-prompt-toggle\s*\{/);
  assert.match(acpCSS, /\.subagent-prompt\.is-collapsed[\s\S]*?max-height/);
  assert.match(acpCSS, /\.subagent-prompt-body[\s\S]*?overflow-y:\s*auto/);
});

test('prompt card controls stay readable and touch-sized in dock and drawer', async () => {
  const dockCSS = await readFile(new URL('dock.css', root), 'utf8');
  const acpCSS = await readFile(new URL('styles/acp.css', root), 'utf8');

  // Toggle: at least 11px type with a 32px minimum hit area, in both scopes.
  for (const [name, css] of [['dock', dockCSS], ['acp', acpCSS]]) {
    const toggle = css.match(/\.subagent-prompt-toggle\s*\{([^}]*)\}/)?.[1] || '';
    const fontSize = Number(toggle.match(/font:\s*(?:\S+\s+)*?(\d+(?:\.\d+)?)px/)?.[1]);
    assert.ok(fontSize >= 11, `${name} prompt toggle font ${fontSize}px is below the 11px readability floor`);
    assert.match(toggle, /min-height:\s*32px/, `${name} prompt toggle needs the 32px hit area`);
    const label = css.match(/\.subagent-prompt-label\s*\{([^}]*)\}/)?.[1] || '';
    const labelSize = Number(label.match(/font:\s*(?:\S+\s+)*?(\d+(?:\.\d+)?)px/)?.[1]);
    assert.ok(labelSize >= 10, `${name} prompt label font ${labelSize}px is below the 10px floor`);
  }
});
