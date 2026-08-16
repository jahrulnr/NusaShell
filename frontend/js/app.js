// NusaShell Light — application shell. Router + transport wiring.

import { rpc, on, connectWS } from './rpc.js';
import { initHome, refresh as refreshHome } from './views/home.js';
import { initAgent, refresh as refreshAgent } from './views/agent.js';
import { initSkills, refresh as refreshSkills } from './views/skills.js';
import { initPlugins, refresh as refreshPlugins } from './views/plugins.js';
import { initProviders, refresh as refreshProviders } from './views/providers.js';
import { initLogs, refresh as refreshLogs } from './views/logs.js';
import { initSettings, refresh as refreshSettings } from './views/settings.js';
import { initLearning, refresh as refreshLearning } from './views/learning.js';
import { wirePluginWindow } from './plugin-window.js';
import { toast } from './ui.js';

const viewRefresh = {
  home: refreshHome,
  agent: refreshAgent,
  skills: refreshSkills,
  plugins: refreshPlugins,
  providers: refreshProviders,
  logs: refreshLogs,
  settings: refreshSettings,
  learning: refreshLearning,
};

function setConnection(status) {
  document.documentElement.dataset.backendStatus = status;
  window.dispatchEvent(new CustomEvent('nusashell:connection-status', { detail: { status } }));
  const orb = document.getElementById('conn-fill');
  const label = document.getElementById('conn-status');
  const settingsOrb = document.getElementById('settings-conn-fill');
  const settingsLabel = document.getElementById('settings-conn-label');
  orb.className = 'connection-orb';
  if (status === 'open') {
    orb.classList.add('online');
    label.textContent = 'Connected';
  } else if (status === 'connecting') {
    orb.classList.add('connecting');
    label.textContent = 'Connecting…';
  } else if (status === 'reconnecting') {
    orb.classList.add('connecting');
    label.textContent = 'Reconnecting…';
  } else {
    orb.classList.add('offline');
    label.textContent = 'Offline';
  }
  if (settingsOrb && settingsLabel) {
    settingsOrb.className = 'connection-orb';
    if (status === 'open') {
      settingsOrb.classList.add('online');
      settingsLabel.textContent = 'Connected';
    } else if (status === 'connecting' || status === 'reconnecting') {
      settingsOrb.classList.add('connecting');
      settingsLabel.textContent = status === 'reconnecting' ? 'Reconnecting…' : 'Connecting…';
    } else {
      settingsOrb.classList.add('offline');
      settingsLabel.textContent = 'Disconnected';
    }
  }
}

let routeInitial = true;

function route() {
  const requested = location.hash.slice(1) || 'home';
  const aliases = { mcp: 'plugins' };
  const routed = aliases[requested] || requested;
  const known = [...document.querySelectorAll('.view')].some((view) => view.dataset.view === routed);
  const target = known ? routed : 'home';
  if (target !== requested) history.replaceState(null, '', `#${target}`);
  const items = document.querySelectorAll('[data-nav]');
  items.forEach((item) => {
    const active = item.dataset.view === target;
    item.classList.toggle('active', active);
    if (active) item.setAttribute('aria-current', 'page');
    else item.removeAttribute('aria-current');
  });
  document.querySelectorAll('.view').forEach((v) => v.classList.toggle('active', v.dataset.view === target));
  if (target === 'logs') document.getElementById('log-tail').scrollTop = document.getElementById('log-tail').scrollHeight;
  // Re-fetch the target view's data so it never goes stale after
  // changes made elsewhere (plugin install/uninstall, MCP edits, etc).
  // Skip on the initial route call — views already fetched during init.
  if (!routeInitial) {
    const refresher = viewRefresh[target];
    if (refresher) refresher().catch((err) => console.warn(`refresh ${target}:`, err?.message || err));
  }
  routeInitial = false;
}

async function boot() {
  document.querySelectorAll('[data-nav]').forEach((item) => {
    item.addEventListener('click', () => { location.hash = item.dataset.view; });
  });
  document.getElementById('nav-settings-btn').addEventListener('click', () => { location.hash = '#settings'; });
  const sidebar = document.getElementById('sidebar');
  const compact = localStorage.getItem('nusashell.sidebarMode') === 'icons';
  const sidebarToggle = document.getElementById('sidebar-mode-toggle');
  const setSidebarCompact = (value, persist = true) => {
    sidebar.classList.toggle('is-compact', value);
    sidebarToggle.setAttribute('aria-pressed', String(value));
    sidebarToggle.setAttribute('aria-label', value ? 'Expand sidebar' : 'Collapse sidebar');
    sidebarToggle.title = value ? 'Show icons and text' : 'Use icon-only sidebar';
    sidebarToggle.querySelector('.nav-label').textContent = value ? 'Show labels' : 'Collapse sidebar';
    if (persist) localStorage.setItem('nusashell.sidebarMode', value ? 'icons' : 'full');
  };
  sidebarToggle.addEventListener('click', () => setSidebarCompact(!sidebar.classList.contains('is-compact')));
  setSidebarCompact(compact, false);
  window.nusashell = { ...(window.nusashell || {}), setSidebarCompact };
  window.addEventListener('hashchange', route);

  wirePluginWindow();

  setConnection('connecting');
  // one transport per function: WS carries BE -> FE event triggers; the FE
  // talks to the BE over HTTP /rpc
  connectWS({
    onStatus: setConnection,
  });

  // surface backend events as toasts where useful
  on('logs.append', (payload) => {
    const entry = payload?.entry ?? payload;
    if (entry?.level === 'error') toast(entry.message, 'error', 5000);
  });

  try {
    const info = await rpc('app.info', {}, { timeoutMs: 4000 });
    document.getElementById('storage-path').textContent = info.data_dir || '';
    document.title = `NusaShell Light ${info.version ?? ''}`.trim();
  } catch (err) {
    setConnection('offline');
  }

  const results = await Promise.allSettled([
    initHome(),
    initAgent(),
    initSkills(),
    initPlugins(),
    initProviders(),
    initLogs(),
    initSettings(),
    initLearning(),
  ]);
  // never swallow init failures silently: a dead view is a bug, not a state
  for (const r of results) {
    if (r.status === 'rejected') {
      console.error('view init failed:', r.reason);
      const status = document.documentElement.dataset.backendStatus;
      if (status !== 'offline' && status !== 'closed' && status !== 'error') {
        toast(`View init failed: ${r.reason?.message ?? r.reason}`, 'error', 6000);
      }
    }
  }

  // cross-view: skill run opens a conversation in the Agent view
  document.addEventListener('nusashell:open-conversation', async (e) => {
    const mod = await import('./views/agent.js');
    mod.openConversationExternal?.(e.detail);
  });

  route();
}

boot();
