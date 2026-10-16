const own = (value, key) => Object.prototype.hasOwnProperty.call(value || {}, key);

function runStartedAt(run) {
  for (const value of [run?.startedAt, run?.updatedAt, run?.endedAt]) {
    const timestamp = Date.parse(String(value || ''));
    if (Number.isFinite(timestamp)) return timestamp;
  }
  return 0;
}

// The ACP list endpoint combines live map entries with persisted records, so
// its wire order is intentionally not a presentation contract. Keep the
// switcher newest-first. For legacy records without a usable timestamp, the
// backend's historical list is oldest-first, so the later input entry wins;
// exact timestamp ties use the same insertion-order fallback.
export function sortSubagentViews(runs = []) {
  return runs
    .map((run, index) => ({ run, index, startedAt: runStartedAt(run) }))
    .sort((a, b) => b.startedAt - a.startedAt || b.index - a.index)
    .map(({ run }) => run);
}

function elapsedSince(startedAt, endedAt, now = Date.now()) {
  const start = new Date(startedAt).valueOf();
  const end = endedAt ? new Date(endedAt).valueOf() : now;
  if (!Number.isFinite(start) || !Number.isFinite(end)) return '';
  const seconds = Math.max(0, Math.round((end - start) / 1000));
  return seconds >= 60 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${seconds}s`;
}

function normalizeStatus(value, fallback = 'waiting') {
  const status = String(value || fallback).toLowerCase();
  if (['complete', 'completed', 'done'].includes(status)) return 'complete';
  return status || fallback;
}

export function subagentAgentLabel(subagent) {
  const name = String(subagent?.agentName || subagent?.agentId || '').trim();
  return name ? `ACP · ${name}` : 'ACP';
}

function subagentRunParentToolCallID(run) {
  return String(run?.parent_tool_call_id || run?.parentToolCallId || '').trim();
}

function subagentRunID(run) {
  return String(run?.id || '').trim();
}

function subagentRunAgentID(run) {
  return String(run?.agent_id || run?.agentId || '').trim();
}

function subagentRunAgentName(run) {
  return String(run?.agent_name || run?.agentName || subagentRunAgentID(run) || '').trim();
}

// Persisted conversation messages only retain the ACP id in the spawn tool
// arguments. The ACP run list is the authoritative source for the friendly
// agent name, so merge that metadata into a copied event before rendering.
// Prefer the parent tool call because an agent can have multiple runs in one
// conversation; title/id matching only fills gaps in older lifecycle payloads.
export function enrichSubagentToolEvent(event, runs = []) {
  if (!event || event.type !== 'tool.run' || !['subagent', 'delegate'].includes(event.tool)) return event;
  const args = event.args && typeof event.args === 'object' ? event.args : {};
  const toolCallID = String(event.toolCallId || '').trim();
  const runList = Array.isArray(runs) ? runs : [];
  const matched = runList.find((run) => {
    const parent = subagentRunParentToolCallID(run);
    return parent && toolCallID && parent === toolCallID;
  }) || runList.find((run) => {
    const runID = subagentRunID(run);
    return runID && String(args.id || '').trim() === runID;
  }) || runList.find((run) => {
    const runAgentID = subagentRunAgentID(run);
    const eventAgentID = String(args.agent_id || args.agentId || '').trim();
    const runTitle = String(run?.title || '').trim();
    const eventTitle = String(args.title || '').trim();
    return runAgentID && eventAgentID === runAgentID && runTitle && eventTitle === runTitle;
  });
  if (!matched) return event;

  const agentName = subagentRunAgentName(matched);
  const agentID = subagentRunAgentID(matched) || String(args.agent_id || '').trim();
  if (!agentName) return event;
  if (args.agent_name === agentName && (!agentID || args.agent_id === agentID)) return event;
  return {
    ...event,
    args: {
      ...args,
      agent_name: agentName,
      ...(agentID ? { agent_id: agentID } : {}),
    },
  };
}

// Convert the wire-shaped ACP run into the small view model consumed by the
// dock. `previous` matters for lifecycle updates: started/updated events may
// intentionally omit the transcript while a run is still being assembled.
// Keeping the last complete transcript prevents a partial streamed snapshot
// from blanking a reader's transcript during a remount.
export function toSubagentView(run, previous = null, now = Date.now()) {
  const source = run || {};
  const id = source.id || previous?.id || '';
  if (!id) return null;
  const startedAt = own(source, 'started_at') ? source.started_at : (previous?.startedAt || '');
  const updatedAt = own(source, 'updated_at') ? source.updated_at : (previous?.updatedAt || '');
  const endedAt = own(source, 'ended_at') ? source.ended_at : (previous?.endedAt || '');
  const hasTranscript = own(source, 'transcript') && Array.isArray(source.transcript);
  const transcript = hasTranscript ? source.transcript : (previous?.transcript || []);
  const status = own(source, 'status')
    ? normalizeStatus(source.status, previous?.status || 'waiting')
    : (previous?.status || 'waiting');
  const objective = own(source, 'prompt') && source.prompt
    ? source.prompt
    : (previous?.objective || source.title || '');

  return {
    ...previous,
    id,
    name: source.title || source.agent_name || previous?.name || 'Subagent',
    agentName: source.agent_name || previous?.agentName || '',
    status,
    elapsed: elapsedSince(startedAt, endedAt, now) || previous?.elapsed || '',
    objective,
    transcript,
    conversationId: source.conversation_id || previous?.conversationId || '',
    parentToolCallId: source.parent_tool_call_id || previous?.parentToolCallId || '',
    startedAt,
    updatedAt,
    endedAt,
    agentId: source.agent_id || previous?.agentId || '',
    currentModeId: source.current_mode_id || previous?.currentModeId || '',
    currentModelId: source.current_model_id || previous?.currentModelId || '',
    pendingPermission: own(source, 'pending_permission')
      ? source.pending_permission
      : (previous?.pendingPermission || null),
    queuedSteer: own(source, 'queued_steer') ? source.queued_steer : (previous?.queuedSteer || ''),
  };
}

export function acceptSubagentStreamFrame(cursor, frame, conversationID) {
  if (!cursor || !frame || typeof frame !== 'object' || !conversationID) return null;
  if (frame.type === 'acp.run.snapshot') {
    const seq = Number(frame.seq);
    if (!Number.isSafeInteger(seq) || seq < 0 || frame.conversation_id !== conversationID) return null;
    cursor.lastSeq = seq;
    const runs = Array.isArray(frame.runs)
      ? frame.runs.filter((run) => run?.id && run.conversation_id === conversationID)
      : [];
    return { kind: 'snapshot', seq, runs };
  }
  if (!['acp.run.started', 'acp.run.updated', 'acp.run.done'].includes(frame.type)) return null;
  const seq = Number(frame.seq);
  const run = frame.run;
  if (!Number.isSafeInteger(seq) || seq <= (Number(cursor.lastSeq) || 0) || !run?.id || run.conversation_id !== conversationID) return null;
  cursor.lastSeq = seq;
  return { kind: 'update', seq, run };
}
