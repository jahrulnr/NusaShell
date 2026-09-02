// Small pure helpers shared by the Agent composer and its unit tests.

const PNG_SIGNATURE = [137, 80, 78, 71, 13, 10, 26, 10];

export function formatContextUsage(usedTokens, contextWindow) {
  const used = Number.isFinite(usedTokens) && usedTokens > 0 ? usedTokens : 0;
  if (Number.isFinite(contextWindow) && contextWindow > 0) {
    return `${formatTokenCount(used)}/${formatTokenCount(contextWindow)} context`;
  }
  return `${formatTokenCount(used)} ctx`;
}

/**
 * Resolve the effective context window denominator shown to the user.
 * The global max_input_tokens cap is only a FALLBACK for models that are
 * not in the catalog (unknown window). When the model advertises its window,
 * that window wins — capping it to the global setting only confuses users
 * ("1M model, why 200k?"). When the model window is unknown the global
 * setting is used; when neither is known, returns 0.
 */
export function effectiveContextWindow(modelWindow, globalMaxInputTokens) {
  const windowValue = Number.isFinite(modelWindow) && modelWindow > 0 ? modelWindow : 0;
  const fallback = Number.isFinite(globalMaxInputTokens) && globalMaxInputTokens > 0 ? globalMaxInputTokens : 0;
  if (windowValue > 0) return windowValue;
  return fallback;
}

export function estimateContextTokens(messages = []) {
  let chars = 0;
  for (const message of messages) chars += estimateMessageChars(message);
  // ~4 chars/token, +4 tokens per message, +5% safety buffer.
  const tokens = chars / 4 + 4 * messages.length;
  return Math.ceil(tokens * 1.05);
}

// initialWindowStart returns the index of the first active message to render
// so only the most recent `windowSize` messages are shown on open. Older
// messages stay in state and are prepended on scroll-up. This keeps opening a
// long (uncompacted) conversation from rendering thousands of DOM nodes at once.
export function initialWindowStart(total, windowSize) {
  const n = Math.max(0, Math.floor(Number(total) || 0));
  const w = Math.max(1, Math.floor(Number(windowSize) || 1));
  return Math.max(0, n - w);
}

// previousWindowStart returns the new window start after revealing one more
// batch of older messages (clamped at 0).
export function previousWindowStart(currentStart, batch) {
  const s = Math.max(0, Math.floor(Number(currentStart) || 0));
  const b = Math.max(1, Math.floor(Number(batch) || 1));
  return Math.max(0, s - b);
}

// Keep the scroll-pin geometry in a pure helper so terminal lifecycle paths
// can sample the real viewport even when the browser has not delivered its
// latest scroll event yet.
export function isThreadAtBottom(thread, tolerance = 24) {
  if (!thread) return true;
  return thread.scrollHeight - thread.scrollTop - thread.clientHeight <= tolerance;
}

export function syncThreadPin(state, thread) {
  if (thread) state.pinned = isThreadAtBottom(thread);
  return state.pinned;
}

// updateScrollPin is the race-free pin decision for a growing thread.
//
// It is direction-aware: only a real upward user scroll releases the pin.
// Programmatic follow-scrolls always move down (or stay), and content growth
// between scroll events only increases the bottom distance — so neither can
// ever be mistaken for "the user scrolled up". This is what keeps autoscroll
// alive while tool cards spam the tail: the old geometry-only check read the
// post-growth distance and mistook content growth for the user reading up.
//
// - pinned + still at (or near) the bottom → stay pinned.
// - pinned + scrollTop moved up beyond the tolerance a follow-scroll can
//   leave → released (user intent: read history).
// - released + user returned near the bottom → re-pinned.
export function updateScrollPin(state, thread, tolerance = 24, options = {}) {
  if (!thread) return state.pinned;
  const scrollTop = thread.scrollTop;
  const distance = thread.scrollHeight - scrollTop - thread.clientHeight;
  const direction = typeof options === 'string' ? options : options.direction;
  const previous = state.pinGeom;
  if (direction === 'up') {
    // A gesture is stronger evidence than a single geometry sample. Touchpad
    // and wheel events can move only a few pixels before the first scroll
    // event, so waiting for the tolerance here makes follow feel sticky.
    state.pinned = false;
  } else if (direction === 'down' && distance <= tolerance) {
    state.pinned = true;
  } else if (previous && previous.thread === thread && scrollTop < previous.scrollTop - tolerance) {
    // Upward movement beyond what a follow-scroll could produce: the user is
    // scrolling up. Release the pin regardless of where the bottom now is.
    state.pinned = false;
  } else if (distance <= tolerance) {
    state.pinned = true;
  }
  state.pinGeom = { thread, scrollTop };
  return state.pinned;
}

// conversationTail is the snapshot window for a long thread: keep the last
// user (or compaction) bubble, keep the complete trailing assistant run, and
// leave only older complete turns to Load older. A trailing run can contain
// dozens of tool rounds after one user message; trimming it makes most of the
// actual answer disappear behind an older-history affordance. This function
// must not flatten history into an "N earlier rounds" stub.
export function conversationTail(messages = [], options = {}) {
  const prefixWindow = Math.max(1, Math.floor(Number(options.prefixWindow) || 60));
  const n = Array.isArray(messages) ? messages.length : 0;
  if (n === 0) {
    return { visible: [], prefixStart: 0, runStart: 0, assistKeepStart: 0 };
  }
  let lastNonAssistant = n - 1;
  while (lastNonAssistant >= 0 && messages[lastNonAssistant]?.role === 'assistant') {
    lastNonAssistant--;
  }
  const runStart = lastNonAssistant + 1;
  // The current assistant run is one logical response, even when the
  // provider persisted many intermediate tool/reasoning messages. Keep it
  // mounted for both idle snapshots and live turns. `assistKeepStart` stays
  // in the return shape for callers that inspect the split; it now always
  // points at the beginning of the complete trailing run.
  const assistKeepStart = runStart;
  let prefixStart;
  if (options.prefixStart != null && Number.isFinite(Number(options.prefixStart))) {
    prefixStart = Math.min(runStart, Math.max(0, Math.floor(Number(options.prefixStart))));
  } else {
    const keptAssistants = n - assistKeepStart;
    const prefixBudget = Math.max(0, prefixWindow - keptAssistants);
    prefixStart = Math.max(0, runStart - prefixBudget);
  }
  if (runStart > 0) prefixStart = Math.min(prefixStart, runStart - 1);
  const visible = messages.slice(prefixStart, runStart).concat(messages.slice(assistKeepStart));
  return { visible, prefixStart, runStart, assistKeepStart };
}

export function inspectAttachmentContent(bytes) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  if (startsWith(data, PNG_SIGNATURE)) return { type: 'image', mediaType: 'image/png' };
  if (startsWith(data, [255, 216, 255])) return { type: 'image', mediaType: 'image/jpeg' };
  if (startsWith(data, [71, 73, 70, 56])) return { type: 'image', mediaType: 'image/gif' };
  if (startsWith(data, [37, 80, 68, 70, 45])) return { type: 'file', mediaType: 'application/pdf' };
  if (startsWith(data, [82, 73, 70, 70]) && startsWith(data.slice(8), [87, 69, 66, 80])) return { type: 'image', mediaType: 'image/webp' };
  try {
    const content = new TextDecoder('utf-8', { fatal: true }).decode(data);
    if (/[\u0000-\u0008\u000E-\u001F]/.test(content)) return null;
    return { type: 'text', mediaType: 'text/plain', content };
  } catch {
    return null;
  }
}

export function toDataURL(bytes, mediaType) {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return `data:${mediaType};base64,${btoa(binary)}`;
}

function formatTokenCount(value) {
  if (!Number.isFinite(value) || value <= 0) return '0';
  if (value >= 1_000_000) return `${Number((value / 1_000_000).toFixed(1))}M`;
  if (value >= 1_000) return `${Math.round(value / 1_000)}k`;
  return String(value);
}

function startsWith(data, signature) {
  return data.length >= signature.length && signature.every((byte, index) => data[index] === byte);
}
