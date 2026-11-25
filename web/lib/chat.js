// Chat controller: owns the socket, the per-room state, and every command.
// Components read `state` and call these functions; nothing else talks to
// the socket.
import { state, emit, toast, dismissToast, askConfirm } from './store.js';
import { api, errorText, ApiError } from './api.js';
import { TurnSocket, newRequestId } from './socket.js';
import { newTurn, applyFrame, snapshotSegments } from './turn.js';
import { forgetQueued, resetQueued, setQueued } from './queued.js';
import { referencesForSend, referencesToDraft, liveReferences } from './references.js';
import { parseCommand, isNativeCommand, catalogRow, loadCommands } from './commands.js';
import { loadView, startViews } from './views.js';

const HISTORY_PAGE = 60;
const FRESH = 'new';
// ROOM_TTL is how long a successful room read counts as fresh — inside
// the window repeat triggers share or skip instead of firing another
// request. Broadcast staleness (an update the room did not watch) always
// beats the TTL.
const ROOM_TTL = 5_000;

// requestId -> { key, kind, text, messageId? } for commands awaiting a reply.
const requests = new Map();

// queuedRooms holds the rooms whose queue a frame said was not empty and whose
// drain has not been handled yet. A drain is what says the job that waited has
// finished, so the room is read once then — and read once again when an
// activity frame or a reconnect is the only news that comes, because a job can
// die with its worker and a server can stop before sending `waiting: 0`.
const queuedRooms = new Set();

// readQueuedRoom reads the room's history because its queue moved. It reads
// only a room this tab holds and only the room on screen: another room loads
// when it is opened, and this is a read, never a poll.
function readQueuedRoom(id, { force = false } = {}) {
  const r = state.rooms[id];
  if (!r || r.live || state.route.view !== 'chat' || state.route.id !== r.id) return;
  // force is for the drain itself: the read an activity frame just made can
  // predate the message that ended the job, so the TTL must not answer for it.
  loadHistory(r, { force }).catch(() => { /* the next read shows it */ });
}
let socket;

// ---------- rooms ----------

function blankRoom(key, id) {
  return {
    key, id,
    info: null, infoStatus: id ? 'loading' : 'ready', infoError: null,
    messages: [], hist: { status: id ? 'loading' : 'ready', error: null, start: 0, total: 0, more: false },
    live: null, pendingOpen: null, settling: false,
    failed: null, queue: [], steers: [],
    // command is the native command running on this room right now (its name),
    // and catalog what the server says this room offers: the native commands
    // and its agent's own, with availability. Both follow the room, not a tab.
    // null: no command running; a receipt object while one is (see runCommand).
    command: null, catalog: { status: 'idle', items: [], error: null }, catalogAt: 0,
    draft: '', refs: [], files: [], sel: {}, ctx: null, recording: null,
    // quotes are passages picked from a transcript to answer, each with
    // the owner's reply to it, riding above the draft until it is sent.
    quotes: [],
    // caret asks the composer to focus with the caret at that draft offset
    // after the draft changed from outside it, and dictate to start voice
    // input there; sideOf is the thread a side chat on the fresh room was
    // started from.
    caret: null, dictate: false, sideOf: null,
    // pane is the side pane tab left open (undefined until the owner or
    // the kept view decides; paneKept is a kept one a narrow screen did
    // not open), browserUrl the last page its browser showed, and view the
    // bookkeeping that keeps them, and the draft, on the server (views.js).
    pane: undefined, paneKept: '', browserUrl: '', view: null,
    // agentOptions is the agent session options of this room: the one list the
    // composer's chip and the picker's Session Mode both read (see
    // loadAgentOptions). A room without an id keeps the agent's own probe here
    // and stages the pick on `sel`.
    agentOptions: { status: 'idle', list: [], error: null, at: 0, key: '' },
    // Load bookkeeping: *At stamps a successful read, *Stale marks what
    // a broadcast said changed while the room was out of view, *Flight
    // holds the running fetch so concurrent triggers share it.
    infoAt: 0, histAt: 0, ctxAt: 0,
    infoStale: true, histStale: true, ctxStale: true,
  };
}

export function room(key) {
  if (!state.rooms[key]) state.rooms[key] = blankRoom(key, key === FRESH ? null : key);
  return state.rooms[key];
}
export const freshRoom = () => room(FRESH);
export const isBusy = (r) => !!(r.live || r.pendingOpen || r.settling);

// roomBusy is what a composer answers to: the room is working, or a native
// command it started is still running. A command is not a turn — it has no
// live turn to hold — so Enter during one queues instead of sending into a
// refusal. isBusy stays the narrower question (a turn is running), which edit,
// retry, and the header's Compact button ask.
export const roomBusy = (r) => isBusy(r) || !!r.command;

// ---------- routing ----------

export function navigate(hash) {
  if (location.hash !== hash) location.hash = hash;
  else applyRoute();
}

function applyRoute() {
  // A URL the app did not mint: a path-style thread link (/c/<id>) — what
  // an address copied without its hash looks like — folds into the route
  // the app owns, and any other path is the app's not-found view rather
  // than the home screen under an address that cannot be routed. Either
  // way the address bar is rewritten, so a reload or a copy stays routable.
  const path = location.pathname || '/';
  if (path !== '' && path !== '/') {
    const link = /^\/c\/([^/]+)\/?$/.exec(path);
    if (!link) {
      history.replaceState(null, '', '/');
      state.route = { view: 'missing', path };
      state.sidebarOpen = false;
      emit();
      return;
    }
    history.replaceState(null, '', '/#/c/' + link[1]);
  }
  const h = location.hash.replace(/^#\/?/, '');
  const [a, b] = h.split('/');
  if (a === 'c' && b) {
    try { state.route = { view: 'chat', id: decodeURIComponent(b) }; }
    catch (err) {
      if (!(err instanceof URIError)) throw err;
      state.route = { view: 'home' };
    }
  } else if (a === 'settings') state.route = { view: 'settings', section: b || 'providers', arg: h.split('/')[2] };
  else state.route = { view: 'home' };
  // Settings' back link returns to the thread (or home) it was opened from.
  if (state.route.view !== 'settings') state.returnTo = location.hash || '#/';
  state.sidebarOpen = false;
  if (state.route.view === 'chat') openRoom(state.route.id);
  if (state.route.view === 'home') fetchContext(freshRoom());
  emit();
}

// ---------- boot ----------

export function start() {
  window.addEventListener('hashchange', applyRoute);
  socket = new TurnSocket({
    onFrame,
    onState: (s, info) => { state.conn = s; if (s === 'closed') closeCode = info?.code; emit(); },
    onOpen: reattach,
  });
  loadConversations();
  loadProviders();
  loadThreadDefault();
  loadAgents();
  loadProjects();
  checkUpdate();
  setInterval(checkUpdate, 60 * 60_000);
  startViews();
  loadView(freshRoom());
  applyRoute();
}

export async function loadConversations(more = false) {
  const c = state.convs;
  // Concurrent non-paging triggers share one fetch — an activity burst
  // or a reconnect must not fire the same list request twice. Every
  // accepted call really refreshes: callers are already debounced.
  if (!more && c.flight) return c.flight;
  if (more) c.more = true; else if (!c.items.length) c.status = 'loading';
  emit();
  const flight = (async () => {
  try {
    // A refresh asks for as much as the reader has paged in (the server caps
    // a page at 100), so it can see which of those threads still exist.
    const limit = more ? 30 : Math.min(100, Math.max(30, c.items.length));
    // A change applied here while this page was in flight is newer than the
    // page: read again for one that carries it (twice at most).
    let page;
    for (let attempt = 0; ; attempt++) {
      const seen = writes;
      page = await api.conversations(more ? c.items.length : 0, limit);
      if (writes === seen || attempt === 1) break;
    }
    // Every row carries the room's queue depth: seeding from the rows is what
    // brings a lost count back without waiting for the next drain frame.
    for (const it of page.data) setQueued(it.id, it.waiting ?? 0);
    const items = more ? [...c.items, ...page.data.filter((x) => !c.items.some((y) => y.id === x.id))] : page.data;
    // A refresh that came back short saw the whole list, and a thread it does
    // not list is gone (deleted from here or elsewhere). Only a full page may
    // have left out threads paged in beyond it, and those stay, so the list
    // never shrinks under the reader.
    if (!more && items.length === limit && c.items.length > items.length) {
      for (const it of c.items.slice(items.length)) if (!items.some((x) => x.id === it.id)) items.push(it);
    }
    const same = !more && items.length === c.items.length && items.every((x, i) => sameRecords(x, c.items[i]));
    const next = { total: page.page.total, hasMore: items.length < page.page.total, status: 'ready', error: null };
    Object.assign(c, same ? next : { ...next, items });
    if (!more) {
      c.at = Date.now();
      // The list row carries the room's live turn — a tab that missed
      // the activity notice (a reconnect, for one) still finds and
      // reattaches to the running turn on the next list refresh. An
      // idle visible room marked stale by a missed broadcast gets its
      // one refresh here, not on every trigger.
      const r = state.route.view === 'chat' ? state.rooms[state.route.id] : null;
      if (r && !r.live) {
        const row = page.data.find((x) => x.id === r.id);
        if (row?.activeTurnId) watch(r, row.activeTurnId, row.activeTurnKind, -1);
        else if (r.histStale || r.infoStale) {
          loadInfo(r);
          loadHistory(r);
          fetchContext(r);
        }
      }
    }
  } catch (err) {
    if (more) toast(errorText(err), 'error');
    else if (!c.items.length) Object.assign(c, { status: 'error', error: errorText(err) });
    else toast('Could not refresh threads: ' + errorText(err), 'error');
  } finally {
    c.more = false;
    emit();
  }
  })();
  if (!more) c.flight = flight;
  try { await flight; } finally { if (c.flight === flight) c.flight = null; }
}

let convTimer;
// refreshConversationsSoon batches bursts of list-affecting notices into
// one refresh after `ms` — activity storms (and reconnects) collapse to
// a single load instead of one request per frame.
function refreshConversationsSoon(ms = 250) {
  clearTimeout(convTimer);
  convTimer = setTimeout(() => {
    loadConversations();
    // Thread counts and the project lists change with the threads.
    loadProjects();
    for (const pid of Object.keys(state.projectThreads)) loadProjectThreads(pid);
  }, ms);
}

// PROJECT_THREADS caps one project's list; the sidebar says when more exist.
export const PROJECT_THREADS = 100;

// loadProjectThreads fetches one project's threads, whatever the main
// list has paged in.
export async function loadProjectThreads(pid) {
  state.projectThreads = { ...state.projectThreads, [pid]: state.projectThreads[pid] || [] };
  try {
    let page;
    for (let attempt = 0; ; attempt++) {
      const seen = writes;
      page = await api.conversations(0, PROJECT_THREADS, pid);
      if (writes === seen || attempt === 1) break;
    }
    const cur = state.projectThreads[pid] || [];
    const same = page.data.length === cur.length && page.data.every((x, i) => sameRecords(x, cur[i]));
    if (!same) state.projectThreads = { ...state.projectThreads, [pid]: page.data };
  } catch { /* the main list still shows what it has */ }
  emit();
}

// loadThreadDefault reads what a new thread starts on (Settings → Models →
// New threads); unset leaves the last model picked in this browser.
async function loadThreadDefault() {
  try {
    const d = await api.settings();
    state.threadDefault = d.models?.threads || null;
    emit();
  } catch { /* the last pick in this browser still applies */ }
}

export async function loadProviders() {
  const g = state.providers;
  g.status = g.items.length ? 'ready' : 'loading';
  emit();
  try {
    const page = await api.providers();
    Object.assign(g, { items: page.data, status: 'ready', error: null });
  } catch (err) {
    Object.assign(g, { status: 'error', error: errorText(err) });
  }
  emit();
}

export async function loadProjects() {
  const p = state.projects;
  p.status = p.items.length ? 'ready' : 'loading';
  emit();
  try {
    const page = await api.projects();
    Object.assign(p, { items: page.data, status: 'ready', error: null });
    if (state.newProjectId && !p.items.some((x) => x.id === state.newProjectId)) state.newProjectId = '';
  } catch (err) {
    Object.assign(p, { status: 'error', error: errorText(err) });
  }
  emit();
}

// newThreadIn opens the home composer aimed at a project ('' = none).
export function newThreadIn(projectId) {
  state.newProjectId = projectId || '';
  navigate('#/');
}

export async function loadAgents() {
  const a = state.agents;
  a.status = a.items.length ? 'ready' : 'loading';
  emit();
  try {
    const page = await api.agents();
    Object.assign(a, { items: page.data, status: 'ready', error: null });
  } catch (err) {
    Object.assign(a, { status: 'error', error: errorText(err) });
  }
  emit();
}

// agentModels loads an agent's model list once; the first call may start
// the agent process. force re-asks after an agent changed or failed.
export async function loadAgentModels(agentId, force = false) {
  const cur = state.agentModels[agentId];
  if (cur && !force && cur.status !== 'error') return;
  state.agentModels[agentId] = { status: 'loading', list: cur?.list || [], error: null };
  emit();
  try {
    const r = await api.agentModels(agentId);
    state.agentModels[agentId] = { status: 'ready', list: r.data || [], error: null };
  } catch (err) {
    state.agentModels[agentId] = { status: 'error', list: [], error: errorText(err) };
  }
  emit();
}

async function checkUpdate() {
  try { state.update = await api.update(); emit(); } catch { /* the About page surfaces errors */ }
}

// ---------- room loading ----------

// openRoom makes the room's reads current: the loaders themselves decide
// whether a fetch is owed (fresh, un-staled data skips it), so opening a
// room that is already loaded costs no request at all.
export async function openRoom(id) {
  const r = room(id);
  loadView(r);
  // The command catalog is the room's: another room has another set.
  loadCommands(r);
  if (r.live) { loadInfo(r); return; }
  await Promise.all([loadInfo(r), loadHistory(r)]);
  if (r.info?.activeTurnId && !r.live) watch(r, r.info.activeTurnId, r.info.activeTurnKind, -1);
  fetchContext(r);
  loadAgentOptions(r);
  // A queue this room was left with (a command that moved the route, a turn
  // that ended elsewhere) waits for the owner to come back: opening the room
  // is that moment, so it sends rather than sitting there for good. A queue
  // the owner put on hold stays held.
  if (!r.held && !roomBusy(r) && r.queue.length) resumeQueue(r);
}

// fresh reports whether a load timestamp is still inside the TTL.
const fresh = (at) => at && Date.now() - at < ROOM_TTL;

// loadInfo reads the room record — deduped (concurrent triggers share
// one fetch) and TTL-gated: a fresh, un-staled read skips the request
// entirely. force and a marked stale both defeat the TTL.
export async function loadInfo(r, { force = false, created = false } = {}) {
  if (r.infoFlight) {
    // A need that lands mid-flight must not be answered by the older
    // data it returns — queue one follow-up behind the running fetch.
    if (force || r.infoStale) r.infoAgain = true;
    return r.infoFlight;
  }
  if (!force && !r.infoStale && fresh(r.infoAt)) return;
  const flight = (async () => {
    let raced = 0;
    do {
      r.infoAgain = false;
      for (let attempt = 0; ; attempt++) {
        const seen = writes;
        try {
          const info = await api.conversation(r.id);
          // A change applied here while this read was in flight outranks the
          // record it answered with: read again (twice at most, so a stream
          // of changes cannot pin the header).
          if (writes !== seen && raced++ < 2) {
            r.infoAgain = true;
            break;
          }
          if (!sameRecords(info, r.info)) r.info = info;
          // The room's queue depth rides its own record. A read is newer than
          // any frame that arrived before it, so it seeds the line — and a leaf
          // the record omits means the queue is empty, not unknown.
          setQueued(r.id, info.waiting ?? 0);
          r.infoStatus = 'ready';
          r.infoError = null;
          r.infoAt = Date.now();
          r.infoStale = false;
        } catch (err) {
          // turn.opened mints the room before the worker persists it, so
          // a first read of a brand-new room can race ahead of that write
          // and answer 404. One short retry turns the race into a loading
          // header instead of a "thread not found" screen; a room deleted
          // in the same window still falls through to notfound.
          if (created && err.status === 404 && !r.info && attempt === 0) {
            await new Promise((resolve) => setTimeout(resolve, 200));
            continue;
          }
          r.infoStatus = err.status === 404 ? 'notfound' : 'error';
          r.infoError = errorText(err);
        }
        break;
      }
      emit();
    } while (r.infoAgain);
  })();
  r.infoFlight = flight;
  try { await flight; } finally { if (r.infoFlight === flight) r.infoFlight = null; }
}

// loadHistory replaces the newest page — deduped and TTL-gated like
// loadInfo, so a terminal frame plus the activity notice that follows
// collapse to one fetch.
export async function loadHistory(r, { force = false } = {}) {
  if (r.histFlight) {
    // A need that lands mid-flight must not be answered by the older
    // data it returns — queue one follow-up behind the running fetch.
    if (force || r.histStale) r.histAgain = true;
    return r.histFlight;
  }
  if (!force && !r.histStale && fresh(r.histAt)) return;
  const flight = (async () => {
    do {
      r.histAgain = false;
      r.hist.status = r.messages.length ? 'refreshing' : 'loading';
      emit();
      try {
        const page = await api.messages(r.id, -HISTORY_PAGE, HISTORY_PAGE);
        r.messages = page.messages;
        Object.assign(r.hist, { status: 'ready', error: null, start: page.effectiveStart, total: page.total, more: page.effectiveStart > 0 });
        r.histAt = Date.now();
        r.histStale = false;
      } catch (err) {
        if (err.status === 404) { r.infoStatus = 'notfound'; r.hist.status = 'ready'; }
        else Object.assign(r.hist, { status: r.messages.length ? 'ready' : 'error', error: errorText(err) });
        if (r.messages.length) toast('Could not refresh history: ' + errorText(err), 'error');
      }
      emit();
    } while (r.histAgain);
  })();
  r.histFlight = flight;
  try { await flight; } finally { if (r.histFlight === flight) r.histFlight = null; }
}

export async function loadEarlier(r) {
  if (r.hist.loadingEarlier || r.hist.start <= 0) return;
  r.hist.loadingEarlier = true;
  r.hist.earlierError = null;
  emit();
  try {
    const start = Math.max(0, r.hist.start - HISTORY_PAGE);
    const page = await api.messages(r.id, start, r.hist.start - start);
    const known = new Set(r.messages.map((m) => m.id));
    r.messages = [...page.messages.filter((m) => !known.has(m.id)), ...r.messages];
    r.hist.start = page.effectiveStart;
    r.hist.more = page.effectiveStart > 0;
  } catch (err) {
    r.hist.earlierError = errorText(err);
  } finally {
    r.hist.loadingEarlier = false;
    emit();
  }
}

// fetchContext reads the picked model's window and the room's size from the
// server. The size a provider reported for its last request ("live") is the
// truer figure and the stream keeps it current, so a refresh at the end of a
// turn keeps it; only a change to the history itself (a fold) or to the
// model drops it, leaving the server's estimate until the next request. An
// agent keeps its own context and reports it: a room already bound to the
// picked agent asks for that report, and a pick the room has not run yet has
// none to show.
export async function fetchContext(r, { keepLive = false, force = false } = {}) {
  const sel = selection(r);
  const agentRoom = !!sel.agentId && !!r.id && sel.agentId === r.info?.agentId;
  if ((sel.agentId && !agentRoom) || (!r.id && !(sel.providerId && sel.modelId))) { r.ctx = null; emit(); return; }
  if (r.ctxFlight) {
    if (force || r.ctxStale) r.ctxAgain = true;
    return r.ctxFlight;
  }
  if (!force && !r.ctxStale && fresh(r.ctxAt)) return;
  const flight = (async () => {
    do {
      r.ctxAgain = false;
      try {
        const next = await api.context(agentRoom ? { conversationId: r.id }
          : { conversationId: r.id, providerId: sel.providerId, modelId: sel.modelId });
        next.live = next.live ?? (keepLive ? r.ctx?.live ?? null : null);
        r.ctx = next;
        r.ctxAt = Date.now();
        r.ctxStale = false;
      } catch { r.ctx = null; }
      emit();
    } while (r.ctxAgain);
  })();
  r.ctxFlight = flight;
  try { await flight; } finally { if (r.ctxFlight === flight) r.ctxFlight = null; }
}

// ---------- selection ----------

// selection resolves what the next command runs with: explicit composer
// choice, else the room's binding, else — for a thread not started — the
// owner's default for new threads, else the last model the user picked.
// A choice is either an API provider or an agent; both carry a
// modelId. upstream pins the backend behind an aggregator provider.
export function selection(r) {
  const p = state.prefs.lastModel || {};
  // A room carries its own model, effort, and upstream pin: they are stored
  // with it, so they are what the picker shows after a reload or a restart.
  const bound = r.info?.agentId ? { agentId: r.info.agentId, modelId: r.info.modelId }
    : r.info?.providerId ? { providerId: r.info.providerId, modelId: r.info.modelId, effort: r.info.effort, upstream: r.info.upstream } : null;
  const t = !r.id && state.threadDefault;
  const fallback = t?.agent ? { agentId: t.agent, modelId: t.model }
    : t?.provider && t?.model ? { providerId: t.provider, modelId: t.model, effort: t.effort || 'default' } : null;
  const base = r.sel.providerId || r.sel.agentId ? r.sel : bound || fallback || p;
  return {
    agentId: base.agentId || '',
    providerId: base.agentId ? '' : base.providerId || '',
    modelId: base.modelId || '',
    effort: r.sel.effort ?? (bound ? bound.effort || 'default' : fallback ? (fallback.effort || 'default') : p.effort) ?? 'default',
    upstream: r.sel.upstream ?? (bound ? bound.upstream || '' : fallback ? '' : p.upstream) ?? '',
    workspace: r.sel.workspace ?? r.info?.workspace ?? '',
  };
}

export function setSelection(r, patch) {
  const prevAgent = r.sel.agentId;
  r.sel = { ...r.sel, ...patch };
  // A different agent has different options; a choice staged for the last
  // one must not follow it.
  if (patch.agentId !== undefined && patch.agentId !== prevAgent && !patch.agentOptions) {
    r.sel = { ...r.sel, agentOptions: undefined };
    // Another agent offers another list: the room's copy is read again, and
    // until it is, nothing of the last agent's is shown.
    r.agentOptions = { status: 'idle', list: [], error: null, at: 0, key: '' };
    loadAgentOptions(r, { force: true });
  }
  if (patch.providerId !== undefined || patch.agentId !== undefined || patch.modelId !== undefined || patch.effort !== undefined || patch.upstream !== undefined) {
    const s = selection(r);
    state.prefs = { ...state.prefs, lastModel: { agentId: s.agentId, providerId: s.providerId, modelId: s.modelId, effort: s.effort, upstream: s.upstream } };
    try { localStorage.setItem('nusashell.prefs', JSON.stringify(state.prefs)); } catch { /* ignore */ }
    // A turn that is running is not changed by this, and the room's own
    // worker is busy with it: a request to bind the room would wait out the
    // whole turn and time out. The choice rides the next message, which binds
    // the room when it runs; with none queued, the end of the turn binds it.
    if (isBusy(r)) {
      r.bindOnIdle = true;
    } else {
      fetchContext(r, { force: true });
      // The room keeps what the picker shows (its model, effort, and
      // upstream pin), so a turn started anywhere runs it and a reload shows
      // it. A change to the effort alone leaves the model binding alone.
      persistSelection(r, s, patch.providerId !== undefined || patch.agentId !== undefined || patch.modelId !== undefined);
    }
  }
  emit();
}

// ---------- agent session options ----------
//
// One list per room, one loader, one writer. The composer's chip and the
// picker's Session Mode read this and nothing else: a second copy is what made
// the two disagree — each kept the answer it happened to have.

// agentOptionsKey names what a room's list is read from: the room when it
// exists, else the agent standing in for a thread that does not exist yet.
const agentOptionsKey = (r) => {
  const agentId = selection(r).agentId;
  return r.id ? `c:${r.id}` : agentId ? `a:${agentId}` : '';
};

// loadAgentOptions reads the list the room shows. Deduped (one fetch in
// flight, a need that lands mid-flight queues one follow-up) and TTL-gated, so
// a frame, a component mounting, and a turn ending collapse into one read.
export async function loadAgentOptions(r, { force = false } = {}) {
  const o = r.agentOptions;
  const key = agentOptionsKey(r);
  if (!key) { o.status = 'idle'; o.list = []; return; }
  if (o.flight) {
    if (force) o.again = true;
    return o.flight;
  }
  // A list read for another agent says nothing about this one.
  if (o.key && o.key !== key) Object.assign(o, { status: 'idle', list: [], error: null, at: 0, key });
  if (!force && o.status === 'ready' && o.key === key && fresh(o.at)) return;
  const flight = (async () => {
    do {
      o.again = false;
      if (o.status !== 'ready') { o.status = 'loading'; emit(); }
      try {
        const agentId = selection(r).agentId;
        const res = r.id ? await api.agentOptions(r.id) : await api.agentOptionsFor(agentId);
        Object.assign(o, { status: 'ready', list: res.data || [], error: null, at: Date.now(), key });
      } catch (e) {
        Object.assign(o, { status: o.list.length ? 'ready' : 'error', error: errorText(e) });
        if (o.list.length) toast('Could not refresh agent options: ' + errorText(e), 'error');
      }
      emit();
    } while (o.again);
  })();
  o.flight = flight;
  try { await flight; } finally { if (o.flight === flight) o.flight = null; }
}

// sessionOptionValue is what both controls read: the room's stored pick, or —
// for a thread that does not exist yet — the one staged for it.
export function sessionOptionValue(r, o) {
  if (r.id) return o.current ?? '';
  const staged = r.sel.agentOptions || {};
  return staged[o.id] ?? o.current ?? '';
}

// sessionModeName is the word the composer chip shows: the name of the value
// the room stored, read from the same list the picker draws, so the two cannot
// name different modes.
export function sessionModeName(r) {
  const mode = r.agentOptions.list.find((o) => o.category === 'mode');
  if (!mode) return '';
  return (mode.choices || []).find((c) => c.value === sessionOptionValue(r, mode))?.name || '';
}

// setSessionOption is the only writer. A pick for a room is stored on the
// server, and the answer (the whole list) becomes what both controls show; a
// pick for a thread that does not exist yet is staged on its selection and
// rides the first turn. Nothing is ever shown as chosen before the server says
// it is.
export async function setSessionOption(r, optionId, value) {
  const o = r.agentOptions;
  const option = o.list.find((x) => x.id === optionId);
  if (!option) return 'missing';
  // A turn holds this room's worker: a write queued behind it would be
  // canceled by this request's own timeout before it ran, losing the change
  // without a trace. Refusing here is the same answer, said now.
  if (isBusy(r)) return 'busy';
  if (!r.id) {
    setSelection(r, { agentOptions: { ...(r.sel.agentOptions || {}), [optionId]: value } });
    return 'staged';
  }
  try {
    const res = await api.setAgentOption(r.id, optionId, value);
    Object.assign(o, { status: 'ready', list: res.data || [], error: null, at: Date.now(), key: agentOptionsKey(r) });
    emit();
    return 'saved';
  } catch (e) {
    // The stored value is the only one the controls may show: read it again
    // and keep that. A refusal says why once; a write whose fate is unknown
    // says it could not be confirmed and is not retried.
    const refused = typeof e?.status === 'number' && e.status >= 400 && e.status < 500;
    toast(refused
      ? `Could not set ${option.name}: ${errorText(e)}`
      : `Could not confirm ${option.name} — the room's options were read again.`, 'error');
    await loadAgentOptions(r, { force: true }).catch(() => { /* the next read shows it */ });
    return refused ? 'refused' : 'unknown';
  }
}

// writes counts the record changes this tab applied. The list and the room
// record are read by pulls (activity storms, reconnects, a paging reader), and
// a pull that started before a change answers with the state before it: the
// count lets the reader tell that answer apart and read again instead of
// putting the old title, pin, or project back over what the owner just did.
let writes = 0;

// recordKey is what a pulled record says about a room, as one comparable
// string: the fields a row and a header show. A pull that answers with the
// same key changed nothing, so nothing is written — replacing the row object
// would re-render the list (and the header) for the same values, which is
// what a stream of refreshes does all day. A different key is a change, and
// the record is applied.
const RECORD_FIELDS = ['title', 'projectId', 'pinned', 'memoryOff', 'label', 'providerId', 'modelId',
  'agentId', 'effort', 'upstream', 'workspace', 'messageCount', 'activeTurnId', 'activeTurnKind'];
const recordKey = (x) => (x ? RECORD_FIELDS.map((k) => x[k] ?? '').join('\u0000') : '');
const sameRecords = (a, b) => !!a && !!b && recordKey(a) === recordKey(b);

// applyRoomInfo stores a room's fresh record — the open room's info and every
// list row that shows it. A row keeps the live-turn fields, which the record
// projection does not carry, and replaces the rest rather than merging, so a
// cleared project or pin disappears.
export function applyRoomInfo(info) {
  writes++;
  if (state.rooms[info.id]) state.rooms[info.id].info = info;
  const row = (prev) => ({ ...info, activeTurnId: prev.activeTurnId, activeTurnKind: prev.activeTurnKind });
  const i = state.convs.items.findIndex((c) => c.id === info.id);
  if (i >= 0) state.convs.items[i] = row(state.convs.items[i]);
  let lists = null;
  for (const [pid, list] of Object.entries(state.projectThreads)) {
    const j = list.findIndex((x) => x.id === info.id);
    if (j < 0) continue;
    lists = lists || { ...state.projectThreads };
    lists[pid] = list.map((x, k) => (k === j ? row(x) : x));
  }
  if (lists) state.projectThreads = lists;
}

// persistSelection binds the room to what the picker shows, so a turn that
// starts anywhere — a WhatsApp or Telegram message, an automation, another
// browser — runs the model the owner picked. Without it the choice lived
// only in this browser: the picker read the new model off local state while
// the room kept answering on the one it was bound to last.
async function persistSelection(r, s, runtime = true) {
  // A room that does not exist yet takes the choice when its first turn
  // runs; there is nothing to bind.
  if (!r.id) return;
  if (s.agentId ? !runtime : !s.providerId || !s.modelId) return;
  // The effort and the upstream pin are the room's too, and go with the
  // model; an agent takes only its model.
  const body = s.agentId
    ? { agentId: s.agentId, ...(s.modelId ? { modelId: s.modelId } : {}) }
    : { ...(runtime ? { providerId: s.providerId, modelId: s.modelId } : {}), effort: s.effort, upstream: s.upstream };
  try {
    applyRoomInfo(await api.annotate(r.id, body));
    // The room carries the choice now, so the local override is spent:
    // dropping it lets the picker render the room's real binding, and a
    // change made from a chat shows up here instead of being masked.
    r.sel = { agentId: undefined, providerId: undefined, modelId: undefined, effort: undefined, upstream: undefined };
  } catch (err) {
    // The picker had already shown the choice; put it back to the room's
    // binding rather than leave a selection the server never took.
    r.sel = { agentId: undefined, providerId: undefined, modelId: undefined, effort: undefined, upstream: undefined };
    toast('Could not save this thread\'s model: ' + errorText(err), 'error');
  }
  emit();
}

// turnPayload is what a turn command carries of the picker. A send names the
// agent or the provider and model; a resend (an edit or a retry) runs on the
// thread's own runtime, which the picker has already bound, and its payload
// takes no agent at all — the server refuses fields it does not know.
function turnPayload(r, extra, resend = false) {
  const s = selection(r);
  const p = { ...extra };
  if (s.agentId) {
    if (!resend) {
      // Reasoning effort and upstream pins are provider settings; an agent
      // takes only its model.
      p.agentId = s.agentId;
      if (s.modelId) p.modelId = s.modelId;
      // Options staged before the thread existed ride the turn that opens
      // it. An existing thread keeps its own, written as they change.
      if (!r.id) {
        const staged = r.sel.agentOptions;
        if (staged && Object.keys(staged).length) p.agentOptions = staged;
      }
    }
  } else {
    if (s.providerId && s.modelId) { p.providerId = s.providerId; p.modelId = s.modelId; }
    // Always named, "default" included: absent means "the room's own", and
    // the picker saying default must not be read as that.
    p.effort = s.effort;
    if (s.upstream) p.upstream = s.upstream;
  }
  if (r.sel.workspace !== undefined && r.sel.workspace !== (r.info?.workspace || '')) p.workspace = r.sel.workspace;
  return p;
}

// ---------- commands ----------

function command(r, kind, cmd, text) {
  const requestId = newRequestId();
  if (!socket.send({ ...cmd, requestId })) {
    toast(text ? 'Not connected — your message was kept. Try again when the connection is back.'
      : 'Not connected — try again when the connection is back.', 'error');
    return null;
  }
  requests.set(requestId, { key: r.key, kind, text, turnId: cmd.turnId });
  return requestId;
}

const MAX_FILES = 10;
const MAX_UPLOAD = 25 * 1024 * 1024;

// restoreDraft puts text the server did not take back at the head of the
// draft, with its selections (byte offsets into text) and the recording
// chip a failed send was carrying.
function restoreDraft(r, text, references, files, recording) {
  if (recording && !r.recording) r.recording = recording;
  prependDraft(r, text, referencesToDraft(text || '', references), files);
}

// prependDraft restores an input given in editor units — refs already
// point into text, so a draft or a cross-room move can pass them through.
function prependDraft(r, text, refs, files) {
  if (files?.length) r.files = [...files, ...r.files];
  if (!text) return;
  const shift = r.draft ? text.length + 1 : 0;
  const kept = liveReferences(r.draft, r.refs).map((x) => ({ ...x, start: x.start + shift, end: x.end + shift }));
  r.draft = r.draft ? text + '\n' + r.draft : text;
  r.refs = [...liveReferences(text, refs), ...kept];
}

// submit is the composer's Enter: steer the running send turn, queue
// behind it, or send now. refs are the draft's selections (editor
// offsets); files are the uploads attached to the draft. All three
// modes carry the same message — they differ only in timing.
export function submit(r, text, refs = [], files = [], steerTurnId = '') {
  // A native command is an action, not a turn: recognized on the draft's first
  // line, it never becomes a message to the model or the agent — not even when
  // the room refuses it. The caller keeps the draft; only the server's 200
  // spends it.
  const cmd = parseCommand(text);
  if (cmd && isNativeCommand(r, cmd.name)) {
    // The command is spent from the draft here, once: submit is the only
    // place that knows the text it sent. What it carried — attachments,
    // quotes, a recording — belongs to the next message, not to the command.
    const sent = String(text || '').trim();
    r.draft = '';
    r.refs = [];
    r.caret = 0;
    runCommand(r, cmd.name, cmd.args, sent);
    return false;
  }
  if (files.some((f) => f.status === 'uploading')) {
    toast('Wait for the files to finish uploading.', 'info');
    return false;
  }
  if (files.some((f) => f.status === 'error')) {
    toast('Remove failed attachments before sending.', 'error');
    return false;
  }
  files = files.filter((f) => f.status === 'ready');
  const references = referencesForSend(text, refs);
  text = text.trim();
  if (!text && !files.length && !r.recording) return false;
  const message = { text, references, files, recording: r.recording };
  if (steerTurnId) {
    if (message.recording) {
      // A recording belongs to a new message — the server refuses it in
      // a steer. Keep the draft and the chip; the owner stops the turn.
      toast('A recording goes with a new message — wait for the turn to end or remove it.', 'info');
      return false;
    }
    if (r.live?.turnId !== steerTurnId || r.live.kind !== 'send' || r.live.terminal) {
      toast('That turn has ended. Your message is still in the draft.', 'info');
      return false;
    }
    const inputId = command(r, 'steer', { type: 'turn.steer', conversationId: r.id, turnId: steerTurnId, payload: messagePayload(message) }, text);
    if (!inputId) return false;
    r.steers.push({ ...message, inputId, turnId: steerTurnId, state: 'sending' });
    emit();
    return true;
  }
  if (roomBusy(r) || r.queue.length) {
    r.queue.push({ ...message, id: newRequestId() });
    // An explicit send in an idle room releases a held queue, oldest first.
    if (!roomBusy(r)) resumeQueue(r);
    emit();
    return true;
  }
  return sendNow(r, message);
}

// messagePayload is the wire body every mode shares: text, selections
// (byte offsets), uploads, live-page annotations (the items with a
// pickId), and a browser recording. Annotation screenshots ride after
// the files, in order.
function messagePayload({ text, references = [], files = [], recording = null }) {
  const extra = { content: text };
  if (references.length) extra.references = references;
  const uploads = files.filter((f) => !f.pickId).map((f) => f.id);
  const notes = files.filter((f) => f.pickId).map((f) => ({ pickId: f.pickId, comment: f.comment || undefined, screenshot: !!f.screenshot }));
  if (uploads.length) extra.uploads = uploads;
  if (notes.length) extra.annotations = notes;
  if (recording?.id) extra.recordingId = recording.id;
  return extra;
}

// sendNow sends the draft as a new turn — the only mode that carries the
// picker's provider/model/workspace selections.
function sendNow(r, message) {
  const { text } = message;
  const s = selection(r);
  if (!r.id && !s.agentId && !(s.providerId && s.modelId)) {
    toast('Choose a model before sending.', 'error');
    return false;
  }
  const extra = messagePayload(message);
  if (!r.id && state.newProjectId) extra.projectId = state.newProjectId;
  const requestId = command(r, 'send', {
    type: 'turn.send',
    conversationId: r.id,
    payload: turnPayload(r, extra),
  }, text);
  if (!requestId) return false;
  // The message carries the picker's choice and binds the room when it runs.
  r.bindOnIdle = false;
  r.pendingOpen = { requestId, text, references: message.references || [], files: message.files || [], recording: message.recording || null, kind: 'send', queued: message.id ? message : null };
  r.failed = null;
  emit();
  return true;
}

// attachFiles uploads files picked, dropped or pasted into the composer.
export function attachFiles(r, list) {
  for (const file of list) {
    if (r.files.filter((x) => !x.pickId).length >= MAX_FILES) { toast(`A message carries at most ${MAX_FILES} files.`, 'error'); break; }
    const f = { key: newRequestId(), name: file.name || 'pasted file', size: file.size, mediaType: file.type, status: 'uploading' };
    if (file.type.startsWith('image/')) f.preview = URL.createObjectURL(file);
    r.files.push(f);
    if (file.size > MAX_UPLOAD) { f.status = 'error'; f.error = 'Larger than 25 MB'; continue; }
    if (!file.size) { f.status = 'error'; f.error = 'The file is empty'; continue; }
    const ctl = new AbortController();
    f.abort = () => ctl.abort();
    api.upload(file, ctl.signal).then((res) => {
      Object.assign(f, { status: 'ready', id: res.upload.id, mediaType: res.upload.mediaType });
    }, (e) => {
      if (ctl.signal.aborted) return;
      Object.assign(f, { status: 'error', error: errorText(e) });
    }).finally(() => { f.abort = null; emit(); });
  }
  emit();
}

// annotate adds a mark made on the live page to the draft. It rides the
// files list so queueing and restoring carry it like a file.
export function annotate(r, pick, comment, screenshot) {
  const d = pick.descriptor || {};
  r.files.push({
    key: newRequestId(), status: 'ready', pickId: pick.pickId, marker: pick.marker, comment, screenshot,
    kind: d.kind, label: annotationLabel(d), name: `[${pick.marker}] ${annotationLabel(d)}`,
  });
  emit();
}

// nextMarker is the badge number the next mark in the draft gets.
export function nextMarker(r) {
  return Math.max(0, ...r.files.filter((f) => f.pickId).map((f) => f.marker || 0)) + 1;
}

// annotationLabel is how a mark reads in one line.
export function annotationLabel(d) {
  if (d.kind === 'region') return `region ${Math.round(d.rect?.width || 0)}×${Math.round(d.rect?.height || 0)}`;
  if (d.kind === 'page') return 'page' + (d.title ? ` "${d.title}"` : '');
  const name = d.name || d.immediateText || '';
  return `<${d.tagName || 'element'}>` + (name ? ` "${name.length > 40 ? name.slice(0, 40) + '…' : name}"` : '');
}

export function removeFile(r, key) {
  const f = r.files.find((x) => x.key === key);
  if (!f) return;
  f.abort?.();
  if (f.preview) URL.revokeObjectURL(f.preview);
  r.files = r.files.filter((x) => x !== f);
  emit();
}

export function recallSteer(r, inputId) {
  if (!r.live) return;
  command(r, 'recall', { type: 'turn.steer_recall', conversationId: r.id, turnId: r.live.turnId, payload: { inputId } });
}

export function cancel(r) {
  if (!r.live || r.live.terminal || r.live.canceling) return;
  if (!command(r, 'cancel', { type: 'turn.cancel', conversationId: r.id, turnId: r.live.turnId })) return;
  r.live.canceling = true;
  emit();
}

// waitingPrompt answers the owner prompt the room has not answered: the
// newest prompt with no assistant text after it. A room whose last word is an
// answer has nothing left to retry, and a note (a synthetic user line) is not
// a prompt. Null when there is no prompt at all.
export function waitingPrompt(room) {
  const msgs = (room.messages || []).filter((m) => m.role === 'user' || m.role === 'assistant');
  const at = msgs.findLastIndex((m) => m.role === 'user' && m.userType !== 'system');
  if (at < 0) return null;
  const answered = msgs.slice(at + 1).some((m) => m.role === 'assistant'
    && ((m.content || '').trim() !== '' || (m.parts || []).some((p) => p.kind === 'text' && (p.text || '').trim() !== '')));
  return answered ? null : msgs[at];
}

export function retry(r) {
  if (isBusy(r)) return;
  const requestId = command(r, 'resume', { type: 'turn.resume', conversationId: r.id, payload: turnPayload(r, {}, true) });
  if (!requestId) return;
  r.pendingOpen = { requestId, kind: 'resume' };
  emit();
}

export function editMessage(r, messageId, text) {
  text = text.trim();
  if (!text) return false;
  const requestId = command(r, 'edit', { type: 'turn.edit', conversationId: r.id, payload: turnPayload(r, { messageId, content: text }, true) }, text);
  if (!requestId) return false;
  r.pendingOpen = { requestId, text, kind: 'edit', messageId };
  r.failed = null;
  emit();
  return true;
}

export function removeQueued(r, id) {
  r.queue = r.queue.filter((q) => q.id !== id);
  emit();
}
export function restoreQueued(r, id) {
  const q = r.queue.find((x) => x.id === id);
  if (!q) return;
  const at = r.draft ? r.draft.length + 1 : 0;
  r.draft = r.draft ? r.draft + '\n' + q.text : q.text;
  r.refs = [...liveReferences(r.draft, r.refs), ...referencesToDraft(q.text, q.references, at)];
  r.files = [...r.files, ...(q.files || [])];
  if (q.recording && !r.recording) r.recording = q.recording;
  removeQueued(r, id);
}

function watch(r, turnId, kind, afterSeq) {
  if (!r.live || r.live.turnId !== turnId) {
    r.live = newTurn({ turnId, conversationId: r.id, kind: kind || 'send' });
    // adopted marks a turn this tab never opened — another browser, a chat
    // channel, or a thread relaying a prompt into this room. Its prompt is
    // not in hand, only in the room's history.
    r.live.adopted = true;
    // A live turn turns every native command off for the room.
    loadCommands(r, { force: true });
  }
  command(r, 'watch', { type: 'turn.watch', conversationId: r.id, turnId, afterSeq });
  emit();
}

// readNewestPage merges the room's newest page into what the transcript
// holds without dropping the earlier pages the reader loaded — for a change
// that landed under the room rather than through a turn this tab ran. It
// answers the page it read; a failed read changes nothing and the next load
// shows what it missed.
async function readNewestPage(r) {
  const page = await api.messages(r.id, -HISTORY_PAGE, HISTORY_PAGE);
  if (state.rooms[r.id] !== r) return null;
  const fresh = new Map(page.messages.map((m) => [m.id, m]));
  const known = new Set(r.messages.map((m) => m.id));
  r.messages = [...r.messages.map((m) => fresh.get(m.id) || m), ...page.messages.filter((m) => !known.has(m.id))];
  r.hist.total = page.total;
  r.histAt = Date.now();
  r.histStale = false;
  emit();
  return page;
}

// readLivePrompt shows the prompt of a turn this tab adopted. The server
// writes that user message just after the turn opens — no stream frame
// carries it — so the transcript would otherwise run the live output
// straight on from the previous turn's answer and show the prompt itself
// only once the turn ends and history reloads. Callers read when the turn
// opens (the notice may beat the write) and again at the turn's first
// streamed frame, which proves the write landed.
function readLivePrompt(r) {
  if (state.route.view !== 'chat' || state.route.id !== r.id) return;
  if (!r.live?.adopted || r.live.kind !== 'send') return;
  readNewestPage(r).catch(() => { /* the turn's end reads it */ });
}

// After a (re)connect: re-attach every live turn from its last seq and
// mark the open room's reads stale — broadcast frames sent while the
// socket was down are gone, so its data may be behind. What a reconnect
// must NOT do is refetch the world: the debounced list refresh carries
// each room's live-turn row (which re-watches it), and the marked-stale
// loaders refetch on the next real need. A send that never got its
// turn.opened has unknown delivery — the text goes back to the draft and
// the user decides after checking history.
// closeCode is the code the last lost socket closed with: 1000 and 1012 are the
// server saying it is going away (which takes every in-memory queue with it),
// anything else — 1006 above all — is a drop nobody explained.
let closeCode;
let connectedOnce = false;
function reattach() {
  if (!connectedOnce) { connectedOnce = true; return; }
  // A cancel with no ack is not replayed. Rewatch the turn and let the user retry Stop.
  for (const [requestId, req] of requests) {
    if (req.kind !== 'cancel') continue;
    requests.delete(requestId);
    const live = state.rooms[req.key]?.live;
    if (live?.turnId === req.turnId) live.canceling = false;
  }
  for (const r of Object.values(state.rooms)) {
    if (r.live && !r.live.terminal) {
      command(r, 'watch', { type: 'turn.watch', conversationId: r.id, turnId: r.live.turnId, afterSeq: r.live.lastSeq });
    } else if (r.id) {
      // Missed broadcasts while the socket was down — the next real
      // load refetches instead of trusting the TTL.
      r.infoStale = true;
      r.histStale = true;
      r.ctxStale = true;
      r.catalogAt = 0;
    }
    if (r.pendingOpen) {
      requests.delete(r.pendingOpen.requestId);
      restoreDraft(r, r.pendingOpen.text, r.pendingOpen.references, r.pendingOpen.files, r.pendingOpen.recording);
      if (r.pendingOpen.kind === 'resume') {
        r.unconfirmedRetry = { requestId: r.pendingOpen.requestId, failure: r.failed };
      }
      if (r.pendingOpen.queued && r.queue.length) r.held = true;
      toast(r.pendingOpen.kind === 'resume'
        ? 'Connection dropped before the server confirmed the retry. Check the thread before retrying.'
        : 'Connection dropped before the server confirmed your message. Check the thread before resending.', 'error', 0);
      r.pendingOpen = null;
    }
    for (const s of r.steers) if (s.state === 'sending') s.state = 'unknown';
  }
  // The open room is the only one worth a reload — and only when it was
  // never loaded (a deep link straight into a thread). A loaded room
  // gets its live turn back through the watch above, or through the
  // list refresh's activeTurnId row.
  if (state.route.view === 'chat') {
    const r = room(state.route.id);
    if (!r.live && r.infoStatus !== 'ready' && r.hist.status !== 'ready') openRoom(state.route.id);
    // A drain notice sent while the socket was down is gone, and frames are
    // never replayed: the room it was about is read now instead. Its count went
    // with the frames, so the line stops claiming one — and when the server
    // said it was leaving, the queue it described cannot be there at all.
    if (queuedRooms.has(r.id)) {
      if (closeCode === 1000 || closeCode === 1012) forgetQueued(r.id);
      else resetQueued(r.id);
      readQueuedRoom(r.id, { force: true });
    }
  }
  refreshConversationsSoon(5000);
  emit();
}

// ---------- frames ----------

function roomForFrame(f) {
  const req = f.requestId && requests.get(f.requestId);
  if (req) return room(req.key);
  if (f.conversationId && state.rooms[f.conversationId]) return state.rooms[f.conversationId];
  return null;
}

function onFrame(f) {
  switch (f.type) {
    // How many messages from other threads wait in a room. Unsequenced and
    // never replayed, so the number is the current one — last frame wins, and
    // the line that shows it renders itself (lib/queued.js), which is why this
    // frame deliberately does not emit.
    case 'conversation.queued': {
      const id = f.conversationId;
      const waiting = Math.max(0, Math.trunc(Number(f.payload?.waiting) || 0));
      // Nothing to render when the room was drained before this tab heard it:
      // only a notice that crossed a non-empty queue earns the read below.
      const drained = waiting === 0 && queuedRooms.delete(id);
      if (waiting) queuedRooms.add(id);
      setQueued(id, waiting);
      if (drained) readQueuedRoom(id, { force: true });
      return;
    }
    case 'turn.opened': return onOpened(f);
    case 'error': return onError(f);
    case 'conversation.activity': return onActivity(f);
    case 'conversation.updated': {
      // A room changed outside a turn: its title (the thread list and its
      // header) or a reply's delivery to a chat. Only the room on screen
      // earns room reads; every other room just goes stale and reloads
      // when it is actually opened.
      refreshConversationsSoon();
      const r = state.rooms[f.conversationId];
      if (!r) return;
      r.infoStale = true;
      r.histStale = true;
      if (state.route.view !== 'chat' || state.route.id !== r.id) { emit(); return; }
      // A pick stored elsewhere is a room change like any other: the list that
      // shows it is stale now, so the read that follows is a real one (and one
      // per change, not one per control that shows it).
      r.agentOptions.at = 0;
      loadAgentOptions(r);
      if (r.infoStatus === 'ready') loadInfo(r);
      if (r.live || r.hist.status !== 'ready') { emit(); return; }
      readNewestPage(r).catch(() => { /* the next load shows it */ });
      return;
    }
    case 'turn.cancel_ack': requests.delete(f.requestId); return;
    case 'turn.resync_required': return onResync(f);
  }
  const r = roomForFrame(f);
  if (!r) return;
  const inputId = f.payload?.inputId;
  if (f.type === 'turn.steer_ack') {
    const s = r.steers.find((x) => x.inputId === inputId);
    if (s) s.state = 'pending';
  } else if (f.type === 'turn.steer_recalled') {
    const s = r.steers.find((x) => x.inputId === inputId);
    if (s) {
      r.steers = r.steers.filter((x) => x !== s);
      restoreDraft(r, s.text, s.references, s.files);
    }
  }
  // An agent's own account of its context window is the footer's figure and
  // window; each report replaces the last.
  // Something the agent or the runtime wants the owner to know.
  if (f.type === 'turn.notice') {
    const text = f.payload.title ? `${f.payload.title}: ${f.payload.text}` : f.payload.text;
    toast(text, f.payload.severity === 'warning' ? 'warning' : f.payload.severity === 'error' ? 'error' : 'info', 7000);
  }
  if (f.type === 'turn.context') {
    r.ctx = { estimate: 0, ...r.ctx, total: f.payload.size, live: f.payload.used, cost: f.payload.cost || null };
  }
  if (!r.live || r.live.turnId !== f.turnId) { emit(); return; }
  // A turn this tab did not open holds its prompt only in history: the
  // opening frame precedes the server's write of that user message, and
  // every frame after it proves the write landed. Read once for it.
  if (f.type !== 'turn.opened' && !r.live.promptRead) {
    r.live.promptRead = true;
    readLivePrompt(r);
  }
  const compacting = r.live.phase?.phase === 'compacting';
  if (!applyFrame(r.live, f)) return;
  // A fold just ended: the history is smaller than any size reported before
  // it, so the meter asks the server rather than keep showing the old one.
  if (compacting && r.live?.phase?.phase !== 'compacting') fetchContext(r, { force: true });
  if (f.type === 'turn.tool_result') {
    // A thread that stopped another one is told how many messages stayed
    // queued. Same number, same place: the room's line, never a second one.
    const call = r.live?.segments.findLast((x) => x.kind === 'tool' && x.callId === f.payload?.callId);
    if (call?.name === 'conversation_stop') {
      const read = (text) => { try { return JSON.parse(text || ''); } catch { return null; } };
      const out = read(call.output);
      const asked = read(call.input) || {};
      if (typeof out?.waiting === 'number') setQueued(out.threadId || asked.thread || asked.threadId || asked.conversationId, out.waiting);
    }
  }
  if (f.type === 'turn.steer' && inputId) {
    // The frame carries the input's text and uploads, not the annotation
    // picks that went with it. The tab that submitted this steer still holds
    // them, so the bubble it draws shows the marks it made instead of only
    // the screenshot.
    const mine = r.steers.find((x) => x.inputId === inputId);
    const picks = (mine?.files || []).filter((x) => x.pickId);
    const seg = r.live.segments.find((x) => x.kind === 'steer' && x.inputId === inputId);
    if (seg && picks.length && !seg.annotations) seg.annotations = picks;
    if (f.payload?.final) r.steers = r.steers.filter((x) => x.inputId !== inputId);
  }
  // A provider call reports the size it was sent; an agent's figure comes from its own reports.
  if (f.type === 'turn.step_finished' && f.payload?.usage && r.ctx && !selection(r).agentId) r.ctx.live = f.payload.usage.totalInputTokens;
  if (f.type === 'turn.completed' || f.type === 'turn.failed') onTerminal(r, f);
  emit();
}

function onOpened(f) {
  const req = requests.get(f.requestId);
  if (!req) {
    // A watcher replays the opening frame of a turn another command or
    // channel started — adopt the server's clock for its timer.
    const r = state.rooms[f.conversationId];
    if (r?.live && !r.live.terminal && r.live.turnId === f.turnId) { applyFrame(r.live, f); emit(); }
    return;
  }
  let r = room(req.key);
  if (!r.id) {
    // A fresh view just got its server-minted id: move it under that key.
    // minted keeps what a failed first send must hand back to the home view.
    const moved = { ...r, key: f.conversationId, id: f.conversationId, info: null, infoStatus: 'loading', minted: { projectId: state.newProjectId } };
    state.rooms[f.conversationId] = moved;
    state.rooms[FRESH] = blankRoom(FRESH, null);
    req.key = f.conversationId;
    r = moved;
    state.newProjectId = '';
    if (state.route.view === 'home') navigate('#/c/' + encodeURIComponent(f.conversationId));
    loadInfo(r, { created: true });
  }
  r.live = newTurn({ turnId: f.turnId, requestId: f.requestId, conversationId: f.conversationId, kind: req.kind === 'send' ? 'send' : 'edit', startedAt: f.payload?.startedAt });
  r.live.lastSeq = 0;
  r.live.userText = req.kind === 'send' || req.kind === 'edit' ? req.text : null;
  r.live.userRefs = req.kind === 'send' ? r.pendingOpen?.references || null : null;
  r.live.userFiles = req.kind === 'send' ? r.pendingOpen?.files || null : null;
  r.live.userRecording = req.kind === 'send' ? r.pendingOpen?.recording || null : null;
  r.live.editOf = req.kind === 'edit' ? r.pendingOpen?.messageId : null;
  r.pendingOpen = null;
  if (req.kind === 'resume') {
    r.failed = null;
    r.held = false;
  }
  // A live turn turns every native command off for the room.
  loadCommands(r, { force: true });
  refreshConversationsSoon();
  emit();
}

function onError(f) {
  const req = f.requestId && requests.get(f.requestId);
  requests.delete(f.requestId);
  if (!req) { toast(f.message || 'The server rejected a command.', 'error'); return; }
  if (req.kind === 'cancel') {
    const live = state.rooms[req.key]?.live;
    if (live?.turnId === req.turnId) live.canceling = false;
    toast(f.message || 'The server rejected the command.', 'error');
    return;
  }
  const r = room(req.key);
  if (req.kind === 'send' || req.kind === 'edit' || req.kind === 'resume') {
    const mine = r.pendingOpen?.requestId === f.requestId ? r.pendingOpen : null;
    if (mine) r.pendingOpen = null;
    if (mine?.queued) {
      r.queue.unshift(mine.queued);
      r.held = true;
    } else if (req.kind === 'send') restoreDraft(r, req.text, mine?.references, mine?.files, mine?.recording);
  } else if (req.kind === 'steer') {
    const s = r.steers.find((x) => x.inputId === f.requestId);
    if (s) { r.steers = r.steers.filter((x) => x !== s); restoreDraft(r, s.text, s.references, s.files); }
  } else if (req.kind === 'watch') {
    if (r.live && !r.live.terminal) { r.live = null; loadHistory(r); }
  }
  toast(f.message || 'The server rejected the command.', 'error');
  emit();
}

function onResync(f) {
  const req = requests.get(f.requestId);
  requests.delete(f.requestId);
  const r = req ? room(req.key) : state.rooms[f.conversationId];
  if (!r) return;
  const p = f.payload || {};
  if (r.live?.turnId !== f.turnId || r.live.terminal) return;
  if (p.turn === 'active') {
    // A journal gap lands here: the server's own snapshot rebuilds what
    // streamed, the clock rides with it, and the live tail keeps
    // appending after it. When the bound trimmed earlier output — or no
    // snapshot came at all — say so instead of faking completeness.
    if (p.startedAt) r.live.startedAt = p.startedAt;
    if (p.phase) r.live.phase = { phase: p.phase.phase, retryAfterMs: p.phase.retryAfterMs, at: Date.now() };
    if (p.parts) {
      snapshotSegments(r.live, p.parts);
      if (p.dropped > 0) {
        r.live.segments.unshift({
          id: `${r.live.turnId}:resync`, kind: 'resync',
          text: 'Some earlier live output is unavailable — the full record appears when the turn ends.',
        });
      }
    } else if (!r.live.segments.length) {
      r.live.segments.push({
        id: `${r.live.turnId}:resync`, kind: 'resync',
        text: 'Live output so far is unavailable — it appears when the turn ends.',
      });
    }
    for (const s of r.steers.filter((x) => x.turnId === f.turnId)) {
      const receipt = p.steering?.find((x) => x.inputId === s.inputId);
      if (receipt?.state === 'pending' || receipt?.state === 'claimed') s.state = 'pending';
      else if (receipt?.state === 'recalled' || receipt?.state === 'persisted') {
        r.steers = r.steers.filter((x) => x !== s);
        if (receipt.state === 'recalled') restoreDraft(r, s.text, s.references, s.files);
      } else s.state = 'unknown';
    }
  } else if (p.terminal && (p.turn === 'completed' || p.turn === 'failed')) {
    const terminal = { type: 'turn.' + p.turn, requestId: r.live.requestId, turnId: f.turnId, payload: p.terminal };
    applyFrame(r.live, terminal);
    onTerminal(r, terminal);
  } else {
    r.live = null;
    for (const s of r.steers) if (s.turnId === f.turnId) s.state = 'unknown';
    loadInfo(r);
    loadHistory(r);
  }
  emit();
}

function onActivity(f) {
  refreshConversationsSoon();
  const r = state.rooms[f.conversationId];
  if (!r) return;
  const p = f.payload || {};
  // Whatever the state change, the room's stored answers may have moved
  // — a turn opening or ending writes the record and usually history.
  r.infoStale = true;
  if (p.state !== 'opened') r.histStale = true;
  // A turn starting or ending changes what the room's commands may do; the
  // next open (or the visible-room read below) asks again.
  r.catalogAt = 0;
  const ours = p.requestId && requests.has(p.requestId);
  if (ours || r.live?.turnId === f.turnId) return;
  if (p.state === 'opened' && !r.live && !r.pendingOpen && state.route.view === 'chat' && state.route.id === r.id) {
    watch(r, f.turnId, p.kind, -1);
    // Show the prompt the turn answers as soon as it is readable; the
    // notice can beat the server's write, and the turn's first streamed
    // frame reads again when it does.
    readLivePrompt(r);
  }
  // Only the room on screen refetches; every other room reloads when it
  // is opened. A terminal state means the turn wrote its messages —
  // stale alone does not show them here, so the visible room reloads.
  if (p.state !== 'opened' && !r.live && state.route.view === 'chat' && state.route.id === r.id) {
    loadInfo(r);
    // A room whose queue was announced waiting lands here too, and this is the
    // read that covers it when the drain notice never comes (a job that dies
    // with its worker, a server that stops): the frame marks history stale, so
    // the read happens even when the last one was moments ago.
    loadHistory(r);
    // The room is idle again, so its commands are available again.
    loadCommands(r, { force: true });
  }
}

async function onTerminal(r, f) {
  requests.delete(f.requestId);
  // A live session's options differ from the agent's probe, so the list the
  // controls show is read again once the turn that used it ends.
  loadAgentOptions(r, { force: true });
  const p = f.payload || {};
  const live = r.live;
  // A replayed terminal can confirm a retry whose opening reply was lost.
  const unconfirmed = r.unconfirmedRetry;
  if (unconfirmed && unconfirmed.requestId === f.requestId) {
    if (r.failed === unconfirmed.failure) r.failed = null;
    r.unconfirmedRetry = null;
  }
  // Prepend in reverse submission order so recovered inputs keep their order.
  // An explicit Steer never becomes an ordinary queued send.
  let steerReturned = false;
  for (const s of [...(p.steering || [])].reverse()) {
    const mine = r.steers.find((x) => x.inputId === s.inputId);
    if (!mine) continue;
    const unconsumed = s.state === 'pending' || s.state === 'claimed';
    if (unconsumed || s.state === 'recalled') restoreDraft(r, mine.text, mine.references, mine.files);
    if (unconsumed) steerReturned = true;
    r.steers = r.steers.filter((x) => x !== mine);
  }
  if (steerReturned) toast('The turn ended before it could use your steer. Your input is back in the draft.', 'info');
  // A command can reach the server after this terminal snapshot. Keep its
  // input until a late rejection can restore it; do not resend it blindly.
  for (const s of r.steers) if (s.turnId === live?.turnId) s.state = 'unknown';

  if (f.type === 'turn.failed' && r.minted && live?.kind === 'send' && p.userMessagePersisted === false) {
    // The server drops a thread whose first message it never saved, so
    // there is nothing to open: the message and the choices go back to the
    // home composer, where the send began.
    const home = freshRoom();
    home.sel = { ...r.sel, ...home.sel };
    prependDraft(home, r.draft, r.refs, r.files);
    home.quotes = [...r.quotes, ...home.quotes];
    restoreDraft(home, live.userText, live.userRefs, live.userFiles, live.userRecording);
    home.queue = [...r.queue, ...home.queue];
    if (home.queue.length) home.held = true;
    home.steers = [...r.steers, ...home.steers];
    for (const s of r.steers) {
      const req = requests.get(s.inputId);
      if (req) req.key = home.key;
    }
    state.newProjectId = r.minted.projectId;
    delete state.rooms[r.id];
    // The address, not the route: the route follows the address one task
    // later, and a failure can land in between.
    if (location.hash === '#/c/' + encodeURIComponent(r.id)) navigate('#/');
    if (p.status !== 408) toast(p.message || 'The turn failed.', 'error');
    refreshConversationsSoon();
    emit();
    return;
  }
  r.minted = null;
  if (f.type === 'turn.failed') {
    // Never started: the server took no user line for this turn.
    const unstarted = p.userMessagePersisted === false;
    // Leaving: the server is going away, so it cannot say the prompt failed —
    // whatever ran is in history, and a card here would offer work already done.
    const leaving = p.status === 503 && p.code === 'unavailable';
    if (live?.kind === 'send' && (live.userText || live.userFiles?.length || live.userRecording) && unstarted) {
      restoreDraft(r, live.userText, live.userRefs, live.userFiles, live.userRecording);
    }
    if (p.status === 408) {
      if (!live?.canceling) toast('Turn was interrupted.', 'info');
    } else if (unstarted || leaving) {
      // Say it once, keep the draft, and leave no failure card (and no Retry)
      // for a turn the server never ran to completion.
      toast(p.message || (leaving ? 'The server restarted before the turn finished.' : 'The turn never started.'), leaving ? 'warning' : 'error');
      r.failed = null;
    } else {
      r.failed = { status: p.status, code: p.code, message: p.message || '' };
    }
  }
  r.settling = true;
  // The turn wrote history — or, on a failed send that never persisted,
  // wrote nothing. The loaders dedupe and TTL-gate, so the activity
  // notice that follows folds into this same refresh instead of firing
  // again.
  r.infoStale = true;
  r.histStale = true;
  r.ctxStale = true;
  const wroteNothing = f.type === 'turn.failed' && p.userMessagePersisted === false;
  emit();
  await (wroteNothing ? loadInfo(r) : Promise.all([loadInfo(r), loadHistory(r)]));
  r.live = null;
  r.settling = false;
  if (!wroteNothing) fetchContext(r, { keepLive: true, force: true });
  // The room is idle again: its native commands come back on.
  loadCommands(r, { force: true });
  refreshConversationsSoon();
  emit();
  // Drain only after a clean finish with readable history; otherwise keep Send next available.
  r.held = r.queue.length > 0 && (f.type === 'turn.failed' || r.hist.status !== 'ready');
  if (!r.held && r.queue.length) {
    resumeQueue(r);
  } else if (r.bindOnIdle) {
    // A model picked while the turn ran binds the room now that its worker
    // is free, and nothing is about to occupy it.
    r.bindOnIdle = false;
    persistSelection(r, selection(r));
  }
}

export function resumeQueue(r) {
  if (roomBusy(r) || !r.queue.length) return;
  const next = r.queue.shift();
  if (sendNow(r, next)) r.held = false;
  else {
    r.queue.unshift(next);
    r.held = true;
  }
}

// ---------- approvals ----------

// answerApproval sends the owner's decision on a permission request. The
// card settles when the turn reports the request resolved.
export async function answerApproval(r, seg, optionId) {
  if (seg.status !== 'pending' || seg.answering) return;
  seg.answering = optionId;
  emit();
  try {
    await api.answerApproval(r.id, seg.request.id, optionId);
  } catch (err) {
    seg.answering = false;
    if (err.status === 404) seg.status = 'withdrawn';
    toast('Could not send your answer: ' + errorText(err), 'error');
  }
  emit();
}

// answerElicitation sends the owner's reply to an agent's request for
// input: accept (with the form's content), decline, or cancel. A 400 is a
// field the form got wrong; the card stays open to fix it.
export async function answerElicitation(r, seg, action, content) {
  if (seg.status !== 'pending' || seg.answering) return false;
  seg.answering = action;
  seg.error = '';
  emit();
  try {
    await api.answerElicitation(r.id, seg.request.id, action === 'accept' && content ? { action, content } : { action });
    return true;
  } catch (err) {
    seg.answering = false;
    if (err.status === 404) seg.status = 'withdrawn';
    else if (err.status === 400) seg.error = errorText(err);
    else toast('Could not send your answer: ' + errorText(err), 'error');
    return false;
  } finally {
    emit();
  }
}

// ---------- room management ----------

export async function rename(r, title) {
  try {
    applyRoomInfo(await api.annotate(r.id, { title }));
    refreshConversationsSoon();
  } catch (err) { toast('Rename failed: ' + errorText(err), 'error'); }
  emit();
}

// annotateRoom pins a thread or moves it between projects from anywhere
// (the sidebar works on threads that may not be open). It answers whether
// the change landed; a failure was already told in a toast.
export async function annotateRoom(id, patch) {
  let ok = false;
  try {
    applyRoomInfo(await api.annotate(id, patch));
    ok = true;
  } catch (err) { toast('Could not update the thread: ' + errorText(err), 'error'); }
  emit();
  return ok;
}

export async function removeRoomById(id) {
  const r = room(id);
  await removeRoom(r);
}

// removeRoom deletes a thread. The server stops the room's running turn first,
// and an agent can take seconds to answer that, so the thread shows as being
// deleted at once (a second delete is not started), and a toast says so when
// it is taking a while. A thread the server no longer has is gone, which is
// what was asked for, so it is dropped like any other.
export async function removeRoom(r) {
  if (state.deleting.includes(r.id)) return;
  state.deleting = [...state.deleting, r.id];
  emit();
  let notice = null;
  const slow = setTimeout(() => { notice = toast('Deleting thread…', 'info', 0); }, 400);
  let failure = null;
  try {
    await api.remove(r.id);
  } catch (err) {
    if (!(err instanceof ApiError && err.status === 404)) failure = err;
  }
  clearTimeout(slow);
  if (notice !== null) dismissToast(notice);
  state.deleting = state.deleting.filter((id) => id !== r.id);
  if (failure) {
    toast('Delete failed: ' + errorText(failure), 'error');
    emit();
    return;
  }
  forgetThread(r.id);
  toast('Thread deleted.');
  emit();
}

// deleteAllRooms purges every thread and its artifacts server-side, then
// drops everything the client held for them — open rooms, drafts, the
// list — and navigates home. The server confirmed nothing survives; the
// client must not pretend otherwise either.
export async function deleteAllRooms() {
  const res = await api.deleteAllConversations();
  state.rooms = {};
  state.convs = { items: [], total: 0, hasMore: false, status: 'loading', error: null, more: false };
  state.projectThreads = {};
  navigate('#/');
  loadProjects();
  loadConversations();
  emit();
  return res;
}

// forgetThread drops a deleted thread from everything that lists or holds it —
// the main list, every project's own list, the open room — and asks the server
// for the counts, which those lists do not carry.
function forgetThread(id) {
  writes++;
  delete state.rooms[id];
  if (state.convs.items.some((c) => c.id === id)) state.convs.total = Math.max(0, state.convs.total - 1);
  state.convs.items = state.convs.items.filter((c) => c.id !== id);
  state.projectThreads = Object.fromEntries(Object.entries(state.projectThreads).map(([pid, list]) => [pid, list.filter((c) => c.id !== id)]));
  if (state.route.id === id) navigate('#/');
  loadProjects();
  loadConversations();
}

// ---------- native commands ----------

// runCommand runs one native command on the room. A command is an action, not
// a turn: no message is written and no turn frame is sent. The server owns
// every precondition; the client refuses only what the room's catalog already
// says is unavailable. A refusal — local or from the server — keeps the draft,
// and only a 200 spends it. draft is the text that was submitted, so a draft
// the owner rewrote while the command ran is left alone.
export async function runCommand(r, name, args = '', text = '') {
  const line = String(text || `/${name}${args ? ' ' + args : ''}`).trim();
  // Refused before it ever started: nothing ran, so a command that came from
  // the composer goes straight back to it — the owner never loses the text.
  // (A command run from the header carries no text of its own.)
  const refuse = (why) => {
    toast(why, 'info');
    if (text) { restoreCommandText(r, line); emit(); }
    return false;
  };
  if (!r.id) {
    const row = catalogRow(r, name);
    return refuse(row?.reason || 'Send a message first.');
  }
  if (r.command) return refuse(`/${r.command.name} is still running.`);
  const row = catalogRow(r, name);
  if (row && row.enabled === false) return refuse(row.reason || `/${name} is not available in this thread.`);
  // The receipt: what was sent, still running (or failed), and whether a retry
  // makes sense. A command is an action with no turn behind it, so this is the
  // only thing that says it is working.
  r.command = { name, text: line, args, phase: 'running', error: '', retry: true, startedAt: Date.now() };
  emit();
  let ok = false;
  let navigated = false;
  try {
    let res;
    try {
      res = await api.runCommand(r.id, { name, args, confirm: false });
    } catch (err) {
      // A destructive command asks first: the dialog is the `confirm: true` the
      // server wants, and cancelling puts the word back where it came from.
      if (!(err instanceof ApiError && err.status === 409 && err.code === 'confirmation_required')) throw err;
      if (!await confirmCommand(name)) {
        r.command = null;
        restoreCommandText(r, line);
        emit();
        return false;
      }
      res = await api.runCommand(r.id, { name, args, confirm: true });
    }
    const before = r.id;
    await applyCommand(r, res?.command || name, res?.conversation || null);
    navigated = name === 'fork' || r.id !== before;
    ok = true;
  } catch (err) {
    // A refusal that names the room's state — busy, unavailable — also means
    // the catalog is out of date; read it again for the next attempt.
    if (err instanceof ApiError && err.status === 409) loadCommands(r, { force: true });
    settleCommandFailure(r, line, err);
  } finally {
    if (r.command?.phase !== 'failed') r.command = null;
    emit();
  }
  loadCommands(r, { force: true });
  // The queue waited for the command, not for a turn: it moves now.
  if (!navigated) resumeQueue(r);
  return ok;
}

// restoreCommandText puts a command the room did not take back at the head of
// the draft, cursor at the end, where it can be sent again or edited — never
// pushed into text the owner was already writing.
function restoreCommandText(r, line) {
  if (!line) return;
  r.draft = r.draft ? line + '\n' + r.draft : line;
  r.caret = line.length;
}

// settleCommandFailure decides where a refused command's text goes. A refusal
// that names its reason (400/409 with a code) is final: if the box is free the
// text goes back to it, otherwise the receipt stays and offers Retry. Anything
// else — a dropped socket, a 5xx without a code — leaves the outcome unknown,
// so the text goes back when the box is free and the receipt says so (no
// Retry: resending an unknown action could run it twice).
function settleCommandFailure(r, line, err) {
  // The server answered and named a reason (any 4xx): the command did not run,
  // and saying so is final. Anything else — a dropped socket, a 5xx — leaves
  // the outcome unknown.
  const named = err instanceof ApiError && err.status >= 400 && err.status < 500;
  const message = err instanceof ApiError ? err.message : errorText(err);
  const boxFree = !String(r.draft || '').trim();
  if (named) {
    if (boxFree) {
      r.command = null;
      restoreCommandText(r, line);
      toast(message, 'error');
      return;
    }
    r.command = { ...r.command, phase: 'failed', error: message, retry: true };
    return;
  }
  if (boxFree) {
    r.command = null;
    restoreCommandText(r, line);
    toast(`Could not confirm ${line} — check the thread.`, 'error');
    return;
  }
  r.command = { ...r.command, phase: 'failed', error: message, retry: false };
}

// retryCommand runs a failed receipt's command again, once.
export function retryCommand(r) {
  const c = r.command;
  if (!c || c.phase !== 'failed' || !c.retry) return;
  r.command = null;
  runCommand(r, c.name, c.args, c.text);
}

// dismissCommand clears a failed receipt without resending it.
export function dismissCommand(r) {
  if (r.command?.phase !== 'failed') return;
  r.command = null;
  emit();
}

// confirmCommand asks before a destructive command runs. The answer is the
// `confirm: true` the server requires; a chat ingress sends the word itself.
function confirmCommand(name) {
  if (name === 'clear') {
    return askConfirm({
      title: 'Clear this thread?',
      body: 'Every message, fold, and tool output in this thread is removed. The thread keeps its model, workspace, project, and label; its agent session starts fresh.',
      confirm: 'Clear',
      danger: true,
    });
  }
  return askConfirm({ title: `Run /${name}?`, body: 'This cannot be undone.', confirm: 'Run', danger: true });
}

// applyCommand settles what a command did. Compaction and clearing leave the
// room in place and reload what they rewrote; a fork answers with the new room,
// which is listed and opened.
async function applyCommand(r, name, info) {
  if (name === 'fork') {
    if (!info?.id) throw new ApiError(0, 'invalid_response', 'The server did not return the forked thread.');
    // The fork's projection is already in hand, so its header paints before
    // its reads land. The lists (threads, projects) learn about it too.
    const next = room(info.id);
    next.info = info;
    next.infoStatus = 'ready';
    next.infoAt = Date.now();
    next.infoStale = false;
    refreshConversationsSoon();
    navigate('#/c/' + encodeURIComponent(info.id));
    openRoom(info.id);
    toast('Thread forked.');
    return;
  }
  if (info) {
    r.info = info;
    r.infoStale = false;
    r.infoAt = Date.now();
  }
  if (name === 'clear') {
    // The room's identity stays; its history, folds, and tool output are gone.
    r.messages = [];
    r.failed = null;
    r.histStale = true;
    r.ctxStale = true;
    await loadHistory(r);
    fetchContext(r, { force: true });
    toast('Thread cleared.');
    return;
  }
  // compact: the checkpoint the room's worker wrote.
  r.histStale = true;
  await loadHistory(r);
  fetchContext(r, { force: true });
  toast('History compacted.');
}

// ---------- quoting a selection ----------

// QUOTE_LIMIT caps how much of a selection one quote carries.
export const QUOTE_LIMIT = 4000;

// quoteBlock writes selected transcript text as a Markdown blockquote: "> "
// before each line, a bare ">" for a blank one, cut at QUOTE_LIMIT.
export function quoteBlock(text) {
  let t = String(text || '').replace(/\r\n?/g, '\n').replace(/\u00a0/g, ' ').trim();
  if (!t) return '';
  if (t.length > QUOTE_LIMIT) t = t.slice(0, QUOTE_LIMIT).trimEnd() + '…';
  return t.split('\n').map((l) => (l.trim() ? '> ' + l.trimEnd() : '>')).join('\n');
}

// addQuote puts a passage on r's draft as a quote with the owner's reply
// to it (which may be empty), and hands the composer the focus. from is
// the thread the passage came from when that is not r.
export function addQuote(r, text, reply = '', from = null) {
  if (!quoteBlock(text)) return false;
  r.quotes = [...r.quotes, { key: newRequestId(), text: text.trim(), reply: reply.trim(), from }];
  r.caret = r.draft.length;
  emit();
  return true;
}

export function removeQuote(r, key) {
  r.quotes = r.quotes.filter((q) => q.key !== key);
  emit();
}

// quotedDraft is the message a draft sends with its quotes: each quote as
// a Markdown blockquote — after a link to its thread when it came from
// another — with its reply below it, all ahead of the draft's text. The
// draft's selections move with the text they point into.
export function quotedDraft(quotes, text, refs = []) {
  const head = quotes.map((q) => [
    q.from && `From [${q.from.title.replace(/[\\[\]]/g, '\\$&')}](/#/c/${encodeURIComponent(q.from.id)}):`,
    quoteBlock(q.text),
    q.reply,
  ].filter(Boolean).join('\n\n')).join('\n\n');
  if (!head) return { text, refs };
  const lead = text.trim() ? head + '\n\n' : head;
  return { text: lead + text, refs: refs.map((x) => ({ ...x, start: x.start + lead.length, end: x.end + lead.length })) };
}

// startSideChat opens the new-thread composer for a conversation about
// text without adding to origin: the same runtime (agent, or provider,
// model, effort and upstream pin), the same project or folder, and text as
// a quote — with the owner's reply, if any — that links back to origin.
// Nothing runs until the owner sends; a draft the new-thread composer held
// is replaced, with an undo.
export function startSideChat(origin, text, reply = '') {
  if (!quoteBlock(text) || !origin.id) return false;
  const home = freshRoom();
  if (isBusy(home)) {
    toast('A new thread is still starting — try again in a moment.', 'info');
    return false;
  }
  const s = selection(origin);
  const projectId = origin.info?.projectId || '';
  const title = origin.info?.title || 'Untitled thread';
  const prior = { draft: home.draft, refs: home.refs, files: home.files, quotes: home.quotes, sel: home.sel, sideOf: home.sideOf, projectId: state.newProjectId };
  home.sel = s.agentId
    ? { agentId: s.agentId, modelId: s.modelId || undefined }
    : { providerId: s.providerId, modelId: s.modelId, effort: s.effort, upstream: s.upstream };
  // A project thread works in the project's folders; a plain one in its own.
  if (!projectId && s.workspace) home.sel.workspace = s.workspace;
  state.newProjectId = projectId;
  home.draft = '';
  home.refs = [];
  home.files = [];
  home.quotes = [{ key: newRequestId(), text: text.trim(), reply: reply.trim(), from: { id: origin.id, title } }];
  home.sideOf = { id: origin.id, title };
  home.caret = 0;
  navigate('#/');
  if (prior.draft.trim() || prior.files.length || prior.quotes.length) {
    toast('Replaced the unsent new-thread draft.', 'info', 8000, { label: 'Undo', run: () => {
      if (isBusy(home)) return;
      Object.assign(home, { draft: prior.draft, refs: prior.refs, files: prior.files, quotes: prior.quotes, sel: prior.sel, sideOf: prior.sideOf, caret: prior.draft.length });
      state.newProjectId = prior.projectId;
      emit();
    } });
  }
  return true;
}
