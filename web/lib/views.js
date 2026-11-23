// A thread's view, kept on the server: the unsent draft (its text, the
// references in it, the quotes riding above it, the files ready to go) and
// the side pane, so a reload — or the same thread on another device —
// opens where the owner left it. The fresh room's view is the composer of
// a thread not started yet.
import { api } from './api.js';
import { state, subscribe } from './store.js';
import { liveReferences } from './references.js';

const SAVE_DELAY = 700;
// The side pane covers the whole screen below this width, so a pane left
// open on a desktop is not reopened over a phone's transcript.
const WIDE = '(min-width: 1024px)';

// viewOf is what a room keeps: only what can be restored as it was.
// Uploading or failed files are left out — the bytes never reached the
// server, so another page could not send them.
export function viewOf(r) {
  const doc = {};
  if (r.draft) doc.draft = r.draft;
  const refs = liveReferences(r.draft, r.refs);
  if (refs.length) doc.refs = refs;
  if (r.quotes.length) doc.quotes = r.quotes.map(({ key, text, reply, from }) => ({ key, text, reply, from }));
  const files = r.files.filter((f) => f.status === 'ready').map((f) => (f.pickId
    ? { key: f.key, pickId: f.pickId, marker: f.marker, comment: f.comment, screenshot: f.screenshot, kind: f.kind, label: f.label, name: f.name }
    : { key: f.key, id: f.id, name: f.name, size: f.size, mediaType: f.mediaType }));
  if (files.length) doc.files = files;
  if (r.recording) doc.recording = { id: r.recording.id, status: r.recording.status, label: r.recording.label };
  // A pane kept from a wide screen but not opened on this narrow one is
  // still kept, so a phone does not close it for the desktop.
  const pane = r.pane === undefined ? r.paneKept : r.pane;
  if (pane) doc.pane = pane;
  if (r.browserUrl) doc.browserUrl = r.browserUrl;
  return doc;
}

// applyView fills a room from a kept view. What the owner already did in
// this page wins: a draft typed before the view arrived is not replaced,
// and a pane they opened or closed stays as they set it.
export function applyView(r, doc, wide = true) {
  if (!doc || typeof doc !== 'object') return;
  if (!r.draft && !r.files.length && !r.quotes.length && !r.recording) {
    r.draft = typeof doc.draft === 'string' ? doc.draft : '';
    r.refs = Array.isArray(doc.refs) ? liveReferences(r.draft, doc.refs) : [];
    r.quotes = Array.isArray(doc.quotes) ? doc.quotes.filter((q) => q && typeof q.text === 'string') : [];
    r.files = Array.isArray(doc.files) ? doc.files.filter((f) => f && (f.id || f.pickId)).map((f) => ({ ...f, status: 'ready' })) : [];
    if (doc.recording && typeof doc.recording.id === 'string') {
      r.recording = { id: doc.recording.id, status: doc.recording.status || '', label: doc.recording.label || 'Browser recording' };
    }
    if (r.draft) r.caret = r.draft.length;
  }
  if (r.pane === undefined && typeof doc.pane === 'string') {
    if (wide) r.pane = doc.pane;
    else r.paneKept = doc.pane;
  }
  if (!r.browserUrl && typeof doc.browserUrl === 'string') r.browserUrl = doc.browserUrl;
}

// loadView reads a room's kept view once per page. Saving starts only
// after it answered, so an empty room never overwrites what was kept.
export async function loadView(r) {
  if (r.view) return;
  r.view = { ready: false, sent: '', timer: 0 };
  try {
    const res = await api.view(r.id);
    applyView(r, res.view?.document, matchMedia(WIDE).matches);
    r.view.sent = JSON.stringify(res.view?.document || {});
  } catch { /* nothing kept, or unreachable: start from what is here */ }
  r.view.ready = true;
  sync();
}

function save(r, keepalive = false) {
  const v = r.view;
  clearTimeout(v.timer);
  v.timer = 0;
  const doc = viewOf(r);
  const body = JSON.stringify(doc);
  if (body === v.sent) return;
  // A room that turned into a thread under way keeps its own view; the
  // fresh room's id is null.
  const id = r.id;
  v.sent = body;
  api.saveView(id, doc, keepalive).catch(() => {
    // A deleted thread answers 404 and needs no retry; anything else is
    // tried again on the next change.
    if (r.view === v && v.sent === body) v.sent = '';
  });
}

// sync schedules a save for every loaded room whose view changed.
function sync() {
  for (const r of Object.values(state.rooms)) {
    const v = r.view;
    if (!v?.ready || r.infoStatus === 'notfound') continue;
    if (JSON.stringify(viewOf(r)) === v.sent) { clearTimeout(v.timer); v.timer = 0; continue; }
    if (!v.timer) v.timer = setTimeout(() => save(r), SAVE_DELAY);
  }
}

// flush sends every pending save now — the page is going away.
function flush() {
  for (const r of Object.values(state.rooms)) if (r.view?.timer) save(r, true);
}

export function startViews() {
  subscribe(sync);
  addEventListener('pagehide', flush);
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') flush(); });
}
