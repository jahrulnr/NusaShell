import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const agentCSS = await readFile(new URL('./styles/agent.css', import.meta.url), 'utf8');
const globalCSS = await readFile(new URL('./styles/global.css', import.meta.url), 'utf8');
const agentView = await readFile(new URL('./js/views/agent.js', import.meta.url), 'utf8');
const appShell = await readFile(new URL('./js/app.js', import.meta.url), 'utf8');
const agentRender = await readFile(new URL('./js/views/agent/render.js', import.meta.url), 'utf8');
const agentComposer = await readFile(new URL('./js/views/agent/composer.js', import.meta.url), 'utf8');
const agentModelPicker = await readFile(new URL('./js/views/agent/model-picker.js', import.meta.url), 'utf8');
const subagentsView = await readFile(new URL('./js/views/agent/subagents.js', import.meta.url), 'utf8');
const acpCSS = await readFile(new URL('./styles/acp.css', import.meta.url), 'utf8');

test('Agent uses the Electron workspace shell without unsupported Todo UI', () => {
  assert.match(html, /class="agent-shell" id="agent-shell"/);
  assert.match(html, /class="agent-conversations" id="agent-conversations"/);
  assert.match(html, /class="agent-thread" id="agent-thread" role="log"/);
  assert.doesNotMatch(html, /class="agent-header"/);
  assert.doesNotMatch(html, /agent-task-strip/);
  // The todo checklist strip uses a distinct class name (agent-todo-strip).
  assert.match(html, /class="agent-todo-strip" id="agent-todo-strip"/);
  assert.match(html, /id="agent-todo-strip-list"/);
  // The strip's live summary line is rendered via the meta span; the old
  // "summary" element was replaced by count + meta during the agent view
  // refactor, so assert on the elements the UI actually uses.
  assert.match(html, /id="agent-todo-strip-count"/);
  assert.match(html, /id="agent-todo-strip-meta"/);
  // The brief is a Markdown document (## Objective, ## Done when, …) and must
  // render as parsed HTML, not raw text. A <div> container is required because
  // block-level Markdown (<p>, <h2>, <ul>) is invalid inside a <p>.
  assert.match(html, /<div class="agent-todo-strip-brief" id="agent-todo-strip-brief" hidden><\/div>/);
  assert.match(agentView, /briefEl\.innerHTML = renderMarkdown\(brief\.trim\(\)\)/);
  const tabletRules = agentCSS.slice(
    agentCSS.indexOf('@media (max-width: 900px)'),
    agentCSS.indexOf('@media (max-width: 760px)'),
  );
  assert.match(tabletRules, /\.agent-conversations \{ display: none; \}/);
});

test('Agent tool transcripts start collapsed, cap output at ten lines, and lazy-render reasoning', () => {
  assert.match(agentRender, /class: 'agent-tool-terminal'/);
  assert.match(agentRender, /el\('details', \{ class: 'agent-tool-terminal'/);
  assert.match(agentRender, /function materializeReasoning/);
  assert.doesNotMatch(agentRender, /content\.innerHTML = renderMarkdown\(reasoning\)/);
  assert.doesNotMatch(agentView, /content\.innerHTML = renderMarkdown\(run\.rawReasoning\)/);
  assert.match(agentCSS, /\.agent-tool-terminal-output \{[^}]*max-height: calc\(10 \* 1\.55em\);[^}]*overflow-y: auto;/s);
});

test('Archived chunks load only on explicit Load older or scroll-to-top, never after turn.done or live compaction', () => {
  // Proactive maybeLoadOlderChunk after refresh/compaction re-inflated the
  // just-archived long turn into the DOM and froze the thread.
  assert.doesNotMatch(agentView, /maybeLoadOlderChunk/);
  assert.match(agentView, /function revealOlderHistory/);
  assert.match(agentView, /hasOlderActiveMessages\(\) \|\| state\.nextChunkIndex >= 0/);
});

test('Room snapshots keep the complete trailing run; older complete turns remain explicit', () => {
  // The hide-bubble workaround (park rounds, "N earlier rounds trimmed"
  // stub) is removed — it traded UX away for performance.
  assert.doesNotMatch(agentRender, /KEEP_VISIBLE_ROUNDS|MAX_PARKED_LIVE_ROUNDS|parkLiveRound|restoreParkedLiveRounds|pruneOverflowLiveRounds|agent-round-stub/);
  assert.match(agentRender, /export function mountLiveRound/);
  // Snapshot history is backed by the conversation tail + explicit Load
  // older, but the current assistant run is never trimmed.
  assert.doesNotMatch(agentView, /SNAPSHOT_KEEP_ROUNDS|assistKeepStart/);
  assert.doesNotMatch(agentView, /KEEP_VISIBLE_ROUNDS/);
  // The browser-level guard rails that replace the trimming: per-delta
  // enhancement only touches newly rendered blocks (incrementalRender).
  // No CSS containment on .agent-round — content-visibility there clipped
  // the reasoning mark / tool-terminal chrome drawn at the round edges.
  assert.doesNotMatch(agentCSS, /\.agent-round \{[^}]*content-visibility/s);
  assert.doesNotMatch(agentCSS, /\.agent-round \{[^}]*content-visibility/s);
  assert.match(agentView, /incrementalRender\(/);
  assert.match(agentView, /scheduleLiveEnhancement\(/);
  assert.match(agentView, /mountLiveRound\(/);
  assert.match(agentView, /scheduleLiveRender/);
  assert.match(agentView, /run\.toolJobs = toolJobs/);
  assert.match(agentView, /retryTurn\(node, message_id\)/);
  assert.match(agentView, /MAX_LIVE_ROUND_CHARS = 512 \* 1024/);
  assert.match(agentView, /MAX_LIVE_TOOL_JOBS = 128/);
  assert.match(agentView, /setLiveToolJob/);
  // The raw-cap banner was taken out too: nothing in the live thread may be
  // hidden or replaced by a notice — the 512KB memory caps stay, silently.
  assert.doesNotMatch(agentCSS, /agent-live-trimmed/);
  assert.doesNotMatch(agentView, /updateLiveTrimNotice|rawCapped|rawReasoningCapped/);
  assert.doesNotMatch(agentCSS, /agent-round-stub/);
  assert.doesNotMatch(agentCSS, /agent-round-stub/);
  assert.match(html, /id="agent-provider-status" aria-live="polite" aria-atomic="true"/);
  assert.match(agentView, /conversationTail\(/);
  assert.doesNotMatch(agentRender, /function earlierRoundsDisclosure/);
  assert.match(agentView, /complete trailing assistant run/);
  assert.doesNotMatch(agentView, /keepAllTrailing|hasOlderTurnRounds/);
  assert.match(agentView, /maybeAutoTitleConversation\(id, \{ conversation, messages \}\)/);
});

test('Context usage stays visible in the narrow composer', () => {
  const narrowRules = agentCSS.slice(
    agentCSS.indexOf('@media (max-width: 760px)'),
    agentCSS.indexOf('@media (max-width: 680px)'),
  );
  assert.doesNotMatch(narrowRules, /\.agent-provider-status\s*\{\s*display:\s*none;\s*\}/,
    'mobile must not hide the backend context usage badge');
  assert.match(narrowRules, /\.agent-provider-status\s*\{[\s\S]*display:\s*inline-flex;/,
    'mobile context usage should remain a visible compact status');
  assert.match(narrowRules, /\.agent-composer-actions\s*\{[\s\S]*width:\s*100%;/,
    'status and send controls need a full-width mobile action row');
});

test('Agent delegates presentation, composer, and model picker responsibilities to focused modules', () => {
  assert.match(agentView, /from '\.\/agent\/render\.js'/);
  assert.match(agentView, /from '\.\/agent\/composer\.js'/);
  assert.match(agentView, /from '\.\/agent\/model-picker\.js'/);
  assert.match(agentComposer, /export function bindComposer/);
  assert.match(agentModelPicker, /export function bindModelPicker/);
});

test('Subagent drawer reuses the conversation sidebar design', () => {
  assert.match(subagentsView, /class: 'agent-conversations acp-run-sidebar'/);
  assert.match(subagentsView, /class: 'agent-conversation-list acp-run-list'/);
  assert.match(subagentsView, /class: `agent-conversation-item/);
  assert.doesNotMatch(subagentsView, /acp-run-switcher|acp-run-switch'/);
  assert.match(acpCSS, /\.acp-drawer-shell \{[^}]*grid-template-columns:/s);
});

test('ACP transcripts use the agent conversation structure and historical run lookup', () => {
  assert.match(subagentsView, /acp\.runs\.get/);
  assert.match(subagentsView, /class: 'agent-message assistant acp-run-message'/);
  assert.match(subagentsView, /class: 'agent-bubble acp-transcript-body'/);
  assert.match(subagentsView, /class: 'agent-message user acp-prompt-message'/);
  assert.match(subagentsView, /kind === 'prompt'/);
  assert.match(subagentsView, /syncTranscript\(panel, run\.transcript \|\| \[\], run\.prompt, run\.started_at\)/);
  assert.match(subagentsView, /class: `agent-round acp-transcript-round/);
  assert.match(subagentsView, /class: 'agent-tool-stack'/);
  assert.match(acpCSS, /\.acp-run-panel > \.agent-thread \{[^}]*overflow: visible;[^}]*padding: 0;/s);
  assert.doesNotMatch(subagentsView, /acp-transcript-line/);
  assert.doesNotMatch(acpCSS, /\.acp-transcript-line/);
  assert.match(agentRender, /extractSubagentRunIDs/);
});

test('Thinking and tool markers share the same conversation rail', () => {
  assert.match(agentCSS, /\.agent-reasoning summary \{[^}]*margin-left: -12px;/s);
  assert.match(agentCSS, /\.agent-tool-terminal summary \{[^}]*margin-left: -12px;/s);
});

test('Expanded Thinking is capped to twenty lines and scrolls internally', () => {
  const reasoningBody = agentCSS.slice(
    agentCSS.indexOf('.agent-reasoning-content {'),
    agentCSS.indexOf('.agent-reasoning-content > :first-child'),
  );
  assert.match(reasoningBody, /max-height: 33em;/);
  assert.match(reasoningBody, /overflow-y: auto;/);
  assert.match(reasoningBody, /overflow-x: auto;/);
  assert.match(reasoningBody, /scrollbar-gutter: stable;/);
  assert.match(reasoningBody, /overscroll-behavior: contain;/);
  assert.match(agentCSS, /\.agent-reasoning-content ul \{[^}]*padding-left: 1\.5em;/s,
    'bullet indentation remains compact');
  assert.match(agentCSS, /\.agent-message\.assistant \.agent-bubble \.agent-reasoning-content ol \{[^}]*padding-left: 2\.25em;/s,
    'ordered-list markers need enough left gutter to stay outside the scroll clip');
});

test('User Markdown bubbles keep prose normal and fenced code inside a scrollable card', () => {
  assert.match(agentCSS, /\.agent-message\.user \.agent-bubble \{[^}]*white-space: normal;/s);
  assert.match(agentCSS, /\.agent-message\.user \.agent-bubble-text > p,[\s\S]*white-space: pre-line;/s,
    'user prose must preserve Markdown soft line breaks');
  assert.match(agentCSS, /\.agent-message\.user \.agent-bubble-text > p,\s*\.agent-message\.user \.agent-bubble-text > ul,\s*\.agent-message\.user \.agent-bubble-text > ol \{[^}]*margin: 0;/s,
    'two source newlines must collapse to one visible block break');
  assert.match(agentCSS, /\.agent-message\.user \.agent-bubble pre \{[^}]*overflow-x: auto;/s);
  assert.match(agentCSS, /\.agent-message\.user \.agent-bubble pre code \{[^}]*display: block;/s);
});

test('Assistant tables keep their content width inside a horizontal scroller', () => {
  const tableRules = agentCSS.slice(agentCSS.indexOf('.markdown-table-scroll {'));
  assert.match(tableRules, /overflow-x:\s*auto/);
  assert.match(tableRules, /\.markdown-table-scroll table\s*\{[\s\S]*display:\s*table/);
  assert.match(tableRules, /\.markdown-table-scroll table\s*\{[\s\S]*width:\s*max-content/);
  assert.match(tableRules, /\.markdown-table-scroll table\s*\{[\s\S]*min-width:\s*100%/);
});

test('Live Thinking follows only while the thread-end marker is visible', () => {
  assert.match(agentView, /agent-thread-end-marker/);
  assert.match(agentView, /new IntersectionObserver/);
  assert.match(agentView, /state\.pinned = entry\.isIntersecting \|\| isThreadAtBottom\(thread\)/);
  assert.match(agentView, /state\.pinned = isThreadAtBottom\(thread\)/);
  assert.match(agentView, /if \(!force && !state\.pinned\) return/);
});

test('Turn completion samples the real scroll position before playing its sound', () => {
  const doneHandler = agentView.slice(
    agentView.indexOf("on('agent.turn.done'"),
    agentView.indexOf("on('agent.turn.error'"),
  );
  assert.match(doneHandler, /syncActiveThreadPin\(\);/);
  assert.match(doneHandler, /playComplete\(/);
  assert.match(doneHandler, /refreshActiveConversation\(\)/);
});

test('Transcript refresh restores user-controlled Thinking and tool disclosure state', () => {
  const refreshHandler = agentView.slice(agentView.indexOf('async function refreshActiveConversation'));
  assert.match(agentRender, /export function captureDisclosureState/);
  assert.match(agentRender, /export function restoreDisclosureState/);
  assert.match(refreshHandler, /const disclosureState = captureDisclosureState\(thread\);/);
  assert.match(refreshHandler, /restoreDisclosureState\(thread, disclosureState\);/);
  assert.match(refreshHandler, /renderThread\(windowedActiveMessages\(\), state\.pinned, disclosureState\)/);
});

test('Live tool terminals carry their call ID into disclosure restoration', () => {
  const liveToolFactory = agentView.slice(
    agentView.indexOf('function ensureLiveToolJob'),
    agentView.indexOf('function resetLiveRoundText'),
  );
  assert.match(liveToolFactory, /renderToolJob\(\{ id: toolCallId, name: toolName/);
});

test('ACP wait/result bookkeeping never mounts a duplicate live tool row', () => {
  assert.match(agentRender, /isSubagentAuxiliaryTool\(toolCall\.name\)/);
  assert.match(agentView, /isSubagentAuxiliaryTool\(name\)\) return;/);
  assert.match(agentView, /isSubagentAuxiliaryTool\(frame\.name\)\) break;/);
});

test('Narrow windows ellipsize tool meta instead of overflowing the thread', () => {
  assert.match(agentCSS, /\.agent-tool-terminal summary \{[^}]*display: flex;/s);
  assert.match(agentCSS, /\.agent-tool-terminal-meta \{[^}]*flex: 1 1 0;/s);
  assert.match(agentCSS, /\.agent-tool-stack \{[^}]*min-width: 0;/s);
  assert.match(agentCSS, /\.agent-tool-terminal \{[^}]*max-width: 100%;/s);
  assert.match(agentCSS, /\.agent-reasoning summary \{[^}]*display: flex;/s);
  assert.match(agentCSS, /\.agent-bubble \{[^}]*min-width: 0;/s);
  assert.match(agentCSS, /\.agent-bubble-text \{[^}]*overflow-wrap: anywhere;/s);
  assert.match(agentCSS, /\.agent-conversation \{[^}]*min-width: 0;/s);
  assert.match(agentCSS, /\.agent-thread \{[^}]*min-width: 0;/s);
  assert.doesNotMatch(agentCSS, /grid-template-columns: 24px minmax\(0, 1fr\) auto auto auto auto/);
});

test('Live tool deltas stay queued until the card exists and thinking pulse is phase-gated', () => {
  assert.match(agentRender, /export function applyQueuedToolDeltas/);
  assert.match(agentView, /applyQueuedToolDeltas\(run\.toolJobs, run\.pendingToolDeltas\)/);
  assert.doesNotMatch(agentView, /run\.pendingToolDeltas\.clear\(\)/);
  assert.match(agentView, /function ensureLiveToolJob/);
  assert.match(agentView, /run\.toolsStarted = true/);
  assert.match(agentView, /run\.thinkingLive = true/);
  assert.match(agentView, /reasoningShouldStream\(/);
  assert.match(agentView, /sealReasoningStreaming\(run\.bubble\)/);
});

test('Agent live motion respects reduced-motion preferences', () => {
  assert.match(agentCSS, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(globalCSS, /animation-duration: \.001ms !important/);
});

test('Agent surfaces an unavailable backend promptly instead of waiting for a normal RPC timeout', () => {
  assert.match(appShell, /rpc\('app\.info', \{\}, \{ timeoutMs: 4000 \}\)/);
});

test('Live compaction does not append a marker at the thread tail, and a new round does not open two thinking slots', () => {
  // Mid-turn compaction used to thread.append a marker while deltas still
  // targeted the live bubble above it. The live path must splice history
  // in front of the active node instead.
  assert.doesNotMatch(agentView, /thread\.append\(el\('div', \{ class: 'agent-compaction-marker'/);
  assert.match(agentView, /applyLiveCompaction/);
  assert.match(agentView, /stampRunMessageId/);
  assert.match(agentView, /reusePlaceholder/);
  // turns.start HTTP vs agent.turn.started WS: queue the event while the
  // composer is in-flight, then bind one placeholder (never dots-user-dots).
  assert.match(agentView, /localTurnPending/);
  assert.match(agentView, /bindOptimisticTurn/);
  assert.match(agentRender, /export function bindOptimisticTurn/);
  assert.match(agentComposer, /state\.localTurnPending = true/);
});

test('Compaction lifecycle is room-scoped and reuses the Thinking disclosure for handover', () => {
  assert.match(agentView, /on\('agent\.compacting'/);
  assert.match(agentView, /markCompacting\(conversation_id, run_id\)/);
  assert.match(agentView, /clearCompactionStatus\(conversation_id, run_id\)/);
  assert.match(agentRender, /title: 'Compacted context'/);
  assert.match(agentRender, /collapsedHint: 'Show handover'/);
  assert.match(agentCSS, /\.agent-compaction-status[\s\S]*agent-compaction-dot/);
});

test('Steer application seals old dots and anchors the next round after the user bubble', () => {
  assert.match(agentView, /sealLiveNodeBeforeSteer/);
  assert.match(agentView, /nextRoundAnchor/);
  assert.match(agentView, /deferredRoundStart/);
  assert.match(agentView, /insertAfterOrAppend/);
  assert.match(agentView, /conversation_id === state\.activeId\s*&& state\.steerDraft/);
  assert.match(agentView, /awaitingSteerRound: hasSteerAfterMessage\(active\.message_id\)/);
  assert.match(agentView, /run\.nextRoundAnchor \|\| run\.deferredRoundStart \|\| run\.awaitingSteerRound/);
  assert.match(agentView, /conversation_id !== state\.activeId\) \{\s*run\.nextRoundAnchor = null;\s*run\.awaitingSteerRound = false;/);
  assert.match(agentView, /clearSavedSteerQueue\(conversation_id\)/);
  assert.doesNotMatch(agentView, /Create a new assistant placeholder for the next round/);
});

test('Agent todo strip has race protection via render token and event filtering', () => {
  // Render token guards against stale async fetches (room switch during fetch)
  assert.match(agentView, /todoRenderToken/);
  assert.match(agentView, /token !== state\.todoRenderToken/);
  // Event handler filters by active conversation to prevent cross-room leaks
  assert.match(agentView, /on\('agent\.todo\.updated'/);
  assert.match(agentView, /conversation_id !== state\.activeId/);
  // Delete button is disabled during RPC to prevent double-clicks
  assert.match(agentView, /btn\.disabled = true/);
  assert.match(agentView, /btn\.disabled = false/);
  // Todos are cleared on conversation delete and create
  assert.match(agentView, /state\.todos = \{ items: \[\], summary/);
});

test('Agent todo strip render function is exported from render module', () => {
  assert.match(agentRender, /export function renderTodoItem/);
  assert.match(agentView, /renderTodoItem/);
});

test('Agent empty thread offers clickable starter prompts', () => {
  assert.match(html, /id="agent-starter-prompts"/);
  assert.match(html, /data-starter-prompt=/);
  assert.match(agentRender, /STARTER_PROMPTS/);
  assert.match(agentRender, /data-starter-prompt/);
});

test('Narrow agent layout can reopen the conversations pane', () => {
  assert.match(html, /id="agent-rooms-toggle"/);
  assert.match(html, /id="agent-rooms-backdrop"/);
  assert.match(agentView, /agent-rooms-toggle/);
  assert.match(agentCSS, /agent-shell\.is-rooms-open/);
});

test('Harbor palette is tokenized instead of acid-lime defaults', async () => {
  const globalCSS = await readFile(new URL('./styles/global.css', import.meta.url), 'utf8');
  assert.match(globalCSS, /--accent:\s*#6ee0c4/);
  assert.doesNotMatch(globalCSS, /#c5f45d/);
  assert.match(appShell, /bindShellShortcuts/);
});
