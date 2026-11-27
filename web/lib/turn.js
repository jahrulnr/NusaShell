// Turn projection: websocket frames → ordered segments, and persisted
// history → the same segment shapes, so the transcript renders live and
// stored turns through one path.
//
// Segment kinds: text | reasoning | tool | steer | user | approval | plan | elicitation | resync.

// STEER_INPUT is the DTO field a steered input's user message carries: the
// inputId of the turn.steer command that wrote it (the contract the backend
// settled on). One constant, so a different final name is a one-line change.
export const STEER_INPUT = 'inputId';

// turnSeconds is how long a live turn has run on the server's clock: null until the
// server has said when it started (turn.opened), because a tab never invents a start.
export const turnSeconds = (t, now = Date.now()) => (t?.startedAt ? Math.floor((now - t.startedAt) / 1000) : null);

export function newTurn({ turnId, requestId, conversationId, kind, startedAt, steerable }) {
  const turn = {
    turnId, requestId, conversationId, kind,
    lastSeq: -1,
    segments: [],
    phase: null,          // { phase, retryAfterMs, at }
    usage: null,
    startedAt: startedAt || null,
    terminal: null,       // the terminal frame once it arrives
    n: 0,
  };
  // Only the wire's boolean. Absent stays absent: the client queues rather
  // than treating a missing field as permission to steer.
  if (steerable === true || steerable === false) turn.steerable = steerable;
  return turn;
}

const last = (t) => t.segments[t.segments.length - 1];

// toolParent is the run a subagent member belongs to. The only wire field is
// keys['acp.subagent.parent'] on the opening turn.tool_call, the same name the
// record uses. An empty value is absence. A frame field named parent is not a link.
function toolParent(p) {
  const linked = p?.keys?.['acp.subagent.parent'];
  return typeof linked === 'string' && linked !== '' ? linked : '';
}

function toolKeys(p) {
  return p?.keys && typeof p.keys === 'object' ? p.keys : null;
}

function settleTools(t, why, definitive = false) {
  for (const s of t.segments) {
    // A member is skipped for every settle, definitive or not: its run outlives
    // the step and the turn, and a missing result is not a failure. The skip
    // is the stored key from the opening frame, not a guess about the call.
    if (s.kind !== 'tool' || s.parent || s.status === 'success' || s.status === 'failed') continue;
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
    // The clock stops with the row: a call the turn closed without a result
    // still ran for a while, and that is what its label says from now on.
    if (s.at && !s.took) s.took = Date.now() - s.at;
  }
}

// unpersisted is a segment the store does not hold yet: streamed text or
// reasoning, and a tool row still waiting for its result. One predicate, so
// the tail a retry may discard is one rule rather than a list of exceptions.
const unpersisted = (s) =>
  s.kind === 'text' || s.kind === 'reasoning' || (s.kind === 'tool' && s.status !== 'success' && s.status !== 'failed');

// dropUnpersisted trims the tail a failed attempt left behind, up to the
// first segment the store already has. Everything before it is history the
// retry continues from, not output it rewrites.
function dropUnpersisted(t) {
  let end = t.segments.length;
  while (end > 0 && unpersisted(t.segments[end - 1])) end--;
  if (end < t.segments.length) t.segments.length = end;
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
      // adopts it, so the working timer survives the reattach. steerable is
      // the same declaration: true or false is written, and a missing field
      // is left missing.
      if (p.startedAt) t.startedAt = p.startedAt;
      if (p.steerable === true || p.steerable === false) t.steerable = p.steerable;
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
    case 'turn.phase': {
      t.phase = { phase: p.phase, retryAfterMs: p.retryAfterMs, at: Date.now() };
      // A retry re-runs a call that persisted nothing: everything the failed
      // attempt streamed is about to be streamed again by the next one. Drop
      // that tail now, or the two attempts splice into one answer.
      if (p.phase === 'retry_wait') dropUnpersisted(t);
      break;
    }
    case 'turn.tool_call': {
      // A later frame for the same call replaces its keys and does not add a
      // row. at stays the moment this tab first saw the call.
      const existing = t.segments.findLast((x) => x.kind === 'tool' && x.callId === p.callId);
      const keys = toolKeys(p);
      if (existing) {
        if (keys) {
          existing.keys = keys;
          existing.parent = toolParent({ keys });
        }
        if (typeof p.title === 'string' && p.title) existing.title = p.title;
        if (typeof p.kind === 'string' && p.kind) existing.toolKind = p.kind;
        if (typeof p.name === 'string' && p.name) existing.name = p.name;
      } else {
        t.segments.push({
          id: `${t.turnId}:${++t.n}`, kind: 'tool', callId: p.callId, name: p.name, input: '', output: '',
          status: 'requested', parent: toolParent(p), keys, title: typeof p.title === 'string' ? p.title : '',
          toolKind: typeof p.kind === 'string' ? p.kind : '', at: Date.now(),
        });
      }
      if (t.phase?.phase === 'reasoning') t.phase = null;
      break;
    }
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
        // not put a finished row back into the running state. A member has
        // no verdict: a later chunk is the run continuing, even if a sweep
        // had marked the row.
        if (s.parent) {
          if (s.status !== 'success') s.status = 'streaming';
        } else if (s.status !== 'success' && s.status !== 'failed') s.status = 'streaming';
      }
      break;
    }
    case 'turn.tool_result': {
      const s = t.segments.findLast((x) => x.kind === 'tool' && x.callId === p.callId);
      if (s) {
        s.status = p.failed ? 'failed' : 'success';
        s.settled = 'Tool finished';
        if (s.at && !s.took) s.took = Date.now() - s.at;
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
    case 'turn.steer_recalled': {
      // The owner withdrew this input: its live bubble goes. A row the store
      // already holds stays — the recall only unsends what was not used.
      if (p.inputId) t.segments = t.segments.filter((s) => !(s.kind === 'steer' && s.inputId === p.inputId));
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
          if (s.kind === 'tool' && !s.parent && s.status !== 'success' && s.status !== 'failed') {
            s.status = 'failed';
            s.settled = 'Turn failed';
            // Its clock stops here too: the row keeps the time it ran.
            if (s.at && !s.took) s.took = Date.now() - s.at;
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
        t.segments.push({
          id, kind: 'tool', callId: p.callId, name: p.name, input: p.input || '', output: p.output || '',
          status: p.status || 'requested', parent: toolParent(p), keys: toolKeys(p),
          title: typeof p.title === 'string' ? p.title : '', toolKind: typeof p.kind === 'string' ? p.kind : '',
        });
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

// relayName names the sender a relayed prompt's header shows, from the
// provenance the server stored — never from the message text. The label it
// was given (shown as "@label"), then its title, then a short form of the
// thread id when that is all the room has.
export function relayName(relay) {
  if (!relay) return '';
  if (relay.label) return '@' + relay.label;
  if (relay.title) return relay.title;
  const id = relay.id || '';
  return id.length > 12 ? id.slice(0, 12) + '…' : id;
}

// toolEnding answers how a tool call ended — one value read by the row, its
// card footer, and the group's count, so those three cannot disagree. Pure:
// it reads the item and nothing else.
//
// { kind, mark, word, tone } plus the detail a footer needs: code for an exit,
// signal for a kill, cap for a timeout, by for who stopped it. `mark` names the
// dot (or ring) the row draws; `word` is the status as the row says it, empty
// when there is nothing to add; `tone` is its colour family.
export function toolEnding(item) {
  if (!item) return { kind: 'ok', mark: 'ok', word: '', tone: '' };
  if (item.status === 'requested' || item.status === 'streaming') return { kind: 'running', mark: 'live', word: '', tone: '' };
  const close = closeStatus(item.output)?.close;
  if (close) {
    switch (close.kind) {
      case 'exit':
        if (close.code === 0) {
          // A clean exit the backend still calls failed: the backend is
          // authoritative about its own call.
          return item.status === 'failed'
            ? { kind: 'failed', mark: 'bad', word: 'failed', tone: 'bad' }
            : { kind: 'ok', mark: 'ok', word: '', tone: '' };
        }
        return { kind: 'exit', mark: 'bad', word: `exit ${close.code}`, tone: 'bad', code: close.code };
      case 'signal':
        return { kind: 'signal', mark: 'bad', word: close.signal, tone: 'bad', signal: close.signal };
      case 'timeout':
        return { kind: 'timeout', mark: 'warn', word: 'timed out', tone: 'warn', cap: close.after };
      case 'interrupted': {
        const word = close.by === 'agent' ? 'stopped by agent' : close.by === 'server' ? 'restarted' : 'stopped';
        return { kind: 'stopped', mark: 'ring', word, tone: 'muted', by: close.by };
      }
      case 'canceled':
        return { kind: 'canceled', mark: 'ring', word: 'canceled', tone: 'muted' };
    }
  }
  // No status line: the backend's flag, or the wording of a stored result.
  if (item.status === 'failed' || (item.status === 'settled' && toolFailed(item.output))) {
    return { kind: 'failed', mark: 'bad', word: 'failed', tone: 'bad' };
  }
  if (item.status === 'pending') return { kind: 'pending', mark: 'none', word: 'no result', tone: 'muted' };
  return { kind: 'ok', mark: 'ok', word: '', tone: '' };
}

// userLayout answers how a user message is laid out. Whether it came from
// another thread is the server's word — userType 'relay' on the stored message
// — not something read back out of the text: a prompt the owner happens to
// start with the relay head's wording is still theirs. A relayed prompt carries what
// it brought inside its own block, and is not the owner's to rewrite.
export function userLayout(relayed) {
  return { relayed: !!relayed, extras: relayed ? 'relay' : 'own', editable: !relayed };
}

// isPrompt answers whether a stored line is a prompt the room owes an answer
// to. Synthetic rows are never prompts: a note (system) is a report, a
// compaction checkpoint is a boundary — the domain's SystemPayload(). A
// relayed prompt stays a prompt: its answer is still this room's to give.
export function isPrompt(m) {
  return m.role === 'user' && m.userType !== 'system' && m.userType !== 'compaction';
}

// A tool's stored output signals failure by convention.
// closeStatus reads the status line exec closes its output with: how the
// process ended, and — for an exit — how long it ran. The line is data the
// model needs, so it stays in the stored output; the row reads it to say what
// happened, and the transcript shows the output without it.
//
// The vocabulary is the runner's (infrastructure/service/tool/terminal):
//   (exit 0, 4.4s)  (exit 1, 43.2s)  (no output, exit 0, 0.3s)
//   (signal: killed, 3.2s)
//   (timed out after 120s — partial output above; …)
//   (interrupted by user after 12.3s — partial output above)
//   (interrupted by agent after 4.1s — …)  (nusashell restarted after 8.0s — …)
//   (canceled after 12.3s — …)
// The duration is optional and never decides anything: it says how long, not
// what happened.
export function closeStatus(output) {
  const text = String(output || '').trimEnd();
  const cut = text.lastIndexOf('\n');
  const last = (cut >= 0 ? text.slice(cut + 1) : text).trim();
  const m = /^\((.*)\)$/.exec(last);
  if (!m) return null;
  const close = classifyClose(m[1].trim());
  if (!close) return null;
  return { body: cut >= 0 ? text.slice(0, cut) : '', close };
}

function classifyClose(inner) {
  let m;
  // How long the run took is the runner's own number, on the same line: it is
  // the only duration a replayed or resynced row can still know, since the tab
  // never saw the call start.
  if ((m = /^(?:no output, )?exit (\d+)(?:, ([\d.]+)s)?$/.exec(inner))) {
    return { kind: 'exit', code: Number(m[1]), ms: m[2] != null ? Math.round(Number(m[2]) * 1000) : undefined };
  }
  // A runner that could not start the process reports the wait error itself.
  if ((m = /^exit status (\d+)(?:, ([\d.]+)s)?$/.exec(inner))) {
    return { kind: 'exit', code: Number(m[1]), ms: m[2] != null ? Math.round(Number(m[2]) * 1000) : undefined };
  }
  if ((m = /^signal: ([^,]+?)(?:, ([\d.]+)s)?$/.exec(inner))) {
    return { kind: 'signal', signal: m[1].trim(), ms: m[2] != null ? Math.round(Number(m[2]) * 1000) : undefined };
  }
  // The cap that fired is how long the run lasted, to within the poll.
  if ((m = /^timed out after (\S+)/.exec(inner))) return { kind: 'timeout', after: m[1], ms: parseWait(m[1]) };
  if ((m = /^interrupted by (user|agent) after ([\d.]+)s/.exec(inner))) return { kind: 'interrupted', by: m[1], ms: Math.round(Number(m[2]) * 1000) };
  if ((m = /^nusashell restarted after ([\d.]+)s/.exec(inner))) return { kind: 'interrupted', by: 'server', ms: Math.round(Number(m[1]) * 1000) };
  if ((m = /^canceled after ([\d.]+)s/.exec(inner))) return { kind: 'canceled', ms: Math.round(Number(m[1]) * 1000) };
  return null;
}

// parseWait reads the duration the runner writes for a timeout — "120s", or a
// Go duration like "2m0s" when the wait is not whole seconds.
function parseWait(text) {
  let ms = 0;
  const re = /([\d.]+)(ms|h|m|s)/g;
  let m;
  while ((m = re.exec(String(text || '')))) {
    const n = Number(m[1]);
    if (!Number.isFinite(n)) continue;
    ms += m[2] === 'ms' ? n : m[2] === 's' ? n * 1000 : m[2] === 'm' ? n * 60_000 : n * 3_600_000;
  }
  return ms || undefined;
}

// toolFailed answers whether a tool call failed, not whether it stopped: a
// nonzero exit and a signal that killed it are failures, while a timeout, an
// interruption, and a cancel are this side ending the run on purpose.
export function toolFailed(output) {
  const o = String(output || '');
  const close = closeStatus(o)?.close;
  if (close) {
    if (close.kind === 'exit') return close.code !== 0;
    return close.kind === 'signal';
  }
  return /^error:/i.test(o) || /^failed$/i.test(o.trim());
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

// liveSegments answers the live turn's segments as the transcript should draw
// them, given what history already holds.
//
// Two things the record has are replayed to a tab that did not watch the turn
// from its first frame (a reload, another tab, a resync). A steer is written to
// history as an ordinary user message carrying the inputId of the turn.steer
// command, so its line is in history AND its bubble comes back from the
// journal. The record wins — it is the full copy, with the annotations and
// uploads the frame never carried — but it is drawn where the live segment
// sits, not where history parked it: text that streamed before the steer must
// stay above it. The caller hides the row in its history place instead.
//
// And a turn that has already finished a step has that step's text and tool
// calls in history, while the journal replays them from the beginning: drawn
// twice, the same work appears as two separate "Used N tools" runs, split by
// the duplicated prose, and only merges once the turn ends and the live copy
// is dropped. So the live tail starts after the last tool call the record
// holds — the leading run of replayed segments is the persisted prefix, and
// only it goes. Segments after it are output the record does not have, and a
// resync marker inside the dropped range is kept: it says what is missing
// rather than duplicating it.
export function liveSegments(live, items) {
  const segments = live?.segments || [];
  if (!segments.length) return segments;
  const calls = new Set();
  const rows = new Map();
  const byCall = new Map();
  for (const x of items || []) {
    if (x.kind === 'tool' && x.callId) { calls.add(x.callId); byCall.set(x.callId, x); }
    if (x.kind === 'user' && x.steerInput) rows.set(x.steerInput, x);
  }
  // The history row is the one on screen once this prefix is dropped, so a
  // later key frame has to land on it or the card would keep the old keys.
  for (const s of segments) {
    const h = s.kind === 'tool' && s.callId ? byCall.get(s.callId) : null;
    if (!h || !s.keys) continue;
    h.keys = s.keys;
    h.parent = s.parent || '';
    if (s.title) h.title = s.title;
    if (s.toolKind) h.toolKind = s.toolKind;
  }
  if (!calls.size && !rows.size) return segments;
  let cut = -1;
  for (let i = 0; i < segments.length; i++) {
    const s = segments[i];
    if (s.kind !== 'tool') continue;
    // A tool call the record does not hold ends the persisted prefix: what
    // follows is this tab's own tail, and what came before it cannot be
    // assumed to be stored.
    if (!s.callId || !calls.has(s.callId)) break;
    cut = i;
  }
  const dropped = cut >= 0 ? segments.slice(0, cut + 1) : [];
  const rest = cut >= 0 ? segments.slice(cut + 1) : segments;
  const kept = [];
  for (const s of rest) {
    if (s.kind !== 'steer' || !s.inputId) { kept.push(s); continue; }
    // The record's copy when it has one, the live bubble otherwise: an input
    // the room has not written yet is only ever the live one.
    kept.push(rows.get(s.inputId) || s);
  }
  return [...dropped.filter((s) => s.kind === 'resync'), ...kept];
}

// joinText folds neighbouring text items into one block. A part is a
// persistence boundary — the server seals the text of a finished step so the
// next step cannot attach to it — not a presentation one: drawn as separate
// Markdown blocks, one sentence split at a step boundary becomes two
// paragraphs, and a fenced block split there is parsed twice and comes out
// broken. Joining keeps the first item's id, so the DOM key stays stable
// while the text grows, and it runs before group(), so "Copy response" copies
// the text as it was written. A tool, steer, plan, media, approval, or
// elicitation between two texts is a real boundary and keeps them apart.
export function joinText(items) {
  const out = [];
  for (const it of items || []) {
    const prev = out[out.length - 1];
    if (it.kind === 'text' && prev?.kind === 'text') {
      out[out.length - 1] = { ...prev, text: prev.text + it.text };
      continue;
    }
    out.push(it);
  }
  return out;
}

// historyItems flattens persisted messages into transcript items:
// { kind: 'user', msg } | { kind: 'note', msg } | segment-like items.
export function historyItems(messages, cwd = '') {
  const out = [];
  for (const m of messages) {
    if (m.role === 'user') {
      out.push({
        // A compaction checkpoint is a boundary in the history, not the
        // owner's word — its own row, so it never renders as a message.
        id: m.id, kind: isPrompt(m) ? 'user' : m.userType === 'compaction' ? 'compaction' : 'note', msg: m,
        steerInput: m[STEER_INPUT] || '',
        // A prompt another thread sent is the server's to say (userType), never
        // something the transcript guesses from the text. Its provenance rides
        // as data — the header reads it, the body stays untouched.
        relayed: m.userType === 'relay',
        relay: m.relay,
      });
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
          parent: toolParent(c), keys: toolKeys(c),
          title: typeof c.title === 'string' ? c.title : '',
          toolKind: typeof c.kind === 'string' ? c.kind : '',
          turnEnd: m.finishReason === 'interrupted' ? 'stopped' : m.finishReason ? 'finished' : '',
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

// A stored ghost is a failed tool row whose every content field is blank or
// a sentinel: name blank or "tool", input blank, null, or "{}", output blank
// or the word "failed", and no kind. The DTO type is not read — every call
// is marked delegated. A real title, a real input, a real output, or a row
// that has not failed stays a card.
const blank = (v) => !String(v ?? '').trim();
function ghostOutput(v) {
  const s = String(v ?? '').trim();
  return s === '' || s === 'failed';
}
function sentinelName(v) {
  const s = String(v ?? '').trim();
  return s === '' || s === 'tool';
}
function sentinelInput(v) {
  if (v == null) return true;
  const s = String(v).trim();
  return s === '' || s === '{}';
}
export function isGhostTool(it) {
  return !!it && it.kind === 'tool' && it.status === 'failed'
    && ghostOutput(it.output) && sentinelName(it.name) && sentinelInput(it.input) && blank(it.toolKind);
}

// ghostCountText is the one quiet line a turn uses for its ghosts. The
// singular is its own sentence; the count is the fact the line keeps.
export function ghostCountText(n) {
  return n === 1
    ? '1 agent event with no recorded detail'
    : `${n} agent events with no recorded detail`;
}

const TURN_EDGE = new Set(['user', 'note', 'compaction']);

// foldGhosts replaces the empty failed tool rows of one turn with a single
// quiet line, placed after the last tool row that remains. A turn with none
// is returned as it was. Runs before group(), so the line is not a card.
export function foldGhosts(items) {
  if (!items?.some(isGhostTool)) return items || [];
  const out = [];
  let turn = [];
  const flush = () => {
    if (!turn.length) return;
    const ghosts = turn.filter(isGhostTool);
    if (!ghosts.length) { out.push(...turn); turn = []; return; }
    const kept = [];
    let afterTool = -1;
    for (const it of turn) {
      if (isGhostTool(it)) continue;
      if (it.kind === 'tool') afterTool = kept.length;
      kept.push(it);
    }
    const line = { id: 'ghosts:' + ghosts[0].id, kind: 'ghosts', n: ghosts.length, text: ghostCountText(ghosts.length) };
    if (afterTool < 0) kept.push(line);
    else kept.splice(afterTool + 1, 0, line);
    out.push(...kept);
    turn = [];
  };
  for (const it of items) {
    if (TURN_EDGE.has(it.kind)) { flush(); out.push(it); continue; }
    turn.push(it);
  }
  flush();
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

const SUB_ID = 'acp.subagent.id';
const SUB_PROFILE = 'acp.subagent.profile';
const SUB_MODEL = 'acp.subagent.model';
const SUB_BACKGROUND = 'acp.subagent.background';
const SUB_SUCCESS = 'acp.subagent.success';
const SUB_SUMMARY = 'acp.subagent.summary';

export function subagentId(it) {
  const id = it?.keys?.[SUB_ID];
  return typeof id === 'string' && id !== '' ? id : '';
}

function inputObject(raw) {
  if (typeof raw !== 'string' || !raw.trim()) return null;
  try {
    const v = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null;
  } catch { return null; }
}

function shownText(v) {
  return typeof v === 'string' && v.trim() ? v : '';
}

function flagTrue(v) {
  return v === true || v === 'true';
}

function flagFalse(v) {
  return v === false || v === 'false';
}

// subagentStatus is the card word. It reads the success key and how the turn
// ended. It never reads the tool row, the output, or a clock.
export function subagentStatus(keys, end = {}) {
  const success = keys?.[SUB_SUCCESS];
  if (flagTrue(success)) return { status: 'Done', note: '' };
  if (flagFalse(success) || end.ended === 'finished') {
    return { status: 'Incomplete', note: 'No report was recorded for this subagent.' };
  }
  if (end.ended === 'stopped') {
    return {
      status: 'Incomplete',
      note: end.ownerStop
        ? 'You stopped the turn before this subagent reported.'
        : 'The turn ended before this subagent reported.',
    };
  }
  if (end.blind) return { status: 'Running — no live view', note: '' };
  return { status: 'Running', note: '' };
}

// memberLine is one activity line: kind, title, and the first line of input.
// A missing part is left out. Nothing is invented after it.
export function memberLine(it) {
  const raw = typeof it?.input === 'string' ? it.input : '';
  const line = raw.split('\n')[0].trim();
  const kind = typeof it?.toolKind === 'string' ? it.toolKind.trim() : '';
  const title = typeof it?.title === 'string' ? it.title.trim() : '';
  return [kind, title, line].filter(Boolean);
}

// subagentView is the card the screen draws. The parent output stays the
// output. The summary is a short line only after Done, and it is not the output.
export function subagentView(item) {
  const keys = item?.keys || {};
  const input = inputObject(item?.input);
  const profile = shownText(input?.profile) || shownText(keys[SUB_PROFILE]);
  const model = shownText(keys[SUB_MODEL]);
  const badges = [];
  if (profile) badges.push(profile);
  if (model) badges.push(model);
  if (flagTrue(keys[SUB_BACKGROUND])) badges.push('Background');
  const state = subagentStatus(keys, item?.subagentEnd || {});
  return {
    title: shownText(input?.title),
    task: shownText(input?.task),
    badges,
    status: state.status,
    note: state.note,
    summary: state.status === 'Done' ? shownText(keys[SUB_SUMMARY]) : '',
    members: (item?.members || []).map(memberLine),
    output: typeof item?.output === 'string' ? item.output : '',
  };
}

// applyLiveEnd stamps history rows of a turn this tab just saw end, so a
// card drawn from the stored row does not stay Running after the terminal.
export function applyLiveEnd(items, live) {
  const term = live?.terminal;
  if (!term || !items?.length) return items;
  const ids = new Set();
  for (const s of live.segments || []) if (s.kind === 'tool' && s.callId) ids.add(s.callId);
  if (!ids.size) return items;
  const ended = term.type === 'turn.completed' ? 'finished' : 'stopped';
  const ownerStop = ended === 'stopped' && !!live.canceling;
  for (const it of items) {
    if (it.kind === 'tool' && ids.has(it.callId)) {
      it.turnEnd = ended;
      it.ownerStop = ownerStop;
    }
  }
  return items;
}

// placeSubagents lifts each run that carries acp.subagent.id into its own
// card, inline where that call sits. Members join by parent id. A member
// whose card is absent stays an ordinary row until the turn ends, and then
// a flat delegated row. A call with no keys is left untouched.
export function placeSubagents(items, end = {}) {
  if (!items?.some((it) => it.kind === 'tool' && (subagentId(it) || it.parent))) return items || [];
  const out = [];
  let turn = [];
  const flush = () => {
    if (!turn.length) return;
    out.push(...placeTurn(turn, endOfTurn(turn, end)));
    turn = [];
  };
  for (const it of items) {
    if (TURN_EDGE.has(it.kind)) { flush(); out.push(it); continue; }
    turn.push(it);
  }
  flush();
  return out;
}

function endOfTurn(turn, fallback) {
  const marked = turn.find((it) => it.kind === 'tool' && it.turnEnd);
  if (!marked) return fallback || {};
  return { ended: marked.turnEnd, ownerStop: !!marked.ownerStop, blind: false };
}

function placeTurn(turn, end) {
  const cards = new Map();
  for (const it of turn) {
    const id = it.kind === 'tool' ? subagentId(it) : '';
    if (id && !cards.has(id)) cards.set(id, { ...it, kind: 'subagent', members: [], subagentEnd: end });
  }
  const out = [];
  const seen = new Set();
  for (const it of turn) {
    const id = it.kind === 'tool' ? subagentId(it) : '';
    if (id) {
      if (seen.has(id)) continue;
      seen.add(id);
      out.push(cards.get(id));
      continue;
    }
    if (it.kind === 'tool' && it.parent) {
      const card = cards.get(it.parent);
      if (card) { card.members.push(it); continue; }
      if (end.ended) {
        out.push({ id: 'delegated:' + it.id, kind: 'delegated', name: it.name || '', title: it.title || '' });
        continue;
      }
    }
    out.push(it);
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

// formatDuration is the coarse, stable label a tool row shows: whole seconds
// while it runs, so the text changes once a second and never jitters.
export function formatDuration(ms) {
  const s = Math.max(0, Math.round((ms || 0) / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`;
}

// toolClock answers what a tool row says about time, or '' when it knows
// nothing: a call this tab saw start counts up from `at` while it runs (the
// row's own ticker re-reads this every second), and a call that has a stored
// duration shows it once, fixed. A row from history has neither.
export function toolClock(item, now = Date.now()) {
  if (!item) return '';
  const running = item.status === 'requested' || item.status === 'streaming';
  // While it runs the tab's own clock from the call's start is all there is.
  if (running) return item.at ? formatDuration(now - item.at) : '';
  // Finished: the runner's duration on the status line wins. It is the same
  // number whether this tab watched the call or replayed it, which the tab's
  // clock is not — a replayed 40-second call starts when the replay arrives.
  const took = closeStatus(item.output)?.close?.ms;
  if (took != null) return formatDuration(took);
  return item.took ? formatDuration(item.took) : '';
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

// ROOM_VERBS names the tools whose subject is another room, and the verb the
// row reads. A room tool names the room itself, so the generic summary (which
// would print the raw id from the arguments) does not apply to it.
const ROOM_VERBS = {
  conversation_send: 'Sent',
  conversation_stop: 'Stopped',
  conversation_read: 'Read',
  conversation_wait: 'Waited for',
  conversation_search: 'Searched',
  conversation_spawn: 'Spawned',
  conversation_export: 'Exported',
};

// shortHandle is a room id said short: its last six characters, marked as a
// handle, which is all a row can honestly show before the result names it. Where
// another id the client has met ends alike, it is the shortest suffix none of
// them shares, so a handle grows only when a new id collides with it. `among`
// holds those ids; the id itself is never among its rivals.
export function shortHandle(id, among = new Set()) {
  const s = String(id || '').trim();
  if (!s) return '';
  const taken = (n) => { const tail = s.slice(-n); for (const x of among) if (x !== s && x.endsWith(tail)) return true; return false; };
  let n = Math.min(6, s.length);
  while (n < s.length && taken(n)) n++;
  return '@' + s.slice(-n);
}

// roomName is how one room reads: its label, its title, or the short handle of
// its id when it has neither. A label is already a handle, so it takes no
// quotes; a title is not one, so it is quoted and never given an at-sign — an
// at-sign on a title would invent a handle nobody has.
//
// It reads both shapes a tool answers with: a target that carries the thread
// object (send, stop, read, wait, export, and spawn once it has one), and a
// flat search hit (threadId, label, title). One reader for one shape on screen.
// roomName reads one room's name. A room the client has loaded reads from its own
// record first, so a row names it the same way while its call runs and after the
// answer lands; the answer's thread is the fallback for a room the client has not
// loaded, and the handle the last one. A room the client has not loaded reads as
// its handle until its answer names it. If such rows ever come to more than one
// in ten of a transcript's room rows, read the room from the server, cached,
// rather than widen this fallback (BL-10).
function roomName(s, knownRoom) {
  const k = knownRoom(s.id);
  const from = k.label || k.title ? k : s;
  const label = String(from.label || '').trim();
  const title = String(from.title || '').trim();
  return { label, title, short: label || title ? '' : k.handle };
}

// partialId reads the id out of arguments that are still arriving: a tool's
// input streams as text, so its first frames are not JSON yet and the row
// would have nothing to show for them.
function partialId(input) {
  const m = /"conversationId"\s*:\s*"([^"\\]*)/.exec(String(input || ''));
  return m ? m[1].trim() : '';
}

// toolRoom answers the room (or rooms) a tool row names, or null for a tool
// that names none. The result is the source: a thread object the server read
// when the call was made — an id in the transcript is not a name, and a cache
// here would go stale the first time a label changed.
//
// While the call still runs there is no result yet, so the row falls back to
// what the input named (a short handle, or the label the caller already used),
// because an empty summary would read as a tool that was never called.
export function toolRoom(name, input, output, knownRoom = noRooms) {
  const verb = ROOM_VERBS[name];
  if (!verb) return null;
  let args = null;
  try { args = JSON.parse(input || '{}'); } catch { /* partial while streaming */ }
  let out = null;
  try { out = JSON.parse(output || ''); } catch { /* no result yet */ }
  // A search names the query it ran. An export answers a file and a count rather
  // than a room, so the row shows what it wrote.
  const detail = name === 'conversation_search' ? (typeof args?.query === 'string' ? args.query : '')
    : name === 'conversation_export' ? String(out?.path || '') : '';
  return { verb, detail, names: roomSubjects(name, input, output).map((s) => roomName(s, knownRoom)).filter(named) };
}

// noRooms is the room book of a caller that knows no rooms: no names, and a
// handle over the id alone.
const noRooms = (id) => ({ label: '', title: '', handle: shortHandle(id) });

// subject reads one room from an answer's shape: a thread object, or a flat search
// hit. whole says the id comes from something complete, an answer or an input that
// parsed. A call still streaming its input names nothing the client may register.
const subject = (src, whole) => {
  const t = src?.thread || src || {};
  return { id: String(t.id || t.threadId || '').trim(), label: String(t.label || '').trim(), title: String(t.title || '').trim(), whole };
};

// roomSubjects reads the rooms a room tool call names: every hit of a search, else
// the thread its answer carries, else the id it was called with. The row reads
// these, and so does the client's registry of the ids it has met, so both read a
// call the same way.
//
// The input is read as an opaque handle, never as a name. A model can write a
// label where an id belongs, and a call that fails never gets a result to
// correct it — so the transcript must not repeat the model's word as if the
// room were really called that. The handle is the last six characters of what
// it wrote, until the result says what the room is.
export function roomSubjects(name, input, output) {
  if (!ROOM_VERBS[name]) return [];
  let args = null;
  try { args = JSON.parse(input || '{}'); } catch { /* partial while streaming */ }
  let out = null;
  try { out = JSON.parse(output || ''); } catch { /* no result yet */ }
  // One hit per room, each already flat, so a list of rooms reads as a list of names.
  if (name === 'conversation_search') return (Array.isArray(out?.hits) ? out.hits : []).map((h) => subject(h, true));
  // A spawn answers an id until its thread object lands.
  if (out) return [subject(out, true)];
  const asked = typeof args?.conversationId === 'string' ? args.conversationId.trim() : partialId(input);
  return [{ id: asked.replace(/^@/, ''), label: '', title: '', whole: !!args }];
}

// named drops a room the answer said nothing about, so an empty handle never
// renders as a blank gap.
const named = (n) => !!(n.label || n.title || n.short);
