import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderConversation, renderEmptyThread, renderToolJob, renderToolCallCard, setToolTerminalPresentation, appendToolJobDelta, applyQueuedToolDeltas, appendLiveError, bindToolStop, renderMessageAttachments, renderToolAttachments, parseShowAudioOutput, parseShowVideoOutput, STARTER_PROMPTS, reasoningDisclosure, renderCompactionStatus, mountLiveRound, sealLiveNodeBeforeSteer, insertAfterOrAppend, bindOptimisticTurn, thinkingDots, setThinkingDots, reasoningShouldStream, setReasoningStreaming, sealReasoningStreaming, captureDisclosureState, restoreDisclosureState } from '../js/views/agent/render.js';
import { normalizeToolCall, registerToolContracts, toolContractFor, toolContractClass } from '../js/views/agent/tool-contracts.js';
function renderTranscript(messages) {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    thread.append(renderConversation(messages));
    return thread;
  } finally {
    globalThis.document = previousDocument;
  }
}

test('user bubbles render Markdown and fenced code as readable HTML cards', () => {
  const thread = renderTranscript([{
    role: 'user',
    content: 'Use **the config** and `settings.css`.\n\n```css\nbody { color: red; }\n```',
    created_at: '2026-08-30T00:00:00Z',
  }]);
  const bubble = thread.querySelector('.agent-message.user .agent-bubble');
  assert.ok(bubble, 'user bubble is rendered');
  assert.ok(bubble.querySelector('.agent-bubble-text'), 'user content has a renderable content wrapper');
  assert.ok(bubble.querySelector('strong'), 'Markdown emphasis is rendered as HTML');
  assert.ok(bubble.querySelector('code:not(pre code)'), 'inline backticks render as an inline code span');
  assert.ok(bubble.querySelector('pre code.language-css'), 'fenced code renders as a language-aware code card');
  assert.doesNotMatch(bubble.textContent, /```/, 'fence markers are not shown to the user');
});

test('user bubbles keep soft line breaks and collapse blank Markdown lines', () => {
  const thread = renderTranscript([{
    role: 'user',
    content: 'first line\nsecond line\n\nthird line',
    created_at: '2026-08-30T00:00:00Z',
  }]);
  const content = thread.querySelector('.agent-message.user .agent-bubble-text');
  assert.ok(content, 'user Markdown content is rendered inside its wrapper');
  assert.deepEqual(
    [...content.children].map((block) => block.tagName),
    ['P', 'P'],
    'a blank Markdown line creates blocks, not an empty visible paragraph',
  );
  assert.equal(content.children[0].textContent, 'first line\nsecond line');
  assert.equal(content.children[1].textContent, 'third line');
});

// exec tool output persisted on the conversation is rendered verbatim when
// the thread is reloaded from the snapshot (no live run in memory), so users
// see the streamed output after a refresh / room switch.
test('exec tool output is rendered from the persisted conversation', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run a long command', created_at: '2026-08-25T00:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-25T00:00:01Z',
      steps: [
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'exec', args: { command: 'sleep 5' }, status: 'ok',
            output: 'exit_code: 0\nduration_ms: 5000\n---\nPING reply 1\nPING reply 2\n' },
        ] },
      ],
    },
  ]);
  const terminal = thread.querySelector('.agent-tool-event.is-terminal');
  assert.ok(terminal, 'exec card rendered from snapshot');
  assert.equal(terminal.classList.contains('is-success'), true, 'card is success');
  const out = terminal.querySelector('.agent-tool-terminal-output');
  assert.match(out.textContent, /PING reply 1/);
  assert.match(out.textContent, /PING reply 2/);
  // The streaming-only Stop button must not survive the reload.
  assert.equal(terminal.querySelector('.agent-tool-stop').hidden, true);
});

// exec tool with interrupted status keeps the persisted partial output so
// users see the streamed lines after a reload, not just "interrupted by user".
test('exec tool with interrupted status renders persisted partial output', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Cancel a long command', created_at: '2026-08-25T00:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-25T00:00:01Z',
      steps: [
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'exec', args: { command: 'sleep 60' }, status: 'interrupted',
            output: 'error: exec cancelled: context canceled\npartial output:\nfirst chunk\nsecond chunk\n' },
        ] },
      ],
    },
  ]);
  const terminal = thread.querySelector('.agent-tool-event.is-terminal');
  assert.equal(terminal.classList.contains('is-error'), true, 'interrupted status flagged as error styling');
  const out = terminal.querySelector('.agent-tool-terminal-output');
  assert.match(out.textContent, /first chunk/);
  assert.match(out.textContent, /second chunk/);
});

test('compaction summary renders above retained chronological turns, not after them', () => {
  const thread = renderTranscript([
    { role: 'user', content: '[COMPACTION CHECKPOINT]\nGoal: fix ordering. Done: found Compact regrouped users then assistants.', created_at: '2026-08-27T15:50:00Z' },
    { role: 'user', content: 'keep going', created_at: '2026-08-27T15:51:00Z' },
    { role: 'assistant', content: 'live delta continues here', created_at: '2026-08-27T15:52:00Z' },
  ]);
  const nodes = [...thread.children];
  assert.equal(nodes[0].classList.contains('agent-compaction-marker'), true, 'handover is the first live bubble');
  const handover = nodes[0].querySelector('.agent-reasoning');
  assert.ok(handover, 'handover reuses the Thinking disclosure');
  assert.equal(handover.querySelector('.agent-reasoning-title').textContent, 'Compacted context');
  assert.equal(handover.open, false, 'handover starts collapsed');
  assert.doesNotMatch(handover.textContent, /Goal: fix ordering/);
  openDetails(handover);
  assert.match(handover.textContent, /Goal: fix ordering/);
  assert.equal(nodes[1].classList.contains('user'), true);
  assert.match(nodes[1].textContent, /keep going/);
  assert.equal(nodes[2].classList.contains('assistant'), true);
  assert.match(nodes[2].textContent, /live delta continues here/);
});

test('compaction status is an accessible animated inline status', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const status = renderCompactionStatus();
    document.body.append(status);
    assert.equal(status.classList.contains('agent-compaction-status'), true);
    assert.equal(status.getAttribute('role'), 'status');
    assert.equal(status.getAttribute('aria-live'), 'polite');
    assert.match(status.textContent, /Context automatically compacting/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('compaction status replaces generic loading dots instead of showing both', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.createElement('div');
    const textBox = document.createElement('div');
    textBox.append(thinkingDots());
    const status = renderCompactionStatus();
    bubble.append(textBox, status);

    setThinkingDots(textBox, false);

    assert.equal(bubble.querySelectorAll('.agent-thinking-dots').length, 0);
    assert.equal(bubble.querySelectorAll('.agent-compaction-status').length, 1);

    // When compaction ends, the same waiting slot may be restored idempotently
    // if the provider has not emitted its first token yet.
    setThinkingDots(textBox, true);
    setThinkingDots(textBox, true);
    assert.equal(bubble.querySelectorAll('.agent-thinking-dots').length, 1);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('renders one model and usage summary for all assistant rounds in a user turn', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run the checks', created_at: '2026-08-13T18:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-13T18:00:01Z',
      usage: { input_tokens: 100, output_tokens: 20 },
      steps: [
        { type: 'reasoning', content: 'I will inspect the workspace.' },
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'memory_list', args: {}, status: 'ok', output: '[]' },
          { id: 'tool_2', name: 'memory_search', args: {}, status: 'ok', output: '[]' },
        ] },
      ],
    },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-13T18:00:02Z',
      usage: { input_tokens: 200, output_tokens: 40, cache_read: 8 },
      steps: [{ type: 'text', content: 'The checks are complete.' }],
    },
    { role: 'user', content: 'Do one more thing', created_at: '2026-08-13T18:01:00Z' },
    {
      role: 'assistant', model: 'luna', created_at: '2026-08-13T18:01:01Z',
      usage: { input_tokens: 50, output_tokens: 10 },
      steps: [{ type: 'tool_calls', tool_calls: [{ id: 'tool_3', name: 'skill', args: { op: 'list' }, status: 'ok', output: '[]' }] }],
    },
  ]);

  const assistantTurns = thread.querySelectorAll('.agent-message.assistant');
  assert.equal(assistantTurns.length, 2);
  assert.equal(assistantTurns[0].querySelectorAll('.agent-turn-meta').length, 1);
  assert.equal(assistantTurns[0].querySelectorAll('.agent-tool-event').length, 2);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /deepseek/);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /↑300 ↓60/);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /cache 8/);
  assert.match(assistantTurns[1].querySelector('.agent-turn-meta').textContent, /luna/);
});

test('usage badges render compact token units instead of raw counts', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run checks', created_at: '2026-08-13T18:00:00Z' },
    {
      role: 'assistant', model: 'luna', created_at: '2026-08-13T18:00:01Z',
      usage: { input_tokens: 27085560, output_tokens: 137158, cache_read: 25944438 },
      steps: [{ type: 'text', content: 'done' }],
    },
  ]);
  const meta = thread.querySelector('.agent-turn-meta').textContent;
  assert.match(meta, /↑27\.09M ↓137\.16k/);
  assert.match(meta, /cache 25\.94M/);
  assert.doesNotMatch(meta, /27085560|137158|25944438/);
});

test('subagent cards are the only transcript representation of ACP runs', () => {
  const completion = 'Subagent run acprun_canonical123 completed. Full result delivered in the subagent_result tool call.';
  const thread = renderTranscript([
    { role: 'user', content: 'Delegate the CSS audit', created_at: '2026-08-30T00:00:00Z' },
    {
      role: 'assistant', created_at: '2026-08-30T00:00:01Z',
      steps: [{ type: 'tool_calls', tool_calls: [
        { id: 'spawn-1', name: 'subagent', args: { agent_id: 'acp_dev', prompt: 'Inspect the CSS' }, status: 'ok', output: completion },
        { id: 'wait-1', name: 'subagent_wait', args: { id: 'acprun_canonical123' }, status: 'ok', output: '---\nstatus: completed\nid: acprun_canonical123\n---' },
        { id: 'wait-2', name: 'subagent_wait', args: { id: 'acprun_canonical123' }, status: 'ok', output: '---\nstatus: completed\nid: acprun_canonical123\n---' },
        { id: 'result-1', name: 'subagent_result', args: { id: 'acprun_canonical123' }, status: 'ok', output: '---\nstatus: completed\nid: acprun_canonical123\n---\nDone.' },
      ] }],
    },
  ]);

  assert.equal(thread.querySelectorAll('.agent-subagent-card').length, 1,
    'one clickable card represents the delegated run');
  assert.equal(thread.querySelectorAll('.agent-tool-terminal').length, 0,
    'wait/result bookkeeping must not duplicate the card as tool rows');
  assert.equal(renderToolCallCard({ name: 'subagent_wait', args: {}, status: 'ok', output: '' }), null);
  assert.equal(renderToolCallCard({ name: 'subagent_result', args: {}, status: 'ok', output: '' }), null);
});

test('tool job summary includes elapsed span before chevron', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({ id: 'c1', name: 'exec', args: { command: 'ls' }, status: 'running' });
    const summary = job.querySelector('summary');
    assert.ok(summary.querySelector('.agent-tool-elapsed'), 'elapsed span present');
    const chevron = summary.querySelector('.agent-tool-event-chevron');
    assert.ok(chevron, 'chevron span present');
    // The chevron is the last direct child of the head row; elapsed lives in
    // the middle text column, so it always sits left of the chevron.
    assert.ok([...summary.children].indexOf(chevron) === summary.children.length - 1, 'chevron is the rightmost element');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('tool terminals render as compact execution timeline events', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({
      id: 'timeline-1',
      name: 'file_list',
      args: { path: '/workspace/telegram-research' },
      status: 'ok',
      output: 'count: 3\ntotal: 109K',
      presentation: {
        variant: 'file-list',
        action: 'Files listed',
        request: 'file_list({\n  "path": "/workspace/telegram-research"\n})',
        result: {
          format: 'list',
          summary: '3 entries · 109K',
          text: '-rw file-a\n-rw file-b\n-rw file-c',
          items: [{ name: 'file-a' }, { name: 'file-b' }, { name: 'file-c' }],
        },
      },
    });
    assert.equal(job.classList.contains('agent-tool-event'), true);
    assert.equal(job.open, true, 'event results are visible by default');
    assert.equal(job.querySelector('.agent-tool-event-title')?.textContent, 'Files listed');
    assert.equal(job.querySelector('.agent-tool-event-node')?.textContent, '✓');
    assert.equal(job.querySelector('.agent-tool-event-path')?.textContent, '/workspace/telegram-research');
    assert.match(job.querySelector('.agent-tool-event-summary-text')?.textContent || '', /3 entries · 109K/);
    const raw = job.querySelector('.agent-tool-event-details pre')?.textContent || '';
    assert.match(raw, /file_list\(/);
    assert.match(raw, /"path": "\/workspace\/telegram-research"/);
    assert.match(raw, /count: 3/);
    assert.match(job.querySelector('.agent-tool-event-rows')?.textContent || '', /file-a/);
    assert.equal(job.querySelector('.agent-tool-event-tool'), null, 'head never duplicates the tool name next to the action title');
    assert.equal(job.dataset.tool, 'file_list');
    assert.ok(job.classList.contains('agent-tool-file-list'), 'each built-in owns a dedicated class');
    assert.ok(job.querySelector('.agent-tool-file-list-path'), 'path dressing is scoped to file_list');
    assert.ok(job.querySelector('.agent-tool-file-list-result'), 'result dressing is scoped to file_list');
    assert.ok(job.querySelector('.agent-tool-file-list-details'), 'raw-fold dressing is scoped to file_list');

    const failed = renderToolJob({ name: 'file_list', args: {}, status: 'fail', output: 'permission denied' });
    assert.equal(failed.querySelector('.agent-tool-event-title')?.textContent, 'File listing failed');
    assert.equal(failed.querySelector('.agent-tool-event-node')?.textContent, '!');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('exec and MCP calls render one event with a live terminal output panel', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const execJob = renderToolJob({ id: 'e1', name: 'exec', args: { command: 'git status --short' }, status: 'running', output: '' });
    assert.ok(execJob.classList.contains('agent-tool-event'), 'exec uses the timeline event');
    assert.ok(execJob.classList.contains('is-terminal'), 'with the terminal output panel');
    assert.equal(execJob.querySelector('.agent-tool-event-path')?.textContent, 'git status --short');
    assert.ok(execJob.querySelector('.agent-tool-terminal-output'), 'output panel present');
    assert.equal(execJob.querySelector('.agent-tool-event-result'), null, 'no nested result box for exec');
    assert.ok(execJob.querySelector('.agent-tool-stop'), 'streaming exec keeps the stop button');
    assert.equal(execJob.dataset.tool, 'exec');
    assert.ok(execJob.classList.contains('agent-tool-exec'));
    assert.ok(execJob.querySelector('.agent-tool-exec-path'));
    assert.ok(execJob.querySelector('.agent-tool-exec-output'));
    assert.ok(execJob.querySelector('.agent-tool-exec-result'), 'terminal output has the contract result hook');

    const mcp = renderToolJob({
      id: 'm1', name: 'mcp_call', args: { ref: 'nusashell.files:read', arguments_json: '{"path":"/workspace/a.txt"}' },
      status: 'ok', output: 'done',
      presentation: { variant: 'terminal', action: 'MCP call completed', request: 'mcp_call(nusashell.files:read) {...}', result: { format: 'terminal', summary: '1 result', text: 'done' } },
    });
    assert.ok(mcp.classList.contains('is-terminal'));
    assert.equal(mcp.querySelector('.agent-tool-event-path')?.textContent, 'nusashell.files:read', 'path line shows the tool ref');
    assert.equal(mcp.querySelector('.agent-tool-event-badge')?.textContent, 'MCP', 'MCP badge marks the call');
    assert.match(mcp.querySelector('.agent-tool-terminal-output')?.textContent || '', /done/);
    assert.equal(mcp.dataset.tool, 'mcp_call');
    assert.ok(mcp.classList.contains('agent-tool-mcp-call'));
    assert.ok(mcp.querySelector('.agent-tool-mcp-call-path'));
    assert.ok(mcp.querySelector('.agent-tool-mcp-call-output'));

    const plugin = renderToolJob({
      id: 'p1', name: 'mcp__files__read', args: { path: '/a.txt' }, status: 'ok', output: 'ok',
    });
    assert.equal(plugin.dataset.tool, 'mcp__files__read');
    assert.ok(plugin.classList.contains('agent-tool-mcp'), 'plugin MCP tools share one dressing class');
    assert.equal(plugin.classList.contains('agent-tool-mcp-files-read'), false);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('built-in tool events isolate dressing classes so file_read does not share grep path/result hooks', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const read = renderToolJob({
      name: 'file_read', args: { path: '/workspace/a.txt' }, status: 'ok', output: 'hello',
    });
    const grep = renderToolJob({
      name: 'grep', args: { pattern: 'hello', path: '/workspace' }, status: 'ok', output: '1 match',
    });
    assert.equal(read.dataset.tool, 'file_read');
    assert.equal(grep.dataset.tool, 'grep');
    assert.ok(read.classList.contains('agent-tool-file-read'));
    assert.ok(grep.classList.contains('agent-tool-grep'));
    assert.ok(read.querySelector('.agent-tool-file-read-path'));
    assert.ok(grep.querySelector('.agent-tool-grep-path'));
    assert.equal(read.querySelector('.agent-tool-grep-path'), null);
    assert.equal(grep.querySelector('.agent-tool-file-read-path'), null);
    assert.ok(read.querySelector('.agent-tool-file-read-result'));
    assert.ok(grep.querySelector('.agent-tool-grep-result'));

    const predictable = renderToolJob({ name: 'automation_list', args: {}, status: 'ok', output: 'No automations.' });
    assert.ok(predictable.querySelector('.agent-tool-automation-list-request'));
    assert.ok(predictable.querySelector('.agent-tool-automation-list-result'));

    const ask = renderToolCallCard({
      name: 'ask_question',
      id: 'ask-1',
      args: { question: 'Pick one', options: [{ id: 'a', label: 'A' }] },
      status: 'ok',
      output: '{"answer":"A"}',
    });
    assert.equal(ask.dataset.tool, 'ask_question');
    assert.ok(ask.classList.contains('agent-ask-card'));
    assert.ok(ask.classList.contains('agent-tool-ask-question'));
    assert.ok(ask.querySelector('.agent-tool-ask-question-result'), 'ask output has the contract result hook');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('backend tool contracts own card identity and normalize media output attachments', () => {
  registerToolContracts({
    version: 1,
    tools: [
      {
        name: 'read_media', id: 'tool.read_media.v1', version: 1, css_class: 'agent-tool-read-media',
        description: 'Read media',
        input_schema: { type: 'object', properties: { file_path: { type: 'string' } } },
        presentation: { variants: ['media'], formats: ['media'], request_fields: ['file_path'], result_fields: ['attachments'] },
      },
    ],
  });
  const normalized = normalizeToolCall({
    name: 'read_media',
    output_attachments: [{ type: 'image', name: 'a.png', file_path: '/tmp/a.png' }],
    presentation: { variant: 'media', action: 'Media read', result: { format: 'media', summary: '1 image' } },
  });
  assert.equal(toolContractFor('read_media').css_class, 'agent-tool-read-media');
  assert.equal(toolContractFor('read_media').description, 'Read media');
  assert.equal(toolContractClass('weird__tool'), 'agent-tool-weird-tool');
  assert.equal(normalized.presentation.contract.id, 'tool.read_media.v1');
  assert.equal(normalized.presentation.result.attachments.length, 1);
  const canonicalOnly = normalizeToolCall({
    name: 'read_media',
    output_attachments: [],
    presentation: {
      variant: 'media',
      result: { format: 'media', attachments: [{ type: 'image', name: 'canonical.png', file_path: '/tmp/canonical.png' }] },
    },
  });
  assert.equal(canonicalOnly.output_attachments.length, 1, 'canonical attachments fill an empty legacy array');

  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderToolJob({
      ...normalized,
      args: { file_path: '/tmp/a.png' },
      status: 'ok',
    });
    assert.equal(card.dataset.tool, 'read_media');
    assert.equal(card.dataset.toolContract, 'tool.read_media.v1');
    assert.ok(card.classList.contains('agent-tool-read-media'));
    assert.ok(card.querySelector('.agent-tool-read-media-path'));
    assert.ok(card.querySelector('.agent-tool-read-media-result'));
    assert.ok(card.querySelector('.agent-tool-attachment-image img'));
    assert.equal(card.querySelector('img').getAttribute('src'), '/local-file?path=%2Ftmp%2Fa.png');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('status presentations render compact metadata chips instead of a raw YAML dump', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({
      name: 'file_write', args: { path: '/workspace/a.txt' }, status: 'ok',
      presentation: {
        variant: 'status', action: 'File written', request: 'file_write(...)',
        result: { format: 'status', summary: 'Written', meta: { bytes: 12, written: true, sha256: 'abcdef0123456789' } },
      },
    });
    assert.ok(job.querySelector('.agent-tool-event-status'));
    assert.match(job.querySelector('.agent-tool-event-summary-text')?.textContent || '', /Written/);
    assert.match(job.querySelector('.agent-tool-event-status')?.textContent || '', /bytes:12/);
    assert.doesNotMatch(job.querySelector('.agent-tool-event-status')?.textContent || '', /abcdef0123456789/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('late result presentation repaints the event result and keeps the raw request', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({
      name: 'file_list', args: { path: '/workspace' }, status: 'running',
      presentation: {
        variant: 'file-list', action: 'Listing files', request: 'file_list({\n  "path": "/workspace"\n})',
        result: { format: 'list', summary: 'Running' },
      },
    });
    setToolTerminalPresentation(job, {
      variant: 'file-list', action: 'Files listed', request: '',
      result: { format: 'list', summary: '1 entries', items: [{ name: 'a.txt', size: '12' }] },
    });
    assert.equal(job.querySelector('.agent-tool-event-title')?.textContent, 'Files listed');
    assert.match(job.querySelector('.agent-tool-event-summary-text')?.textContent || '', /1 entries/);
    assert.match(job.querySelector('.agent-tool-event-rows')?.textContent || '', /a\.txt/);
    const raw = job.querySelector('.agent-tool-event-details pre')?.textContent || '';
    assert.match(raw, /file_list\(/, 'raw request survives the late presentation patch');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('late terminal presentation preserves the contract-owned result hook', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({
      name: 'exec', args: { command: 'echo ok' }, status: 'running',
      presentation: {
        variant: 'terminal', action: 'Running command', request: 'exec(...)',
        result: { format: 'terminal', text: '… waiting for output' },
      },
    });
    setToolTerminalPresentation(job, {
      variant: 'terminal', action: 'Command completed', request: 'exec(...)',
      result: { format: 'terminal', summary: 'ok', text: 'ok' },
    });
    const output = job.querySelector('.agent-tool-terminal-output');
    assert.ok(output, 'terminal output remains mounted');
    assert.ok(output.classList.contains('agent-tool-exec-result'), 'backend tool result hook remains mounted');
    assert.ok(output.classList.contains('agent-tool-result'), 'generic result hook remains mounted');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('empty thread renders starter chips that fill the composer', () => {
  const dom = new JSDOM(`
    <div id="agent-thread"></div>
    <div id="tool-job-strip"></div>
    <div id="agent-todo-strip"></div>
    <textarea id="composer-input"></textarea>
  `);
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    assert.ok(STARTER_PROMPTS.length >= 3);
    renderEmptyThread();
    const chips = [...document.querySelectorAll('[data-starter-prompt]')];
    assert.equal(chips.length, STARTER_PROMPTS.length);
    chips[0].click();
    assert.equal(document.getElementById('composer-input').value, STARTER_PROMPTS[0].prompt);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('generate_image renders a proof card instead of a tool terminal', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc1', name: 'generate_image', args: { prompt: 'a red harbor boat' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genimage-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Developing|Emulsion|a red harbor boat/);

    const done = renderToolCallCard({
      id: 'tc1',
      name: 'generate_image',
      args: { prompt: 'a red harbor boat' },
      status: 'ok',
      output: '---\nstatus: completed\nprovider: openai\nmodel: gpt-image-1\nsize: 1024x1024\ncost_usd: 0.04\nfile_path: /tmp/gen-tc1.png\n---\nImage saved.',
      output_attachments: [{ type: 'image', name: 'gen-tc1.png', media_type: 'image/png', file_path: '/tmp/gen-tc1.png' }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const img = done.querySelector('img');
    assert.ok(img);
    assert.match(img.getAttribute('src'), /\/local-file\?path=/);
    assert.match(done.textContent, /gpt-image-1/);
    assert.match(done.textContent, /Download/);
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
    assert.ok(done.querySelector('.agent-genimage-open'));

    const failed = renderToolCallCard({
      id: 'tc1', name: 'generate_image', args: { prompt: 'a boat' }, status: 'fail',
      output: 'error: No image generation model is configured. Ask the user to pick an image model in Settings → Image generation.',
    });
    assert.equal(failed.classList.contains('is-error'), true);
    assert.match(failed.textContent, /Settings/);
    assert.equal(failed.querySelectorAll('img').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('unified generate_media uses the presentation metadata and media card', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderToolCallCard({
      id: 'media-1', name: 'generate_media', args: { media_type: 'image', prompt: 'a harbor at dusk' }, status: 'ok',
      presentation: {
        variant: 'media', action: 'Media generated', request: 'generate_media(...)',
        result: { format: 'media', meta: { status: 'completed', model: 'image-model', file_path: '/tmp/harbor.png' }, text: 'Image saved.' },
      },
      output_attachments: [{ type: 'image', name: 'harbor.png', media_type: 'image/png', file_path: '/tmp/harbor.png' }],
    });
    assert.ok(card.classList.contains('agent-genimage-card'));
    assert.ok(card.classList.contains('agent-tool-generate-media'));
    assert.ok(card.querySelector('.agent-tool-generate-media-request'));
    assert.ok(card.querySelector('.agent-tool-generate-media-result'));
    assert.equal(card.querySelectorAll('.agent-tool-terminal').length, 0);
    assert.match(card.textContent, /image-model/);
    assert.ok(card.querySelector('img'));
  } finally {
    globalThis.document = previousDocument;
  }
});

// Audio attachments returned by generate_speech (and any other tool that
// delivers persisted + inline audio) MUST render a playable <audio> element,
// not the generic TXT chip that image/video renderers explicitly avoid.
// Falls back to /local-file?path= when the inline data URL is too large to
// pass through history; inline wins when present so the chat thread can play
// audio without an extra HTTP round trip.
test('renderMessageAttachments renders an <audio> element for audio attachments', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const gallery = renderMessageAttachments([
      {
        type: 'audio',
        name: 'speech_20260825.wav',
        media_type: 'audio/wav',
        file_path: '/home/user/.config/nusashell/attachments/conv_1/speech_20260825.wav',
        data_url: 'data:audio/wav;base64,UklGRiQ=',
      },
    ]);
    const audio = gallery.querySelector('audio');
    assert.ok(audio, 'audio attachment renders an <audio> element');
    assert.equal(audio.getAttribute('controls'), '');
    assert.equal(audio.getAttribute('preload'), 'metadata');
    // inline data URL wins so playback works even if /local-file is not yet
    // resolvable (e.g. before the server has flushed the attachment).
    assert.equal(audio.getAttribute('src'), 'data:audio/wav;base64,UklGRiQ=');
    const fig = audio.closest('figure');
    assert.ok(fig, 'audio is wrapped in a figure for layout parity with image/video');
    assert.ok(fig.classList.contains('agent-message-audio'), 'audio figure carries the audio layout class');
    assert.match(fig.querySelector('figcaption').textContent, /speech_20260825\.wav/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('renderMessageAttachments falls back to /local-file when audio has no data_url', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const gallery = renderMessageAttachments([
      {
        type: 'audio',
        name: 'speech_20260825.wav',
        media_type: 'audio/wav',
        file_path: '/home/user/.config/nusashell/attachments/conv_1/speech_20260825.wav',
      },
    ]);
    const audio = gallery.querySelector('audio');
    assert.ok(audio);
    assert.equal(
      audio.getAttribute('src'),
      '/local-file?path=' + encodeURIComponent('/home/user/.config/nusashell/attachments/conv_1/speech_20260825.wav'),
      'audio src falls back to /local-file when inline data_url is missing',
    );
  } finally {
    globalThis.document = previousDocument;
  }
});

test('exec tool terminal renders a stop button only while running', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolJob({ id: 't1', name: 'exec', args: { command: 'sleep 5' }, status: 'running' });
    assert.equal(running.classList.contains('is-running'), true);
    let stop = running.querySelector('.agent-tool-stop');
    assert.ok(stop, 'streaming exec card has a stop button');
    assert.equal(stop.hidden, false, 'stop button visible while running');

    const done = renderToolJob({ id: 't2', name: 'exec', args: { command: 'ls' }, status: 'ok', output: 'ok\n' });
    assert.equal(done.classList.contains('is-success'), true);
    assert.equal(done.querySelector('.agent-tool-stop').hidden, true, 'stop hidden after done');

    // Non-streaming tools never show a stop button.
    const other = renderToolJob({ id: 't3', name: 'grep', args: {}, status: 'running' });
    assert.equal(other.querySelector('.agent-tool-stop'), null);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('appendToolJobDelta accumulates streamed output and clears placeholder', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({ id: 't1', name: 'exec', args: { command: 'ping' }, status: 'running' });
    const output = job.querySelector('.agent-tool-terminal-output');
    // Streaming exec starts with the "waiting" placeholder.
    assert.match(output.textContent, /waiting/);
    appendToolJobDelta(job, 'PING 8.8.8.8 (8.8.8.8)\n');
    assert.equal(output.textContent, 'PING 8.8.8.8 (8.8.8.8)\n');
    appendToolJobDelta(job, '64 bytes from 8.8.8.8\n');
    assert.equal(output.textContent, 'PING 8.8.8.8 (8.8.8.8)\n64 bytes from 8.8.8.8\n');
    // Placeholder must not reappear.
    assert.doesNotMatch(output.textContent, /waiting/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('applyQueuedToolDeltas keeps chunks until the tool card exists', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const pending = new Map([['tool_1', 'hello from exec\n']]);
    const emptyJobs = new Map();
    const first = applyQueuedToolDeltas(emptyJobs, pending);
    assert.equal(first.flushed, false, 'no card yet: nothing flushed');
    assert.equal(first.remaining.get('tool_1'), 'hello from exec\n', 'chunk is kept, not dropped');

    const job = renderToolJob({ id: 'tool_1', name: 'exec', args: { command: 'ping' }, status: 'running' });
    const jobs = new Map([['tool_1', job]]);
    const second = applyQueuedToolDeltas(jobs, first.remaining);
    assert.equal(second.flushed, true);
    assert.equal(second.remaining.size, 0);
    assert.equal(job.querySelector('.agent-tool-terminal-output').textContent, 'hello from exec\n');
    assert.doesNotMatch(job.querySelector('.agent-tool-terminal-output').textContent, /waiting/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('reasoningShouldStream is off once tools start or text arrives', () => {
  assert.equal(reasoningShouldStream({ thinkingLive: true, hidden: false }), true);
  assert.equal(reasoningShouldStream({ thinkingLive: true, hidden: false, hasText: true }), false);
  assert.equal(reasoningShouldStream({ thinkingLive: true, hidden: false, toolsStarted: true }), false);
  assert.equal(reasoningShouldStream({ thinkingLive: false, hidden: false }), false);
  assert.equal(reasoningShouldStream({ thinkingLive: true, hidden: true }), false);
});

test('setReasoningStreaming and sealReasoningStreaming toggle is-streaming', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const first = reasoningDisclosure('step one');
    first.hidden = false;
    const second = reasoningDisclosure('step two');
    second.hidden = false;
    const bubble = document.createElement('div');
    bubble.append(first, second);
    setReasoningStreaming(first, true);
    setReasoningStreaming(second, true);
    assert.equal(first.classList.contains('is-streaming'), true);
    assert.equal(second.classList.contains('is-streaming'), true);
    sealReasoningStreaming(first);
    assert.equal(first.classList.contains('is-streaming'), false);
    assert.equal(second.classList.contains('is-streaming'), true, 'sealing one node leaves siblings');
    sealReasoningStreaming(bubble);
    assert.equal(second.classList.contains('is-streaming'), false);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('bindToolStop calls agent.turns.stop with the run id until satisfied', async () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  let stopped = 0;
  const calls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts) => {
    calls.push({ url, body: opts?.body });
    stopped++;
    if (stopped === 1) {
      throw new Error('transport failed');
    }
    return { ok: true, json: async () => ({ result: { ok: true } }) };
  };
  try {
    const job = renderToolJob({ id: 't1', name: 'exec', args: { command: 'sleep 5' }, status: 'running' });
    bindToolStop(job, () => 'run-123');
    const stop = job.querySelector('.agent-tool-stop');
    assert.equal(stop.hidden, false);
    // First click: transport fails → button re-enabled.
    stop.click();
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(stop.disabled, false);
    assert.equal(stop.textContent, '■ Stop');
    // Second click: succeeds → button is retired.
    stop.click();
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(stopped, 2);
    assert.equal(stop.isConnected, false, 'stop button removed after successful stop');
    assert.ok(calls.every((c) => c.url.includes('agent/turns/stop')));
    const payload = JSON.parse(calls[1].body).payload;
    assert.equal(payload.run_id, 'run-123');
  } finally {
    globalThis.fetch = originalFetch;
    globalThis.document = previousDocument;
  }
});

// show(op="audio") parallels show(op="image): the backend returns the same
// wire shape { show: { type, src, path, name } } but with type="audio" and a
// data:audio/...;base64,... src. The frontend parser should match type
// === "audio" and the card should render an <audio controls> element with
// a Download link, mirroring renderShowImageCard.
test('parseShowAudioOutput matches show(op=audio) results', () => {
  const out = JSON.stringify({
    show: {
      type: 'audio',
      src: 'data:audio/wav;base64,UklGRiQ=',
      path: '/tmp/speech.wav',
      name: 'speech.wav',
    },
  });
  assert.deepEqual(
    parseShowAudioOutput({ name: 'show', output: out }),
    { src: 'data:audio/wav;base64,UklGRiQ=', path: '/tmp/speech.wav', name: 'speech.wav' },
  );
  // Other tool names are ignored.
  assert.equal(parseShowAudioOutput({ name: 'generate_speech', output: out }), null);
  // Non-audio show results are ignored.
  assert.equal(parseShowAudioOutput({ name: 'show', output: JSON.stringify({ show: { type: 'image', src: 'x' } }) }), null);
  // Unparseable output is ignored.
  assert.equal(parseShowAudioOutput({ name: 'show', output: 'not json' }), null);
  // Missing output is ignored.
  assert.equal(parseShowAudioOutput({ name: 'show', output: '' }), null);
});

test('renderToolCallCard renders an audio card for show(op=audio)', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderToolCallCard({
      id: 'tc-audio',
      name: 'show',
      args: { op: 'audio', path: '/tmp/speech.wav' },
      status: 'ok',
      output: JSON.stringify({
        show: {
          type: 'audio',
          src: 'data:audio/wav;base64,UklGRiQ=',
          path: '/tmp/speech.wav',
          name: 'speech.wav',
        },
      }),
    });
    assert.equal(card.classList.contains('agent-genaudio-card'), true,
      'audio card carries its own class for parallel layout with image');
    assert.ok(card.classList.contains('agent-tool-show'));
    assert.ok(card.querySelector('.agent-tool-show-request'));
    assert.ok(card.querySelector('.agent-tool-show-result'));
    const audio = card.querySelector('audio');
    assert.ok(audio, 'audio card renders an <audio> element');
    assert.equal(audio.getAttribute('controls'), '');
    assert.equal(audio.getAttribute('preload'), 'metadata');
    assert.equal(audio.getAttribute('src'), 'data:audio/wav;base64,UklGRiQ=',
      'audio src is the inline data URL');
    assert.match(card.textContent, /speech\.wav/);
    assert.equal(card.querySelectorAll('.agent-tool-terminal').length, 0,
      'audio does NOT fall through to the generic tool terminal');
    // Download affordance matches the image card.
    assert.ok(card.querySelector('a[download]'), 'audio card exposes a Download link');
  } finally {
    globalThis.document = previousDocument;
  }
});

// generate_speech: tool-card path mirrors generate_image. The card should
// surface model/voice/duration/cost metadata parsed from the YAML output
// frontmatter and a Download link, so users have the same affordances they
// already get for images.
test('renderToolCallCard renders a speech card for generate_speech', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc-sp', name: 'generate_speech', args: { text: 'hello' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genaudio-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Synthesizing|hello/);

    const done = renderToolCallCard({
      id: 'tc-sp', name: 'generate_speech', args: { text: 'hello' }, status: 'ok',
      output: '---\nstatus: completed\nprovider: piper\nmodel: id_ID-news_tts-medium\nvoice: female\nmedia_type: audio/wav\nfile_path: /tmp/speech.wav\n---\nSpeech generated and saved to /tmp/speech.wav.',
      output_attachments: [{
        type: 'audio', name: 'speech.wav', media_type: 'audio/wav', file_path: '/tmp/speech.wav',
      }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const audio = done.querySelector('audio');
    assert.ok(audio, 'speech card renders an <audio> element');
    assert.equal(audio.getAttribute('controls'), '');
    // Inline data URL is preferred so playback works without an HTTP round trip.
    assert.match(audio.getAttribute('src') || '', /\/local-file\?path=/,
      'speech card falls back to /local-file when only file_path is available');
    assert.match(done.textContent, /piper|id_ID-news_tts-medium|female/);
    assert.ok(done.querySelector('a[download]'), 'speech card has a Download link');
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

// show(op="video") parallels show(op=audio): the backend returns the same
// wire shape { show: { type, src, path, name } } but with type="video" and
// a data:video/...;base64,... src. The frontend parser should match type
// === "video" and the card should render a <video controls> element with
// a Download link, mirroring renderShowImageCard.
test('parseShowVideoOutput matches show(op=video) results', () => {
  const out = JSON.stringify({
    show: {
      type: 'video',
      src: 'data:video/mp4;base64,AAAA',
      path: '/tmp/clip.mp4',
      name: 'clip.mp4',
    },
  });
  assert.deepEqual(
    parseShowVideoOutput({ name: 'show', output: out }),
    { src: 'data:video/mp4;base64,AAAA', path: '/tmp/clip.mp4', name: 'clip.mp4' },
  );
  // Other tool names are ignored.
  assert.equal(parseShowVideoOutput({ name: 'generate_video', output: out }), null);
  // Non-video show results are ignored.
  assert.equal(parseShowVideoOutput({ name: 'show', output: JSON.stringify({ show: { type: 'image', src: 'x' } }) }), null);
  // Unparseable output is ignored.
  assert.equal(parseShowVideoOutput({ name: 'show', output: 'not json' }), null);
  // Missing output is ignored.
  assert.equal(parseShowVideoOutput({ name: 'show', output: '' }), null);
});

test('renderToolCallCard renders a video card for show(op=video)', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderToolCallCard({
      id: 'tc-vid',
      name: 'show',
      args: { op: 'video', path: '/tmp/clip.mp4' },
      status: 'ok',
      output: JSON.stringify({
        show: {
          type: 'video',
          src: 'data:video/mp4;base64,AAAA',
          path: '/tmp/clip.mp4',
          name: 'clip.mp4',
        },
      }),
    });
    assert.equal(card.classList.contains('agent-genvideo-card'), true,
      'video card carries its own class for parallel layout with image/audio');
    const video = card.querySelector('video');
    assert.ok(video, 'video card renders a <video> element');
    assert.equal(video.getAttribute('controls'), '');
    assert.equal(video.getAttribute('preload'), 'metadata');
    assert.equal(video.getAttribute('src'), 'data:video/mp4;base64,AAAA',
      'video src is the inline data URL');
    assert.match(card.textContent, /clip\.mp4/);
    assert.equal(card.querySelectorAll('.agent-tool-terminal').length, 0,
      'video does NOT fall through to the generic tool terminal');
    // Download affordance matches the image/audio cards.
    assert.ok(card.querySelector('a[download]'), 'video card exposes a Download link');
  } finally {
    globalThis.document = previousDocument;
  }
});

// generate_video: tool-card path mirrors generate_image and generate_speech.
// Surfaces provider/model/duration/resolution/cost metadata parsed from
// the YAML output frontmatter plus a click-to-play <video> plate and a
// Download link.
test('renderToolCallCard renders a video card for generate_video', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc-gv', name: 'generate_video', args: { prompt: 'a cat' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genvideo-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Rendering|a cat/);

    const done = renderToolCallCard({
      id: 'tc-gv', name: 'generate_video', args: { prompt: 'a cat' }, status: 'ok',
      output: '---\nstatus: completed\nprovider: veo3\nmodel: veo-3\nmedia_type: video/mp4\nfile_path: /tmp/clip.mp4\n---\nVideo generated and saved to /tmp/clip.mp4.',
      output_attachments: [{
        type: 'video', name: 'clip.mp4', media_type: 'video/mp4', file_path: '/tmp/clip.mp4',
      }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const video = done.querySelector('video');
    assert.ok(video, 'video card renders a <video> element');
    assert.equal(video.getAttribute('controls'), '');
    assert.match(video.getAttribute('src') || '', /\/local-file\?path=/,
      'video card falls back to /local-file when only file_path is available');
    assert.match(done.textContent, /veo3|veo-3/);
    assert.ok(done.querySelector('a[download]'), 'video card has a Download link');
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

function openDetails(details) {
  const EventCtor = details.ownerDocument?.defaultView?.Event || Event;
  details.open = true;
  details.dispatchEvent(new EventCtor('toggle'));
}

test('reasoning markdown stays out of the DOM until the disclosure is opened', () => {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const details = reasoningDisclosure('I will inspect **the workspace**.');
    document.body.append(details);
    const content = details.querySelector('.agent-reasoning-content');
    assert.equal(details.hidden, false, 'non-empty reasoning is visible as a collapsed row');
    assert.equal(content.innerHTML, '', 'collapsed reasoning is not markdown-parsed');
    assert.doesNotMatch(details.textContent, /the workspace/, 'raw reasoning is not in the collapsed DOM');
    openDetails(details);
    assert.match(content.innerHTML, /<strong>the workspace<\/strong>/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('Thinking renders ordered markers and mixed table/backtick Markdown safely', () => {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const tick = String.fromCharCode(96);
    const fence = tick.repeat(3);
    const source = [
      `1. Create ${tick}server.go${tick}`,
      '2. Wire the handler',
      '3. Run the tests',
      '',
      '| Area | Value |',
      '| --- | --- |',
      `| Inline | ${tick}safe${tick} |`,
      '',
      `${fence}go`,
      'func main() {}',
      fence,
    ].join('\n');
    const details = reasoningDisclosure(source);
    document.body.append(details);
    const content = details.querySelector('.agent-reasoning-content');

    openDetails(details);

    const ordered = content.querySelector('ol');
    assert.ok(ordered, 'ordered Markdown list renders as <ol>');
    assert.deepEqual(
      [...ordered.querySelectorAll(':scope > li')].map((item) => item.textContent.trim()),
      ['Create server.go', 'Wire the handler', 'Run the tests'],
    );
    assert.ok(content.querySelector('.markdown-table-scroll > table'), 'table stays inside its scroll wrapper');
    assert.ok(content.querySelector('.markdown-table-scroll code:not(pre code)'), 'inline backtick code stays inline in a table cell');
    assert.ok(content.querySelector('pre[data-complete="true"] > code.language-go'), 'triple-backtick fence becomes a complete code block');
    assert.doesNotMatch(content.textContent, /```/, 'fence markers are not exposed as visible text');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('expanded Thinking and tool disclosures survive a transcript refresh', () => {
  const messages = [
    { role: 'user', content: 'Inspect the project', created_at: '2026-08-30T00:00:00Z' },
    {
      role: 'assistant', id: 'assistant-round-1', created_at: '2026-08-30T00:00:01Z',
      steps: [
        { type: 'reasoning', content: 'First round reasoning' },
        { type: 'tool_calls', tool_calls: [{ id: 'tool-round-1', name: 'exec', args: { command: 'pwd' }, status: 'ok', output: '/workspace' }] },
      ],
    },
    {
      role: 'assistant', id: 'assistant-round-2', created_at: '2026-08-30T00:00:02Z',
      steps: [{ type: 'reasoning', content: 'Second round reasoning' }],
    },
  ];
  const before = renderTranscript(messages);
  const thinking = before.querySelectorAll('.agent-reasoning');
  const tools = before.querySelectorAll('.agent-tool-event');
  // A live placeholder can carry the assistant turn's aggregate IDs before
  // its first round has been stamped with the individual message ID.
  before.querySelector('.agent-round').removeAttribute('data-message-id');
  // A live tool card is also created before its persisted call ID is known.
  tools[0].removeAttribute('data-tool-call-id');
  thinking[0].open = true;
  thinking[1].open = true;
  tools[0].open = false;

  const disclosureState = captureDisclosureState(before);
  const refreshed = renderTranscript(messages);
  restoreDisclosureState(refreshed, disclosureState);

  assert.equal(refreshed.querySelectorAll('.agent-reasoning')[0].open, true, 'expanded live Thinking remains open');
  assert.equal(refreshed.querySelectorAll('.agent-reasoning')[1].open, true, 'expanded Thinking remains open');
  assert.equal(refreshed.querySelector('.agent-tool-event').open, false, 'collapsed tool event stays collapsed across refresh');
});

test('whitespace-only reasoning stays hidden and unparsed', () => {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const details = reasoningDisclosure('  \n\t');
    document.body.append(details);
    assert.equal(details.hidden, true);
    assert.equal(details.querySelector('.agent-reasoning-content').innerHTML, '');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('snapshot renderConversation keeps the user bubble and every round it is given (no earlier-rounds stub)', () => {
  const messages = [{ role: 'user', content: 'go', created_at: '2026-08-28T00:00:00Z' }];
  for (let i = 0; i < 6; i++) {
    messages.push({
      role: 'assistant',
      id: `msg_round_${i}`,
      model: 'luna',
      created_at: `2026-08-28T00:00:0${i + 1}Z`,
      steps: [
        { type: 'reasoning', content: `thinking UNIQUE_ROUND_${i}` },
        { type: 'text', content: `visible round ${i}` },
        { type: 'tool_calls', tool_calls: [{ id: `t${i}`, name: 'file_read', args: {}, status: 'ok', output: `out ${i}` }] },
      ],
    });
  }
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    thread.append(renderConversation(messages));
    assert.ok(thread.querySelector('.agent-message.user'));
    const turn = thread.querySelector('.agent-message.assistant');
    assert.equal(turn.querySelector('.agent-round-stub'), null);
    assert.equal(turn.querySelectorAll(':scope > .agent-bubble > .agent-round').length, 6);
    assert.match(turn.textContent, /visible round 0/);
    assert.match(turn.textContent, /visible round 5/);
    assert.doesNotMatch(turn.textContent, /UNIQUE_ROUND_5/, 'kept-round reasoning is still lazy');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('mountLiveRound keeps every live round mounted (no trim, no stub)', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-bubble"></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.querySelector('.agent-bubble');
    let current;
    for (let i = 0; i < 6; i++) {
      current = mountLiveRound(bubble, { rawReasoning: `think ${i}` });
    }
    // The hide-bubble workaround is gone: live deltas must never remove
    // earlier rounds from the DOM nor replace them with a stub.
    assert.equal(bubble.querySelector(':scope > .agent-round-stub'), null);
    const rounds = bubble.querySelectorAll(':scope > .agent-round');
    assert.equal(rounds.length, 6);
    for (const round of rounds) assert.ok(round.isConnected, 'round stays mounted');
    assert.equal(rounds[0].querySelector('.agent-reasoning')._reasoningRaw, 'think 0');
    assert.equal(bubble._liveRoundArchive, undefined, 'no parking archive');
    assert.ok(current.textBox.closest('.agent-round')?.isConnected, 'newest live round remains mounted');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('optimistic turn reuses a WS-first placeholder instead of painting a second ...', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    const bubble = document.createElement('div');
    bubble.className = 'agent-bubble';
    const placeholder = document.createElement('div');
    placeholder.className = 'agent-message assistant agent-pending';
    placeholder.append(bubble);
    const slot = mountLiveRound(bubble, {});
    slot.reasoningEl.hidden = true;
    slot.textBox.append(thinkingDots());
    thread.append(placeholder);

    const bound = bindOptimisticTurn(thread, {
      role: 'user',
      content: 'gimana menurut mu?',
      created_at: '2026-08-29T16:05:00Z',
    }, { msgNode: placeholder, bubble, ...slot });

    const nodes = [...thread.children];
    assert.equal(nodes.length, 2, 'user then one assistant placeholder');
    assert.ok(nodes[0].classList.contains('user'));
    assert.ok(nodes[1].classList.contains('assistant'));
    assert.equal(thread.querySelectorAll('.agent-thinking-dots').length, 1);
    assert.equal(bound.msgNode, placeholder);
    assert.equal(nodes[1], placeholder);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('optimistic turn appends user then a single placeholder when WS has not arrived', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    bindOptimisticTurn(thread, {
      role: 'user',
      content: 'hello',
      created_at: '2026-08-29T16:05:00Z',
    });
    const nodes = [...thread.children];
    assert.equal(nodes.length, 2);
    assert.ok(nodes[0].classList.contains('user'));
    assert.ok(nodes[1].classList.contains('agent-pending'));
    assert.equal(thread.querySelectorAll('.agent-thinking-dots').length, 1);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('sealing a live node before steer removes only empty thinking rounds', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-message assistant"><div class="agent-bubble"></div></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const node = document.querySelector('.agent-message.assistant');
    const bubble = node.querySelector('.agent-bubble');
    const settled = mountLiveRound(bubble, {});
    settled.textBox.textContent = 'Keep this completed round';
    const empty = mountLiveRound(bubble, {});
    empty.textBox.append(document.createElement('span'));
    empty.textBox.firstElementChild.className = 'agent-thinking-dots';

    sealLiveNodeBeforeSteer(node);

    assert.equal(node.querySelectorAll('.agent-thinking-dots').length, 0);
    assert.equal(node.querySelectorAll('.agent-round').length, 1);
    assert.match(node.textContent, /Keep this completed round/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('a delayed live round inserts immediately after its steer anchor', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    const prior = document.createElement('div');
    prior.dataset.order = 'prior';
    const steer = document.createElement('div');
    steer.dataset.order = 'steer';
    const later = document.createElement('div');
    later.dataset.order = 'later';
    const response = document.createElement('div');
    response.dataset.order = 'response';
    thread.append(prior, steer, later);

    insertAfterOrAppend(thread, response, steer);

    assert.deepEqual(
      [...thread.children].map((node) => node.dataset.order),
      ['prior', 'steer', 'response', 'later'],
    );
  } finally {
    globalThis.document = previousDocument;
  }
});

test('appendLiveError preserves streamed assistant content', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-bubble"></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.querySelector('.agent-bubble');
    const { textBox } = mountLiveRound(bubble, {});
    textBox.textContent = 'The streamed answer remains visible';
    appendLiveError(bubble, 'provider failed');
    assert.match(bubble.textContent, /The streamed answer remains visible/);
    assert.match(bubble.textContent, /provider failed/);
    assert.equal(bubble.querySelectorAll('.agent-live-error').length, 1);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('live rounds stay mounted no matter how many arrive (performance is CSS + targeted enhancement, not DOM removal)', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-bubble"></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.querySelector('.agent-bubble');
    let current;
    for (let i = 0; i < 40; i++) {
      current = mountLiveRound(bubble, { raw: `output ${i}` });
    }
    assert.equal(bubble.querySelectorAll(':scope > .agent-round').length, 40);
    assert.equal(bubble.querySelector(':scope > .agent-round-stub'), null);
    assert.equal(bubble._liveRoundArchive, undefined);
    for (const round of bubble.querySelectorAll(':scope > .agent-round')) {
      assert.ok(round.isConnected, 'every round stays in the live DOM');
    }
    assert.ok(current.textBox.closest('.agent-round')?.isConnected);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('retry button only for a failed message still last in its turn group (recovered errors must not offer retry)', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  const onRetry = () => {};
  try {
    // A failed assistant followed by a completed assistant was already
    // recovered (error status stays in formed history by design). The
    // backend's lastFailedAssistantIndex stops at the first done assistant
    // from the end, so offering Retry here produced the NOT_FOUND
    // "no failed assistant turn to retry" pop-up.
    const recovered = [
      { role: 'user', content: 'go', created_at: '2026-08-31T10:00:00Z' },
      { role: 'assistant', id: 'msg_failed', status: 'error', error: 'OpenRouter is rate-limited', created_at: '2026-08-31T10:01:00Z' },
      { role: 'assistant', id: 'msg_ok', status: 'done', content: 'recovered answer', created_at: '2026-08-31T10:02:00Z' },
    ];
    let thread = document.getElementById('thread');
    thread.append(renderConversation(recovered, onRetry));
    assert.equal(thread.querySelector('.agent-retry-btn'), null, 'recovered error must not offer retry');
    assert.equal(thread.querySelector('.agent-error-text'), null, 'recovered error text must not be shown as active error');
    assert.match(thread.textContent, /recovered answer/);

    // A failed assistant that IS the last message still offers retry.
    thread = document.getElementById('thread');
    thread.replaceChildren();
    const stillFailed = [
      { role: 'user', content: 'go', created_at: '2026-08-31T11:00:00Z' },
      { role: 'assistant', id: 'msg_failed2', status: 'error', error: 'provider exploded', created_at: '2026-08-31T11:01:00Z' },
    ];
    thread.append(renderConversation(stillFailed, onRetry));
    assert.ok(thread.querySelector('.agent-retry-btn'), 'last failed message must offer retry');
    assert.match(thread.textContent, /provider exploded/);
  } finally {
    globalThis.document = previousDocument;
  }
});
