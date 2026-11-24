// Turn projection: websocket frames → ordered segments, and persisted
// history → the same segment shapes, so the transcript renders live and
// stored turns through one path.
//
// Segment kinds: text | reasoning | tool | steer | user | approval | plan | elicitation | resync.

export function newTurn({ turnId, requestId, conversationId, kind, startedAt }) {
  return {
    turnId, requestId, conversationId, kind,
    lastSeq: -1,
    segments: [],
    phase: null,          // { phase, retryAfterMs, at }
    usage: null,
    startedAt: startedAt || Date.now(),
    terminal: null,       // the terminal frame once it arrives
    n: 0,
  };
}

const last = (t) => t.segments[t.segments.length - 1];

function settleTools(t, why, definitive = false) {
  for (const s of t.segments) {
    if (s.kind !== 'tool' || s.status === 'success' || s.status === 'failed') continue;
    // A call that produced no result before the turn ended did not
    // succeed; an empty successful call gets an explicit tool_result and
    // never reaches this fallback. Model activity can resume while an
    // agent tool is still running, so only a step/turn end may fail it.
    if (!s.output && (s.status === 'requested' || s.status === 'streaming')) {
      if (!definitive) continue;
      s.status = 'failed';
    } else {
      s.status = toolFailed(s.output) ? 'failed' : 'success';
    }
    s.settled = why;
  }
}

// applyFrame mutates `t`; replayed or doubled seq numbers are dropped.
export function applyFrame(t, f) {
  if (typeof f.seq === 'number') {
    if (f.seq <= t.lastSeq) return false;
    t.lastSeq = f.seq;
  }
  const p = f.payload || {};
  if (f.type !== 'turn.phase' && f.type !== 'turn.steer_ack' && f.type !== 'turn.steer_recalled') {
    if (t.phase && t.phase.phase !== 'reasoning') t.phase = null;
  }
  switch (f.type) {
    case 'turn.opened':
      // The server's admission clock — a watcher replays this frame and
      // adopts it, so the working timer survives the reattach.
      if (p.startedAt) t.startedAt = p.startedAt;
      break;
    case 'turn.delta': {
      const s = last(t);
      if (s?.kind === 'text') s.text += p.text || '';
      else { settleTools(t, 'Model activity resumed'); t.segments.push({ id: `${t.turnId}:${++t.n}`, kind: 'text', text: p.text || '' }); }
      if (t.phase?.phase === 'reasoning') t.phase = null;
      break;
    }
    case 'turn.reasoning': {
      const s = last(t);
      if (s?.kind === 'reasoning') s.text += p.text || '';
      else t.segments.push({ id: `${t.turnId}:${++t.n}`, kind: 'reasoning', text: p.text || '' });
      break;
    }
    case 'turn.phase':
      t.phase = { phase: p.phase, retryAfterMs: p.retryAfterMs, at: Date.now() };
      break;
    case 'turn.tool_call':
      t.segments.push({ id: `${t.turnId}:${++t.n}`, kind: 'tool', callId: p.callId, name: p.name, input: '', output: '', status: 'requested' });
      if (t.phase?.phase === 'reasoning') t.phase = null;
      break;
    case 'turn.tool_input': {
      const s = t.segments.findLast((x) => x.kind === 'tool' && x.callId === p.callId);
      if (s) s.input += p.text || '';
      break;
    }
    case 'turn.tool_output': {
      const s = t.segments.findLast((x) => x.kind === 'tool' && x.callId === p.callId);
      if (s) {
        s.output += p.text || '';
        // A late chunk after a terminal result is still data, but it must
        // not put a finished row back into the running state.
        if (s.status !== 'success' && s.status !== 'failed') s.status = 'streaming';
      }
      break;
    }
    case 'turn.tool_result': {
      const s = t.segments.findLast((x) => x.kind === 'tool' && x.callId === p.callId);
      if (s) {
        s.status = p.failed ? 'failed' : 'success';
        s.settled = 'Tool finished';
        // The media the call showed the model, listed as history lists it.
        if (p.attachments?.length) s.attachments = p.attachments;
      }
      break;
    }
    case 'turn.steer': {
      const s = last(t);
      if (s?.kind === 'steer' && !s.final && s.inputId === p.inputId) s.text += p.text || '';
      else { settleTools(t, 'Model activity resumed'); t.segments.push({ id: `${t.turnId}:${++t.n}`, kind: 'steer', inputId: p.inputId, text: p.text || '', attachments: p.attachments || [] }); }
      if (p.final) last(t).final = true;
      break;
    }
    case 'turn.plan': {
      // Each plan update is the whole plan: it replaces the last one.
      const s = t.segments.find((x) => x.kind === 'plan');
      if (s) s.entries = p.entries || [];
      else t.segments.push({ id: `${t.turnId}:plan`, kind: 'plan', entries: p.entries || [] });
      break;
    }
    case 'turn.media':
      // A picture, sound, or file the agent put in its reply, where it came.
      t.segments.push({ id: `${t.turnId}:${++t.n}`, kind: 'media', attachments: [p] });
      break;
    case 'turn.approval':
      // A permission question from the running agent. It stays in the
      // transcript after it is answered, as a record of the decision.
      t.segments.push({ id: `${t.turnId}:apr:${p.id}`, kind: 'approval', request: p, status: 'pending', optionId: '' });
      break;
    case 'turn.approval_resolved': {
      const s = t.segments.findLast((x) => x.kind === 'approval' && x.request.id === p.id);
      if (s) { s.status = p.optionId ? 'answered' : 'withdrawn'; s.optionId = p.optionId || ''; s.answering = false; }
      break;
    }
    case 'turn.elicitation':
      // The agent asks for input: a form or a URL to visit. Only the
      // outcome stays — what the owner typed is never kept in history.
      t.segments.push({ id: `${t.turnId}:eli:${p.id}`, kind: 'elicitation', request: p, status: 'pending', action: '' });
      break;
    case 'turn.elicitation_resolved': {
      const s = t.segments.findLast((x) => x.kind === 'elicitation' && x.request.id === p.id);
      if (s) { s.status = p.action ? 'answered' : 'withdrawn'; s.action = p.action || ''; s.answering = false; }
      break;
    }
    case 'turn.step_finished':
      t.usage = p.usage || t.usage;
      if (p.finishReason !== 'tool_calls') settleTools(t, 'Step ended', true);
      break;
    case 'turn.completed':
    case 'turn.failed':
      if (f.type === 'turn.failed') {
        // A failed/cancelled turn leaves any call without a terminal
        // result unsettled; no output wording can make it a success.
        for (const s of t.segments) {
          if (s.kind === 'tool' && s.status !== 'success' && s.status !== 'failed') {
            s.status = 'failed';
            s.settled = 'Turn failed';
          }
        }
      } else {
        settleTools(t, 'Turn completed', true);
      }
      for (const s of t.segments) if ((s.kind === 'approval' || s.kind === 'elicitation') && s.status === 'pending') s.status = 'withdrawn';
      t.terminal = f;
      t.phase = null;
      break;
  }
  return true;
}

// snapshotSegments replaces a live turn's segments with the bounded
// snapshot a gapped turn.watch got inside resync_required: the server's
// own shadow of what streamed, already folded to segment shapes. Live
// frames keep appending after it — the tail parts merge the same way
// streamed chunks do. A resync marker prepended by the caller states
// honestly when the bound trimmed earlier output.
export function snapshotSegments(t, parts) {
  t.segments = [];
  for (const p of parts || []) {
    const id = `${t.turnId}:snap:${++t.n}`;
    switch (p.kind) {
      case 'text':
        t.segments.push({ id, kind: 'text', text: p.text || '' });
        break;
      case 'reasoning':
        t.segments.push({ id, kind: 'reasoning', text: p.text || '' });
        break;
      case 'tool':
        t.segments.push({ id, kind: 'tool', callId: p.callId, name: p.name, input: p.input || '', output: p.output || '', status: p.status || 'requested' });
        break;
      case 'steer':
        t.segments.push({ id, kind: 'steer', inputId: p.inputId, text: p.text || '', final: !!p.final, attachments: p.attachments || [] });
        break;
      case 'plan':
        t.segments.push({ id: `${t.turnId}:plan`, kind: 'plan', entries: p.entries || [] });
        break;
      case 'media':
        t.segments.push({ id, kind: 'media', attachments: p.attachments || [] });
        break;
      case 'approval':
        t.segments.push({ id: `${t.turnId}:apr:${p.request?.id ?? t.n}`, kind: 'approval', request: p.request || {}, status: p.status || 'pending', optionId: p.optionId || '' });
        break;
      case 'elicitation':
        t.segments.push({ id: `${t.turnId}:eli:${p.request?.id ?? t.n}`, kind: 'elicitation', request: p.request || {}, status: p.status || 'pending', action: p.action || '' });
        break;
    }
  }
}

// splitReplyQuote reads the head a chat reply carries: appchannel puts what
// the message replies to on one line ahead of its text, after the sender's
// name in a group — "Ana: [In reply to: shall we?]" — and the text follows
// on the next line. It answers { sender, quote, head, body }, head being the
// length of that line with its break, or null for any other message.
export function splitReplyQuote(text) {
  const m = /^([^\r\n]*?: )?\[In reply to: ([^\r\n]*)\](?:\r?\n|$)/.exec(text || '');
  if (!m) return null;
  return { sender: (m[1] || '').replace(/: $/, ''), quote: m[2], head: m[0].length, body: text.slice(m[0].length) };
}

// splitRelay reads the head appcontrol puts on a prompt one thread sends
// another (application/control relayHead): "[From thread @label Title
// (c_id)]" and the line telling the receiver how to answer, or "[From
// another thread]" when the sender could not be named. It answers
// { label, title, id, head, body }, head being the length of what it read,
// or null for any other message.
export function splitRelay(text) {
  const m = /^\[From (?:thread (.*?) \((c_[A-Za-z0-9]+)\)|another thread)\](?:\r?\n\[Your answer here does not reach that thread\.[^\r\n]*\])?(?:\r?\n){0,2}/.exec(text || '');
  if (!m) return null;
  const [, label = '', title = ''] = /^(@\S+)?\s*(.*)$/.exec(m[1] || '');
  return { label, title, id: m[2] || '', head: m[0].length, body: text.slice(m[0].length) };
}

// A tool's stored output signals failure by convention.
export function toolFailed(output) {
  const o = String(output || '');
  return /^error:/i.test(o) || /^failed$/i.test(o.trim()) || /interrupted by user/i.test(o)
    || /\(exit [1-9]\d*\)\s*$/.test(o) || /\(signal: [^)]+\)\s*$/.test(o);
}

// liveHistory trims a transcript list to what the live turn still leaves to
// history. A resumed turn re-answers the newest prompt and its live tail
// replaces the stored answers after it, so history stops at that prompt — but
// only for a retry this tab started, which read that prompt from the room
// before the turn: an adopted one cannot tell the turn's prompt from an older
// message a stale page still ends on. An edit replaces the message it names,
// so history stops ahead of it. A send trims nothing: its prompt is the newest
// message itself, and history before the turn is content no stream reproduces
// — trimming at "the last prompt in history" cut away the answer in front of a
// turn whose own prompt had not been read yet.
export function liveHistory(items, live) {
  if (!live) return items;
  if (live.editOf) {
    const at = items.findIndex((x) => x.id === live.editOf);
    return at < 0 ? items : items.slice(0, at);
  }
  if (live.kind === 'resume' && !live.adopted) {
    const at = items.findLastIndex((x) => x.kind === 'user');
    return at < 0 ? items : items.slice(0, at + 1);
  }
  return items;
}

// historyItems flattens persisted messages into transcript items:
// { kind: 'user', msg } | { kind: 'note', msg } | segment-like items.
export function historyItems(messages, cwd = '') {
  const out = [];
  for (const m of messages) {
    if (m.role === 'user') {
      out.push({ id: m.id, kind: m.userType === 'system' ? 'note' : 'user', msg: m });
      continue;
    }
    if (m.role !== 'assistant') continue;
    const calls = new Map((m.toolCalls || []).map((c) => [c.id, c]));
    const parts = m.parts?.length ? m.parts : [
      ...(m.content ? [{ kind: 'text', text: m.content }] : []),
      ...(m.toolCalls || []).map((c) => ({ kind: 'tool_call', toolCallId: c.id })),
    ];
    let i = 0;
    for (const part of parts) {
      const id = `${m.id}:${i++}`;
      if (part.kind === 'text' && part.text) out.push({ id, kind: 'text', text: part.text });
      else if (part.kind === 'reasoning' && part.text) out.push({ id, kind: 'reasoning', text: part.text, done: true });
      else if (part.kind === 'plan') {
        try { out.push({ id, kind: 'plan', entries: JSON.parse(part.text) || [] }); } catch { /* unreadable plan */ }
      } else if (part.kind === 'approval') {
        try {
          const a = JSON.parse(part.text);
          out.push({ id, kind: 'approval', request: a, status: a.optionId ? 'answered' : 'withdrawn', optionId: a.optionId || '' });
        } catch { /* unreadable record */ }
      } else if (part.kind === 'media') {
        try { out.push({ id, kind: 'media', attachments: [JSON.parse(part.text)] }); } catch { /* unreadable record */ }
      } else if (part.kind === 'steer') {
        out.push({ id, kind: 'steer', text: part.text || '', attachments: part.attachments || [] });
      } else if (part.kind === 'elicitation') {
        try {
          const e = JSON.parse(part.text);
          out.push({ id, kind: 'elicitation', request: e, status: e.action ? 'answered' : 'withdrawn', action: e.action || '' });
        } catch { /* unreadable record */ }
      }
      else if (part.kind === 'tool_call') {
        const c = calls.get(part.toolCallId);
        if (!c) continue;
        // The workspace injection records where later tools run.
        if (c.type === 'injection') {
          if (c.name === 'workspace') { try { cwd = JSON.parse(c.input).path || cwd; } catch { /* keep */ } }
          continue;
        }
        out.push({
          id: `${m.id}:${c.id}`, kind: 'tool', callId: c.id, name: c.name, input: c.input || '', output: c.output || '',
          status: c.status === 'pending' ? 'pending' : c.status === 'failed' ? 'failed' : 'settled',
          calledAt: c.calledAt, stored: true, cwd, attachments: c.attachments || [],
        });
      }
    }
    if (m.finishReason === 'interrupted') out.push({ id: `${m.id}:int`, kind: 'interrupted' });
    // A reply that went out to a chat carries what was sent with it and
    // how far the chat confirmed it.
    if (m.sentFiles?.length || m.receipt) out.push({ id: `${m.id}:sent`, kind: 'delivery', files: m.sentFiles || [], receipt: m.receipt || '' });
  }
  return out;
}

// group folds consecutive reasoning/tool items into one activity item so
// the transcript reads as prose with collapsible "work" between.
export function group(items) {
  const out = [];
  let cur = null;
  for (const it of items) {
    if (it.kind === 'reasoning' || it.kind === 'tool') {
      if (!cur) { cur = { id: 'g:' + it.id, kind: 'activity', items: [] }; out.push(cur); }
      cur.items.push(it);
    } else {
      cur = null;
      out.push(it);
    }
  }
  return out;
}

// turnEnds finds where each assistant turn ends in a transcript list (as
// group returns it) and what the turn said: a Map from the index of its
// last item to its text items' Markdown, joined by a blank line. A turn
// runs from one user message to the next; one that said nothing is left out.
export function turnEnds(items) {
  const ends = new Map();
  let texts = [];
  let last = -1;
  const close = () => {
    if (texts.length) ends.set(last, texts.join('\n\n'));
    texts = [];
    last = -1;
  };
  items.forEach((it, i) => {
    if (it.kind === 'user') { close(); return; }
    if (it.kind === 'text' && it.text?.trim()) texts.push(it.text.trim());
    last = i;
  });
  close();
  return ends;
}

// toolSummary renders a one-line label for a tool call from its arguments.
export function toolSummary(name, input) {
  let args = null;
  try { args = JSON.parse(input || '{}'); } catch { /* partial while streaming */ }
  const pick = (...keys) => { for (const k of keys) if (args && typeof args[k] === 'string' && args[k]) return args[k]; return ''; };
  const first = (s) => String(s).split('\n')[0];
  switch (name) {
    case 'exec': case 'exec_session': case 'shell': case 'bash': return { verb: 'Ran', detail: first(pick('command', 'cmd')) };
    case 'write_stdin': return { verb: 'Sent input', detail: first(pick('chars')) };
    case 'read_skill': return { verb: 'Read skill', detail: pick('name') };
    case 'read_file': case 'read_media': case 'read': return { verb: 'Read', detail: pick('path', 'file') };
    case 'write_file': case 'write': return { verb: 'Wrote', detail: pick('path', 'file') };
    case 'apply_patch': case 'edit_file': return { verb: 'Edited', detail: pick('path', 'file') };
    case 'list_dir': case 'ls': case 'glob': return { verb: 'Listed', detail: pick('path', 'pattern') };
    case 'grep': case 'search': return { verb: 'Searched', detail: pick('pattern', 'glob', 'query') };
    case 'web_search': return { verb: 'Searched web', detail: pick('query') };
    case 'web_fetch': case 'fetch': return { verb: 'Fetched', detail: pick('url') };
    default: {
      const detail = args ? pick(...Object.keys(args)) : '';
      return { verb: name, detail: first(detail) };
    }
  }
}
