// Tiny app store: one mutable state tree, change notification batched per
// animation frame, and a Preact hook that re-renders subscribers. Streaming
// frames mutate in place and call `emit()`, so a burst of deltas costs one
// render.
import { useLayoutEffect, useReducer, useRef } from '/vendor/preact/htm-preact.mjs';

export const state = {
  conn: 'connecting',          // connecting | open | closed
  route: { view: 'home' },     // { view: 'home' | 'chat' | 'settings' | 'missing', id?, section?, path? }
  // The mobile drawer. Writes go through setDrawerOpen, so toasts are held while it is open.
  get sidebarOpen() { return drawerOpen; },
  set sidebarOpen(open) { setDrawerOpen(open); },
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
let asked = 0;
export function emit() {
  asked++;
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

// emitCount answers how many changes the store was asked to render, coalesced
// or not. A flush count taken across a wait mixes in whatever else changed
// meanwhile (a toast dismissing on its own timer, a background refresh), so a
// check that only one action asked for a render reads this count on both sides
// of that action instead.
export function emitCount() {
  return asked;
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
// While the nav drawer is closed, state.toasts[0] is the toast on screen. The drawer
// and a toast both sit at the top left, and a toast under the open drawer covers its
// links (Settings: all of it at 390px). So while the drawer is open nothing is on
// screen: the toast that was shown is held with the time it had left, and what
// arrives waits. Behind the screen wait one notice and a line of action toasts.
let drawerOpen = false;
let held = null;             // the toast the open drawer is holding back
let left = 0;                // ms the screen's (or the held) toast's time limit has to run
let since = 0;               // when that toast's clock last started
let hideTimer = 0;
let waitingNotice = null;
const waitingActions = [];

// action is an optional { label, run } button, e.g. Undo; using it dismisses the
// toast. toast shows a message and answers its id, for a message with no time
// limit (ms 0) that the caller takes down itself with dismissToast.
// One toast is on screen at a time. A notice replaces the notice showing rather
// than stacking under it. An action toast is never replaced by a notice: while one
// shows, a notice waits in one slot (the newest notice takes it) and appears when
// the action is used or its time runs out. An action that arrives meanwhile queues
// behind the one showing, in order, and none is dropped. Nothing shows while the
// drawer is open, and a toast's time limit runs only while it is on screen.
export function toast(message, kind = 'info', ms = kind === 'error' ? 8000 : 3500, action = null) {
  const t = { id: ++toastSeq, message, kind, action, ms };
  if (!drawerOpen && !state.toasts[0]?.action) show(t);
  else if (action) waitingActions.push(t);
  else waitingNotice = t;
  return t.id;
}

// dismissToast takes a toast down. The one on screen hands the screen to what waits
// behind it; a held toast is let go; a toast still waiting is cancelled; one already
// gone is a no-op.
export function dismissToast(id) {
  const shown = state.toasts[0];
  if (shown && shown.id === id) {
    show(nextWaiting());
    return;
  }
  if (held?.id === id) {
    held = null;
    return;
  }
  if (waitingNotice?.id === id) waitingNotice = null;
  const i = waitingActions.findIndex((t) => t.id === id);
  if (i !== -1) waitingActions.splice(i, 1);
}

// setDrawerOpen records the nav drawer opening or closing; state.sidebarOpen goes
// through it. Opening holds the toast on screen, its clock stopped with the time it
// had left. Closing shows the held toast again, running on from there, or else what
// waits behind.
function setDrawerOpen(open) {
  if (open === drawerOpen) return;
  drawerOpen = open;
  if (!open) {
    if (held) {
      const t = held;
      held = null;
      state.toasts = [t];
      run(t);
      emit();
    } else {
      show(nextWaiting());
    }
    return;
  }
  const shown = state.toasts[0];
  if (shown) {
    clearTimeout(hideTimer);
    hideTimer = 0;
    if (shown.ms) left = Math.max(0, left - (Date.now() - since));
    state.toasts = [];
    held = (!shown.ms || left > 0) ? shown : null;
  }
  emit();
}

function nextWaiting() {
  if (waitingActions.length) return waitingActions.shift();
  const t = waitingNotice;
  waitingNotice = null;
  return t;
}

// show puts t on screen (null clears the screen) with its whole time limit.
function show(t) {
  clearTimeout(hideTimer);
  hideTimer = 0;
  state.toasts = t ? [t] : [];
  left = t ? t.ms : 0;
  if (t) run(t);
  emit();
}

// run starts the clock of the toast on screen with the time it has left.
function run(t) {
  since = Date.now();
  if (t.ms && left > 0) hideTimer = setTimeout(() => dismissToast(t.id), left);
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
