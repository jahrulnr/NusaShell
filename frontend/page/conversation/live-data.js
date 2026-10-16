function clock(value) {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? '' : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function formatTokens(value) {
  const count = Number(value) || 0;
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}m`;
  if (count >= 1_000) return `${(count / 1_000).toFixed(1)}k`;
  return String(count);
}

function usageLabel(usage) {
  if (!usage) return '';
  const parts = [`↑ ${formatTokens(usage.input_tokens)}  ↓ ${formatTokens(usage.output_tokens)}`];
  if (Number(usage.cache_write) > 0) parts.push(`CW ${formatTokens(usage.cache_write)}`);
  if (Number(usage.cache_read) > 0) parts.push(`CR ${formatTokens(usage.cache_read)}`);
  return parts.join('  ·  ');
}

function usageTitle(usage) {
  if (!usage) return '';
  const parts = [`Input ${formatTokens(usage.input_tokens)}`, `Output ${formatTokens(usage.output_tokens)}`];
  if (Number(usage.cache_write) > 0) parts.push(`Cache write ${formatTokens(usage.cache_write)}`);
  if (Number(usage.cache_read) > 0) parts.push(`Cache read ${formatTokens(usage.cache_read)}`);
  return parts.join(' · ');
}

export function formatModelLabel(model, catalog = []) {
  const source = model && typeof model === 'object' ? model : { id: String(model || '').trim() };
  const rawID = String(source.id || source.model || '').trim();
  const providerID = String(source.provider_id || '').trim();
  const qualified = providerID && rawID
    ? `${providerID}:${rawID}`
    : rawID;
  const entry = Array.isArray(catalog)
    ? catalog.find((candidate) => {
      const candidateQualified = candidate?.provider_id && candidate?.id
        ? `${candidate.provider_id}:${candidate.id}`
        : '';
      // A bare model id is not globally unique (OpenAI and Codex both expose
      // gpt-5.6-sol). Once the source carries a provider, only the qualified
      // key is authoritative; falling back to the first bare-id match silently
      // rewrites the selected provider in the composer label.
      if (providerID) return candidateQualified === qualified;
      return candidateQualified === qualified
        || candidate?.id === rawID
        || candidate?.display_name === rawID;
    })
    : null;
  const provider = String(entry?.provider_name || source.provider_name || source.provider_id || qualified.split(':', 1)[0] || '').trim();
  const name = String(entry?.id || (qualified.includes(':') ? qualified.slice(qualified.indexOf(':') + 1) : rawID)).trim();
  if (!provider) return name;
  if (!name) return provider;
  return `${provider}/${name}`;
}

const MAX_REASONING_SUMMARY = 120;

function reasoningSummary(content) {
  const firstLine = String(content || '').split('\n').map((line) => line.trim()).find(Boolean) || '';
  if (!firstLine) return 'Reasoning';
  if (firstLine.length <= MAX_REASONING_SUMMARY) return firstLine;
  return `${firstLine.slice(0, MAX_REASONING_SUMMARY - 1).trimEnd()}…`;
}

function argsText(args) {
  if (args == null || args === '') return '';
  if (typeof args === 'string') {
    try { return JSON.stringify(JSON.parse(args), null, 2); } catch { return args; }
  }
  return JSON.stringify(args, null, 2);
}

function parsedArgs(args) {
  if (args && typeof args === 'object') return args;
  try { return JSON.parse(args || '{}'); } catch { return {}; }
}

function assistantStatus(value) {
  const normalized = String(value || '').trim().toLowerCase();
  if (['failed', 'fail', 'error'].includes(normalized)) return 'error';
  if (['cancelled', 'canceled', 'interrupted'].includes(normalized)) return 'interrupted';
  if (['complete', 'completed', 'done', 'ok'].includes(normalized)) return 'done';
  return normalized;
}

function compactError(value) {
  const normalized = String(value || '')
    .replace(/^provider returned HTTP \d+:\s*/i, '')
    .replace(/\s+/g, ' ')
    .trim();
  if (normalized.length <= 120) return normalized;
  return `${normalized.slice(0, 119).trimEnd()}…`;
}

export function providerRetryLabel({ attempt = 0, max_attempts: maxAttempts = 0, delay_ms: delayMS = 0, error = '' } = {}) {
  const progress = Number(attempt) > 0 && Number(maxAttempts) > 0 ? ` (${attempt}/${maxAttempts})` : '';
  const milliseconds = Number(delayMS) || 0;
  const seconds = milliseconds > 0 ? milliseconds / 1000 : 0;
  const delay = seconds > 0 ? ` in ${Number.isInteger(seconds) ? seconds : seconds.toFixed(1)}s` : '';
  const reason = compactError(error);
  return `Retrying request${progress}${delay}${reason ? ` — ${reason}` : ''}`;
}

function isSubagentBookkeeping(tool) {
  const name = tool?.name || '';
  if (['subagent_steer', 'subagent_stop', 'subagent_wait', 'subagent_result', 'delegate_result'].includes(name)) return true;
  if (name !== 'subagent' && name !== 'delegate') return false;
  return (parsedArgs(tool.args).op || 'spawn') !== 'spawn';
}

function reasoningEvent(message, content, suffix = 'reasoning') {
  const normalized = String(content || '').trim();
  return {
    id: `${message.id}-${suffix}`,
    messageId: message.id,
    type: 'agent.reasoning',
    duration: '',
    summary: reasoningSummary(normalized),
    details: normalized ? [normalized] : [],
  };
}

function toolEvent(message, tool, index) {
  return {
    id: `${message.id}-tool-${tool.id || index}`,
    messageId: message.id,
    type: 'tool.run',
    tool: tool.name || 'tool',
    toolCallId: tool.id || '',
    runId: message.run_id || message.runId || '',
    args: parsedArgs(tool.args),
    command: argsText(tool.args),
    output: tool.output || '',
    status: tool.status || 'complete',
    duration: '',
    presentation: tool.presentation || null,
  };
}

// A re-attached round stream replays the round's deltas, so each persisted
// step can exist twice after a reload: once as its canonical <messageId>-*
// snapshot row and once as the live-<messageId>-* twin the replay rebuilds.
// The live event owns the row from its first replayed frame; the caller evicts
// the canonical copy (event and mounted widget) before the twin renders
// alongside it. Snapshot merges then fold the persisted row into the live
// event via the existing live-id/semantic lookup.
export function canonicalTwins(events, liveEvent) {
  const messageId = liveEvent?.messageId || '';
  if (!Array.isArray(events) || !messageId || !String(liveEvent?.id || '').startsWith('live-')) return [];
  return events.filter((candidate) => {
    if (!candidate || String(candidate.id || '').startsWith('live-')) return false;
    if (candidate.messageId !== messageId || candidate.type !== liveEvent.type) return false;
    return liveEvent.type !== 'tool.run' || (candidate.toolCallId || '') === (liveEvent.toolCallId || '');
  });
}

export function applyAskStatus(events, payload, nextStatus) {
  const event = events.find((candidate) => candidate.tool === 'ask_question' && candidate.toolCallId === payload?.tool_call_id);
  if (!event) return null;
  event.status = nextStatus;
  if (payload?.answer) event.output = payload.answer;
  if (payload?.reason && !event.output) event.output = payload.reason;
  return event;
}

function answerEvent(message, content, suffix = 'answer', catalog = [], canRetry = false, fallbackProviderID = '') {
  return {
    id: `${message.id}-${suffix}`,
    messageId: message.id,
    type: 'agent.message',
    time: clock(message.created_at),
    model: formatModelLabel({ ...message, id: message.model || '', provider_id: message.provider_id || fallbackProviderID }, catalog),
    usage: usageLabel(message.usage),
    usageTitle: usageTitle(message.usage),
    markdown: String(content || ''),
    status: assistantStatus(message.status),
    error: String(message.error || ''),
    canRetry: Boolean(canRetry),
  };
}

export function messageEvents(messages = [], options = {}) {
  const catalog = Array.isArray(options) ? options : options.models || [];
  const fallbackProviderID = Array.isArray(options) ? '' : (options.providerID || options.provider_id || '');
  let lastAssistantIndex = -1;
  messages.forEach((message, index) => {
    if (message?.role === 'assistant') lastAssistantIndex = index;
  });
  const result = [];
  for (const [messageIndex, message] of messages.entries()) {
    if (message.role === 'user') {
      const content = String(message.content || '');
      // Compaction summaries are stored with role=user so they join the
      // provider request, but the production UI renders them as the
      // collapsed assistant-style "Compacted context" handover marker.
      if (content.startsWith('[COMPACTION CHECKPOINT]')) {
        result.push({
          id: message.id,
          messageId: message.id,
          type: 'agent.reasoning',
          compaction: true,
          duration: '',
          summary: 'Compacted context',
          details: [content.slice('[COMPACTION CHECKPOINT]'.length).trimStart()],
        });
        continue;
      }
      result.push({
        id: message.id,
        type: 'user.message',
        time: clock(message.created_at),
        createdAt: message.created_at || '',
        content: message.content || '',
        attachments: Array.isArray(message.attachments) ? message.attachments : [],
        steer: Boolean(message.steer),
      });
      continue;
    }
    if (message.role !== 'assistant') continue;
    const failed = assistantStatus(message.status) === 'error' || Boolean(String(message.error || '').trim());
    const canRetry = failed && messageIndex === lastAssistantIndex;
    const steps = Array.isArray(message.steps) && message.steps.length ? message.steps : null;
    if (steps) {
      let hasAnswer = false;
      steps.forEach((step, stepIndex) => {
        if (step.type === 'reasoning' && step.content) result.push(reasoningEvent(message, step.content, `reasoning-${stepIndex}`));
        if (step.type === 'tool_calls') (step.tool_calls || []).forEach((tool, toolIndex) => {
          if (!isSubagentBookkeeping(tool)) result.push(toolEvent(message, tool, `${stepIndex}-${toolIndex}`));
        });
        if (step.type === 'text' && step.content) {
          result.push(answerEvent(message, step.content, `answer-${stepIndex}`, catalog, canRetry, fallbackProviderID));
          hasAnswer = true;
        }
      });
      if (failed && !hasAnswer) result.push(answerEvent(message, message.content, 'answer', catalog, canRetry, fallbackProviderID));
      continue;
    }
    if (message.reasoning) result.push(reasoningEvent(message, message.reasoning));
    (message.tool_calls || []).forEach((tool, index) => {
      if (!isSubagentBookkeeping(tool)) result.push(toolEvent(message, tool, index));
    });
    if (message.content || failed) result.push(answerEvent(message, message.content, 'answer', catalog, canRetry, fallbackProviderID));
  }
  return result;
}

export function applyRoundDelta(events, frame, context = {}) {
  const prefix = `live-${context.messageId || 'pending'}`;
  if (frame.kind === 'reasoning') {
    let event = events.find((candidate) => candidate.id === `${prefix}-reasoning`);
    if (!event) {
      event = { id: `${prefix}-reasoning`, messageId: context.messageId || '', type: 'agent.reasoning', duration: '', summary: '', details: [] };
      events.push(event);
    }
    const content = `${event.details?.[0] || ''}${frame.text || ''}`;
    event.summary = reasoningSummary(content);
    event.details = content ? [content] : [];
  }
  if (frame.kind === 'tool') {
    if (isSubagentBookkeeping({ name: frame.name, args: frame.args })) return events;
    const id = `${prefix}-tool-${frame.tool_call_id || 'tool'}`;
    let event = events.find((candidate) => candidate.id === id);
    if (!event) {
      event = { id, messageId: context.messageId || frame.message_id || '', type: 'tool.run', tool: frame.name || 'tool', toolCallId: frame.tool_call_id || '', runId: context.runId || frame.run_id || '', args: parsedArgs(frame.args), command: argsText(frame.args), output: '', status: 'running', duration: '', presentation: frame.presentation || null };
      events.push(event);
    }
    if (context.messageId || frame.message_id) event.messageId = context.messageId || frame.message_id;
    if (context.runId || frame.run_id) event.runId = context.runId || frame.run_id;
    if (frame.presentation) event.presentation = frame.presentation;
    event.output += frame.text || '';
  }
  if (frame.kind === 'text') {
    let event = events.find((candidate) => candidate.id === `${prefix}-answer`);
    if (!event) {
      event = { id: `${prefix}-answer`, messageId: context.messageId || '', type: 'agent.message', time: '', model: formatModelLabel(context.model, context.models || []), usage: '', usageTitle: '', markdown: '', status: 'running', error: '', canRetry: false };
      events.push(event);
    }
    event.markdown += frame.text || '';
  }
  return events;
}

export function applyRoundDone(events, messageId, done = {}) {
  const prefix = `live-${messageId}-`;
  const updated = events.filter((event) => event.id.startsWith(prefix));
  const terminalStatus = done.state === 'error' ? 'failed'
    : done.state === 'interrupted' ? 'interrupted'
      : 'complete';
  for (const event of updated) {
    if (event.type === 'tool.run' && event.status === 'running') event.status = terminalStatus;
    if (event.type === 'agent.message') {
      event.status = done.state === 'error' ? 'error' : event.status;
      if (done.state === 'error') {
        event.error = done.error || event.error || 'Turn failed';
        event.canRetry = true;
      }
      if (done.usage) {
        event.usage = usageLabel(done.usage);
        event.usageTitle = usageTitle(done.usage);
      }
    }
  }
  if (done.state === 'error' && messageId && !updated.some((event) => event.type === 'agent.message')) {
    const event = {
      id: `${prefix}answer`,
      messageId,
      type: 'agent.message',
      time: '',
      model: '',
      usage: done.usage ? usageLabel(done.usage) : '',
      usageTitle: done.usage ? usageTitle(done.usage) : '',
      markdown: '',
      status: 'error',
      error: done.error || 'Turn failed',
      canRetry: true,
    };
    events.push(event);
    updated.push(event);
  }
  return updated;
}

export function applyTurnError(events, payload = {}, context = {}) {
  const messageId = payload.message_id || payload.messageId || '';
  if (!messageId || !Array.isArray(events)) return null;
  let event = events.find((candidate) => candidate.type === 'agent.message' && candidate.messageId === messageId)
    || events.find((candidate) => candidate.id === `live-${messageId}-answer`);
  if (!event) {
    event = {
      id: `live-${messageId}-answer`,
      messageId,
      type: 'agent.message',
      time: '',
      model: formatModelLabel(context.model, context.models || []),
      usage: '',
      markdown: '',
      status: 'error',
      error: payload.message || 'Turn failed',
      canRetry: true,
    };
    events.push(event);
    return event;
  }
  event.status = 'error';
  event.error = payload.message || event.error || 'Turn failed';
  if (!event.model && context.model) event.model = formatModelLabel(context.model, context.models || []);
  event.canRetry = true;
  return event;
}

export function modelOptions(models = []) {
  const groups = new Map();
  for (const model of models) {
    if (model.kind && model.kind !== 'chat') continue;
    const provider = model.provider_name || model.provider_id || 'Provider';
    if (!groups.has(provider)) groups.set(provider, []);
    const name = String(model.display_name || model.id);
    const escape = (value) => value.replace(/[&<>"']/g, (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));
    // Paths from the existing vendored Lucide Type, Image, Mic and Video icons.
    const modes = [['Text', '<path d="M12 4v16"/><path d="M4 7V5a1 1 0 0 1 1-1h14a1 1 0 0 1 1 1v2"/><path d="M9 20h6"/>']];
    if (model.vision) modes.push(['Vision input', '<rect width="18" height="18" x="3" y="3" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>']);
    if (model.audio) modes.push(['Audio input', '<path d="M12 19v3"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><rect x="9" y="2" width="6" height="13" rx="3"/>']);
    if (model.video) modes.push(['Video input', '<path d="m16 13 5.223 3.482a.5.5 0 0 0 .777-.416V7.87a.5.5 0 0 0-.752-.432L16 10.5"/><rect x="2" y="6" width="14" height="12" rx="2"/>']);
    const badges = modes.map(([label, paths]) => `<span class="model-modality" role="img" title="${label}" aria-label="${label}"><svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths}</svg></span>`).join('');
    groups.get(provider).push({ text: name, html: `<span class="model-option-copy"><span class="model-option-name">${escape(name)}</span><small class="model-option-id">${escape(String(model.id))}</small></span><span class="model-modalities">${badges}</span>`, value: `${model.provider_id}:${model.id}` });
  }
  return [...groups].map(([label, options]) => ({ label, options }));
}

export function workspaceName(path) {
  const normalized = String(path || '').replace(/[\\/]+$/, '');
  return normalized.split(/[\\/]/).filter(Boolean).at(-1) || path || 'Home';
}
