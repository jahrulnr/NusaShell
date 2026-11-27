// Slash commands. The room catalog (GET /api/commands) is the only list of
// what a room offers and which source owns each name. The client reserves
// two thread commands — clear and fork — and only when the catalog has not
// listed them. Everything else, compact included, is native only when this
// room's catalog says source "native". An agent row is a turn.
import { useEffect } from '/vendor/preact/htm-preact.mjs';
import { state, emit, useStore } from './store.js';
import { api, errorText } from './api.js';

// Thread commands act on this app's own room. They are not a catalog, and
// they are not an icon table: a row's icon comes from its source.
const THREAD_COMMANDS = ['clear', 'fork'];
const SOURCE_ICON = { native: 'slash', agent: 'bot' };

// COMMAND_BUSY is what the composer and the header say while a command runs.
const COMMAND_BUSY = { compact: 'Compacting', clear: 'Clearing', fork: 'Forking' };
export const commandBusyLabel = (name) => COMMAND_BUSY[name] || `Running /${name}`;

// parseCommand reads a draft as a command: "/name" and nothing else on the
// first line, then arguments. The rest of the draft counts as tokens, so
// "/compact" with text under it is refused by the server instead of silently
// swallowing that text. A path such as "/home/x" is not a command line.
export function parseCommand(text) {
  const m = /^\/([^\s/]+)(?:\s+([\s\S]*))?$/.exec(String(text || '').trim());
  return m ? { name: m[1], args: (m[2] || '').trim() } : null;
}

// isNativeCommand says whether a first-line name is a native action. The
// catalog row decides. A thread command with no row yet stays reserved, so
// clear and fork never become a turn while the catalog is still unread.
// An agent row wins even when the name is clear or fork.
export function isNativeCommand(r, name) {
  if (!name) return false;
  const row = (r?.catalog?.items || []).find((c) => c.name === name);
  if (row) return row.source === 'native';
  return THREAD_COMMANDS.includes(name);
}

// catalogRow is the room's own verdict on a command, the first row of that
// name. The catalog is already one row per name; this does not pick a winner.
export function catalogRow(r, name) {
  return (r?.catalog?.items || []).find((c) => c.name === name) || null;
}

// commandChoices is the catalog as the "/" menu renders it, in the catalog's
// order. A query narrows it by name prefix. The icon is the row's source.
export function commandChoices(r, query = '') {
  const q = String(query || '').toLowerCase();
  const rows = [];
  for (const c of r?.catalog?.items || []) {
    const name = String(c?.name || '');
    if (!name) continue;
    if (q && !name.toLowerCase().startsWith(q)) continue;
    const native = c.source === 'native';
    rows.push({
      kind: 'command', id: name, name, native,
      source: native ? 'NusaShell' : agentName(r),
      detail: [c.description, c.hint].filter(Boolean).join(' · '),
      enabled: c.enabled !== false,
      reason: c.reason || '',
      icon: SOURCE_ICON[c.source] || 'slash',
    });
  }
  return rows;
}

// agentName is the source label of an agent command: the room's own agent.
function agentName(r) {
  const id = r?.info?.agentId || '';
  return state.agents.items.find((a) => a.id === id)?.name || 'Agent';
}

// firstEnabled and stepRow keep the menu's keyboard selection on rows that can
// run. A disabled command stays visible with its reason, but the arrows pass
// it over: Enter always has something it is allowed to pick.
export const firstEnabled = (items) => Math.max(0, items.findIndex((c) => c.enabled !== false));

export function stepRow(items, from, step) {
  const n = items.length;
  if (!n) return 0;
  for (let i = 1; i <= n; i++) {
    const at = (((from + step * i) % n) + n) % n;
    if (items[at].enabled !== false) return at;
  }
  return from;
}

// useCommands is the composer's and the header's view of the catalog: it reads
// it when the room changes and answers the rows the menu renders.
export function useCommands(r) {
  useStore();
  useEffect(() => { loadCommands(r); }, [r.key, r.id]);
  return commandChoices(r);
}

// CATALOG_TTL keeps a room change from re-reading a catalog that was just
// read; an explicit refresh (a turn's start or end) defeats it.
const CATALOG_TTL = 5_000;

// loadCommands reads the room's command catalog — the native commands plus the
// agent's own, with this room's availability. A turn starting or ending
// changes that availability, and so does a command that rewrites history, so
// those refresh it; opening a room reads it once.
export async function loadCommands(r, { force = false } = {}) {
  if (r.catalogFlight) return r.catalogFlight;
  if (!force && r.catalogAt && Date.now() - r.catalogAt < CATALOG_TTL) return;
  const flight = (async () => {
    r.catalog = { ...r.catalog, status: r.catalog.items.length ? 'refreshing' : 'loading' };
    emit();
    try {
      const res = await api.commands({ conversationId: r.id });
      r.catalog = { status: 'ready', items: Array.isArray(res?.data) ? res.data : [], error: null };
    } catch (err) {
      // A catalog the server could not answer leaves the last one in place;
      // the menu says so, and a command typed anyway is settled by the server.
      r.catalog = { status: 'error', items: r.catalog.items, error: errorText(err) };
    }
    r.catalogAt = Date.now();
    emit();
  })();
  r.catalogFlight = flight;
  try { await flight; } finally { if (r.catalogFlight === flight) r.catalogFlight = null; }
}
