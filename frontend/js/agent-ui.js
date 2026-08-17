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

function estimateMessageChars(message) {
  if (!message || typeof message !== 'object') return 0;
  let chars = 0;
  const nonAsciiWeight = (text) => {
    let extra = 0;
    for (const ch of String(text ?? '')) {
      if (ch.charCodeAt(0) > 0x7f) extra += 4; // ~1 token surcharge per rune
    }
    return extra;
  };
  if (Array.isArray(message.steps) && message.steps.length > 0) {
    chars += estimateTokenChars(message.steps);
    if (message.attachments) chars += estimateTokenChars(message.attachments);
    return chars + jsonNonAsciiSurcharge(message.steps, nonAsciiWeight);
  }
  if (typeof message.content === 'string') chars += message.content.length + nonAsciiWeight(message.content);
  else if (message.content != null) {
    chars += estimateTokenChars(message.content);
    chars += jsonNonAsciiSurcharge([message.content], nonAsciiWeight);
  }
  if (typeof message.reasoning === 'string') chars += message.reasoning.length + nonAsciiWeight(message.reasoning);
  if (message.tool_calls) chars += estimateTokenChars(message.tool_calls);
  if (message.attachments) chars += estimateTokenChars(message.attachments);
  return chars;
}

function jsonNonAsciiSurcharge(parts, weight) {
  let extra = 0;
  for (const part of parts ?? []) {
    if (typeof part === 'string') extra += weight(part);
    else if (part && typeof part === 'object') {
      for (const key of Object.keys(part)) {
        const value = part[key];
        if (typeof value === 'string') extra += weight(value);
      }
    }
  }
  return extra;
}

function estimateTokenChars(value) {
  if (value == null) return 0;
  if (typeof value === 'string') return value.length;
  try {
    return JSON.stringify(value).length;
  } catch {
    return String(value).length;
  }
}

function startsWith(data, signature) {
  return data.length >= signature.length && signature.every((byte, index) => data[index] === byte);
}
