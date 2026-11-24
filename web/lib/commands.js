// Native slash commands: the actions NusaShell owns in the composer. The
// server's catalog (GET /api/commands) decides which commands a room offers
// and whether they are available; the names here are what the client reserves
// on a draft's first line, so a recognized command never reaches a model or an
// agent — not even when the room refuses it.
import { useEffect } from '/vendor/preact/htm-preact.mjs';
import { state, emit, useStore } from './store.js';
import { api, errorText } from './api.js';

// NATIVE_COMMANDS is the reserved set: the name, and the icon its row draws.
export const NATIVE_COMMANDS = [
  { name: 'compact', icon: 'compress' },
  { name: 'clear', icon: 'eraser' },
  { name: 'fork', icon: 'branch' },
];

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

// isNativeCommand says whether a first-line name is reserved. The room's
// catalog is asked too, so a command the server adds later is recognized
// without a client change.
export function isNativeCommand(r, name) {
  if (!name) return false;
  if (NATIVE_COMMANDS.some((c) => c.name === name)) return true;
  return (r?.catalog?.items || []).some((c) => c.source === 'native' && c.name === name);
}

// catalogRow is the room's own verdict on a command, when the catalog carries
// it. Absent (never read, or a read that failed) leaves the decision to the
// server, which enforces every precondition anyway.
export function catalogRow(r, name) {
  const items = r?.catalog?.items || [];
  return items.find((c) => c.name === name && c.source === 'native') || items.find((c) => c.name === name) || null;
}

// commandChoices is the catalog as the "/" menu renders it: native rows first,
// then the agent's own, one row per name (native wins a collision), each with
// its source and this room's verdict. A query narrows it by name prefix.
export function commandChoices(r, query = '') {
  const q = String(query || '').toLowerCase();
  const byName = new Map();
  for (const c of r?.catalog?.items || []) {
    const name = String(c?.name || '');
    if (!name) continue;
    const prev = byName.get(name);
    if (!prev || (prev.source !== 'native' && c.source === 'native')) byName.set(name, c);
  }
  const rows = [...byName.values()].map((c) => {
    const native = c.source === 'native';
    return {
      kind: 'command', id: c.name, name: c.name, native,
      source: native ? 'NusaShell' : agentName(r),
      detail: [c.description, c.hint].filter(Boolean).join(' · '),
      enabled: c.enabled !== false,
      reason: c.reason || '',
      icon: native ? NATIVE_COMMANDS.find((x) => x.name === c.name)?.icon || 'slash' : 'bot',
    };
  });
  const matched = q ? rows.filter((c) => c.name.toLowerCase().startsWith(q)) : rows;
  return [...matched.filter((c) => c.native), ...matched.filter((c) => !c.native)];
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
