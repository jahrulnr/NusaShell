import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const parityCSS = await readFile(new URL('./styles/parity.css', import.meta.url), 'utf8');
const agentCSS = await readFile(new URL('./styles/agent.css', import.meta.url), 'utf8');
const agentView = await readFile(new URL('./js/views/agent.js', import.meta.url), 'utf8');
const appShell = await readFile(new URL('./js/app.js', import.meta.url), 'utf8');
const agentRender = await readFile(new URL('./js/views/agent/render.js', import.meta.url), 'utf8');
const agentComposer = await readFile(new URL('./js/views/agent/composer.js', import.meta.url), 'utf8');
const agentModelPicker = await readFile(new URL('./js/views/agent/model-picker.js', import.meta.url), 'utf8');

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
  const tabletRules = parityCSS.slice(
    parityCSS.indexOf('@media (max-width: 900px)'),
    parityCSS.indexOf('@media (max-width: 760px)'),
  );
  assert.match(tabletRules, /\.agent-conversations \{ display: none; \}/);
});

test('Agent tool transcripts start collapsed, cap output at ten lines, and lazy-render reasoning', () => {
  assert.match(agentRender, /class: 'agent-tool-terminal'/);
  assert.match(agentRender, /el\('details', \{ class: 'agent-tool-terminal'/);
  assert.match(agentRender, /function materializeReasoning/);
  assert.doesNotMatch(agentRender, /content\.innerHTML = renderMarkdown\(reasoning\)/);
  assert.doesNotMatch(agentView, /content\.innerHTML = renderMarkdown\(run\.rawReasoning\)/);
  assert.match(parityCSS, /\.agent-tool-terminal-output \{[^}]*max-height: calc\(10 \* 1\.55em\);[^}]*overflow-y: auto;/s);
});

test('Archived chunks load only on explicit Load older or scroll-to-top, never after turn.done or live compaction', () => {
  // Proactive maybeLoadOlderChunk after refresh/compaction re-inflated the
  // just-archived long turn into the DOM and froze the thread.
  assert.doesNotMatch(agentView, /maybeLoadOlderChunk/);
  assert.match(agentView, /function revealOlderHistory/);
  assert.match(agentView, /hasOlderActiveMessages\(\) \|\| state\.nextChunkIndex >= 0/);
});

test('Live turns keep every round mounted; perf comes from content-visibility + targeted enhancement, not DOM removal', () => {
  // The hide-bubble workaround (park rounds, "N earlier rounds trimmed"
  // stub) is removed — it traded UX away for performance.
  assert.doesNotMatch(agentRender, /KEEP_VISIBLE_ROUNDS|MAX_PARKED_LIVE_ROUNDS|parkLiveRound|restoreParkedLiveRounds|pruneOverflowLiveRounds|agent-round-stub/);
  assert.match(agentRender, /export function mountLiveRound/);
  // Snapshot history windowing keeps its own constant (agent.js), still
  // backed by the conversation tail + explicit Load older.
  assert.match(agentView, /SNAPSHOT_KEEP_ROUNDS = 12/);
  assert.doesNotMatch(agentView, /KEEP_VISIBLE_ROUNDS/);
  // The browser-level guard rails that replace the trimming: per-delta
  // enhancement only touches newly rendered blocks (incrementalRender).
  // No CSS containment on .agent-round — content-visibility there clipped
  // the reasoning mark / tool-terminal chrome drawn at the round edges.
  assert.doesNotMatch(agentCSS, /\.agent-round \{[^}]*content-visibility/s);
  assert.doesNotMatch(parityCSS, /\.agent-round \{[^}]*content-visibility/s);
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
  assert.doesNotMatch(parityCSS, /agent-round-stub/);
  assert.match(html, /id="agent-provider-status" aria-live="polite" aria-atomic="true"/);
  assert.match(agentView, /conversationTail\(/);
  assert.doesNotMatch(agentRender, /function earlierRoundsDisclosure/);
  // Reloading a running turn must keep every persisted round of that turn.
  // Snapshot keepRounds would otherwise hide older tool rounds / announcements
  // and Load older used to refuse to reveal them while a run was attached.
  assert.match(agentView, /keepAllTrailing: keepLiveTurnMounted\(\)/);
  assert.match(agentView, /function keepLiveTurnMounted/);
  assert.match(agentView, /hasOlderTurnRounds\(\)/);
  assert.doesNotMatch(agentView, /hasOlderTurnRounds\(\) && !runForConversation/);
});

test('Agent delegates presentation, composer, and model picker responsibilities to focused modules', () => {
  assert.match(agentView, /from '\.\/agent\/render\.js'/);
  assert.match(agentView, /from '\.\/agent\/composer\.js'/);
  assert.match(agentView, /from '\.\/agent\/model-picker\.js'/);
  assert.match(agentComposer, /export function bindComposer/);
  assert.match(agentModelPicker, /export function bindModelPicker/);
});

test('Thinking and tool markers share the same conversation rail', () => {
  assert.match(parityCSS, /\.agent-reasoning summary \{[^}]*margin-left: -12px;/s);
  assert.match(parityCSS, /\.agent-tool-terminal summary \{[^}]*margin-left: -12px;/s);
});

test('Agent live motion respects reduced-motion preferences', () => {
  assert.match(agentCSS, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(parityCSS, /animation-duration: \.001ms !important/);
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
  assert.match(parityCSS, /agent-shell\.is-rooms-open/);
});

test('Harbor palette is tokenized instead of acid-lime defaults', async () => {
  const globalCSS = await readFile(new URL('./styles/global.css', import.meta.url), 'utf8');
  assert.match(globalCSS, /--accent:\s*#6ee0c4/);
  assert.doesNotMatch(globalCSS, /#c5f45d/);
  assert.match(appShell, /bindShellShortcuts/);
});
