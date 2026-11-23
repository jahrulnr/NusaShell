// Selections in the composer: "@" names threads and projects, "/" names
// agent commands, files, folders, skills and MCP servers. Positions in the
// editor are UTF-16 offsets; the wire uses byte offsets into the sent text,
// converted only at that boundary.

// referenceTrigger finds the "@word" or "/word" the caret sits in.
export function referenceTrigger(text, caret) {
  const before = text.slice(0, caret);
  const match = /(?:^|\s)([@/])([^\s@]*)$/.exec(before);
  if (!match) return null;
  const suffix = /^[^\s@]*/.exec(text.slice(caret))[0];
  return { start: caret - match[2].length - 1, end: caret + suffix.length, prefix: match[1], query: match[2] };
}

// reconcileReferences moves selections past an edit and drops the ones it
// touched. start/end is the selection the edit replaced, when known.
export function reconcileReferences(oldText, newText, refs, start, end, inputType = '') {
  if (oldText === newText || !refs.length) return refs;
  if (start == null || end == null) { start = 0; end = oldText.length; }
  if (start === end && newText.length < oldText.length && inputType.startsWith('delete')) {
    const removed = oldText.length - newText.length;
    if (inputType.toLowerCase().includes('backward')) start = Math.max(0, start - removed);
    else end = Math.min(oldText.length, end + removed);
  }
  let inserted = newText.length - (oldText.length - (end - start));
  if (inserted < 0 || oldText.slice(0, start) !== newText.slice(0, start) || oldText.slice(end) !== newText.slice(start + inserted)) {
    // The edit was not where the selection said: diff the texts instead.
    start = 0;
    while (start < oldText.length && start < newText.length && oldText[start] === newText[start]) start++;
    end = oldText.length;
    let newEnd = newText.length;
    while (end > start && newEnd > start && oldText[end - 1] === newText[newEnd - 1]) { end--; newEnd--; }
    inserted = newEnd - start;
  }
  const delta = inserted - (end - start);
  return refs.flatMap((ref) => {
    if (ref.end <= start) return [ref];
    if (ref.start >= end) return [{ ...ref, start: ref.start + delta, end: ref.end + delta }];
    return [];
  }).filter((ref) => newText.slice(ref.start, ref.end) === ref.label);
}

// liveReferences keeps the selections that still match the text — the
// draft may have been rewritten outside the editor.
export const liveReferences = (text, refs) => (refs || []).filter((r) => text.slice(r.start, r.end) === r.label);

// referencesForSend converts selections to byte offsets into the trimmed
// text the server receives.
export function referencesForSend(text, refs) {
  const trimmed = text.trim();
  const leading = text.length - text.trimStart().length;
  const enc = new TextEncoder();
  return liveReferences(text, refs)
    .filter((r) => r.start >= leading && r.end <= leading + trimmed.length)
    .map((r) => ({ kind: r.kind, id: r.id, label: r.label, start: enc.encode(text.slice(leading, r.start)).length, end: enc.encode(text.slice(leading, r.end)).length }))
    .sort((a, b) => a.start - b.start);
}

// referencesFromStored converts a message's byte offsets back to UTF-16.
export function referencesFromStored(text, refs) {
  const bytes = new TextEncoder().encode(text);
  const dec = new TextDecoder('utf-8', { fatal: true });
  return (Array.isArray(refs) ? refs : []).flatMap((r) => {
    try {
      const start = dec.decode(bytes.slice(0, r.start)).length;
      const end = dec.decode(bytes.slice(0, r.end)).length;
      return text.slice(start, end) === r.label ? [{ ...r, start, end }] : [];
    } catch { return []; }
  });
}

// referencesToDraft turns sent selections (byte offsets) back into editor
// selections after the text returns to the draft at offset `at`.
export function referencesToDraft(text, refs, at = 0) {
  return referencesFromStored(text, refs).map((r) => ({ ...r, start: r.start + at, end: r.end + at }));
}

// referenceLabel is the text a picked candidate puts in the message.
// File and folder selections expand to their workspace-relative path: a
// basename alone reads like an absolute path (/voice) to the model and
// hides where the file really is. Other kinds keep their trigger name.
export function referenceLabel(candidate, prefix) {
  const c = candidate || {};
  if ((c.kind === 'file' || c.kind === 'folder') && c.detail) {
    return c.detail.startsWith('/') ? c.detail : './' + c.detail;
  }
  return prefix + (c.name || '');
}

// KIND_LABEL names a selection kind in menus and ref tooltips.
export const KIND_LABEL = { file: 'file', folder: 'folder', skill: 'skill', mcp: 'MCP server', command: 'command', conversation: 'thread', project: 'project' };

// slug makes a project name one token a trigger can own.
export const slug = (s) => String(s || '').trim().toLowerCase().replace(/\s+/g, '-');
