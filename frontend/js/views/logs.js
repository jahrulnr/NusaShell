// Logs workspace: live tail of runtime entries.

import { rpc, on } from '../rpc.js';
import { el, fmtClock } from '../ui.js';

const state = { entries: [], level: '', follow: true, max: 500, lastRenderedId: null };

export async function initLogs() {
  document.getElementById('log-follow').addEventListener('change', (e) => { state.follow = e.target.checked; });
  document.getElementById('log-level-filter').addEventListener('change', (e) => {
    state.level = e.target.value;
    renderTail();
  });
  document.getElementById('logs-clear-btn').addEventListener('click', async () => {
    try {
      await rpc('logs.clear');
      state.entries = [];
      renderTail();
    } catch (err) { /* ignore */ }
  });
  on('logs.append', (payload) => {
    const entry = payload?.entry ?? payload;
    if (!entry?.id) return;
    state.entries.push(entry);
    if (state.entries.length > state.max) state.entries.splice(0, state.entries.length - state.max);
    renderTail(true);
  });
  try {
    const res = await rpc('logs.list', { limit: 300 });
    state.entries = res.entries ?? [];
  } catch { /* backend not ready */ }
  renderTail();
}

function renderTail(appendOnly = false) {
  const tail = document.getElementById('log-tail');
  const shouldFollow = state.follow || !appendOnly;
  const wasAtBottom = tail.scrollHeight - tail.scrollTop - tail.clientHeight < 60;
  const visible = state.entries.filter((e) => !state.level || e.level === state.level);
  document.getElementById('log-count').textContent = `${visible.length} entries`;
  if (!appendOnly) {
    tail.innerHTML = '';
    state.lastRenderedId = null;
  }
  let lastId = state.lastRenderedId;
  for (const entry of visible) {
    if (appendOnly && entry.id === lastId) continue;
    tail.append(el('div', { class: 'log-line', 'data-id': entry.id },
      el('span', { class: 'log-time', text: fmtClock(entry.time) }),
      el('span', { class: `log-level ${entry.level}`, text: entry.level }),
      el('span', { class: 'log-source', text: entry.source }),
      el('span', { class: 'log-msg', text: entry.message }),
    ));
    lastId = entry.id;
  }
  state.lastRenderedId = lastId;
  if (shouldFollow && wasAtBottom) tail.scrollTop = tail.scrollHeight;
}
