// Tiny app store: one mutable state tree, change notification batched per
// animation frame, and a Preact hook that re-renders subscribers. Streaming
// frames mutate in place and call `emit()`, so a burst of deltas costs one
// render.
import { useLayoutEffect, useReducer, useRef } from '/vendor/preact/htm-preact.mjs';

export const state = {
  conn: 'connecting',          // connecting | open | closed
  route: { view: 'home' },     // { view: 'home' | 'chat' | 'settings' | 'missing', id?, section?, path? }
  sidebarOpen: false,          // mobile drawer
  returnTo: '#/',              // where Settings' back link goes
  convs: { items: [], total: 0, hasMore: false, status: 'loading', error: null, more: false },
  deleting: [],                // ids of threads whose delete is in flight
  providers: { items: [], status: 'loading', error: null },
  agents: { items: [], status: 'loading', error: null },
  projects: { items: [], status: 'loading', error: null },
  // projectThreads: project id → its threads, fetched when the project
  // has more than the main list paged in.
  projectThreads: {},
  newProjectId: '',            // project the next new thread opens in
  projectEdit: null,           // project in the editor, { isNew: true }, or null
  agentModels: {},             // agentId -> { status, list, error }
  threadDefault: null,         // models.threads: { agent, provider, model } a new thread starts on
  rooms: {},                   // conversationId (or fresh key) -> Room
  update: null,
  auth: { phase: 'checking', required: false, hasPassword: false, error: null }, // checking | login | ready
  toasts: [],
  preview: null,               // { url, name, line, key } — the file preview dialog
  confirm: null,               // the question being asked, with its resolve — see askConfirm
  prefs: loadPrefs(),
};

const subs = new Set();
let queued = false;
let version = 0;
export function emit() {
  if (queued) return;
  queued = true;
  // rAF batches deltas per frame; the timeout keeps hidden tabs (where
  // rAF pauses) current.
  const flush = () => {
    if (!queued) return;
    queued = false;
    version++;
    for (const fn of [...subs]) fn();
  };
  requestAnimationFrame(flush);
  setTimeout(flush, 100);
}

// subscribe hears every flush; it returns the unsubscribe.
export function subscribe(fn) {
  subs.add(fn);
  return () => subs.delete(fn);
}

export function useStore() {
  const [, force] = useReducer((n) => n + 1, 0);
  const seen = useRef(version);
  seen.current = version;
  useLayoutEffect(() => {
    subs.add(force);
    // A flush between render and subscribe would otherwise be missed.
    if (seen.current !== version) force();
    return () => subs.delete(force);
  }, []);
  return state;
}

let toastSeq = 0;
// action is an optional { label, run } button, e.g. Undo; using it
// dismisses the toast.
// toast shows a message and answers its id, for a message with no time limit
// (ms 0) that the caller takes down itself with dismissToast.
export function toast(message, kind = 'info', ms = kind === 'error' ? 8000 : 3500, action = null) {
  const t = { id: ++toastSeq, message, kind, action };
  state.toasts = [...state.toasts, t];
  emit();
  if (ms) setTimeout(() => dismissToast(t.id), ms);
  return t.id;
}
export function dismissToast(id) {
  state.toasts = state.toasts.filter((t) => t.id !== id);
  emit();
}

// askConfirm asks the owner a yes-or-no question in a dialog and answers
// whether they said yes; it is what confirm() would be. Opts: title, body,
// confirm (the button's label), danger (the yes is destructive). Asking
// while another question is open answers the older one no.
export function askConfirm({ title, body = '', confirm = 'Confirm', danger = false }) {
  return new Promise((resolve) => {
    state.confirm?.resolve(false);
    state.confirm = { title, body, confirm, danger, resolve };
    emit();
  });
}

// UI preferences only (theme, last model choice) — never conversation
// content or credentials.
function loadPrefs() {
  try { return { theme: 'system', ...JSON.parse(localStorage.getItem('nusashell.prefs') || '{}') }; }
  catch { return { theme: 'system' }; }
}
export function setPref(key, value) {
  state.prefs = { ...state.prefs, [key]: value };
  try { localStorage.setItem('nusashell.prefs', JSON.stringify(state.prefs)); } catch { /* private mode */ }
  emit();
}
