// NusaShell — application shell. Router + transport wiring.

import { rpc, on, connectWS } from './rpc.js';
import { initHome, refresh as refreshHome } from './views/home.js';
import { initAgent, refresh as refreshAgent } from './views/agent.js';
import { initSkills, refresh as refreshSkills } from './views/skills.js';
import { initPlugins, refresh as refreshPlugins, closeOverlays as closePluginOverlays } from './views/plugins.js';
import { closeAcpOverlays } from './views/agent/subagents.js';
import { initProviders as initLlmProviders, refresh as refreshLlmProviders } from './views/providers.js';
import { initAcpProviders, refreshAcpProviders } from './views/providers-acp.js';
import { initLogs, refresh as refreshLogs } from './views/logs.js';
import { initSettings, refresh as refreshSettings } from './views/settings.js';
import { initLearning, refresh as refreshLearning } from './views/learning.js';
import { initCI, refresh as refreshCI } from './views/ci.js';
import { initTelemetry, refresh as refreshTelemetry } from './views/telemetry.js';
import { toast, dismissOpenDialogs } from './ui.js';
import { bindShellShortcuts } from './shell-shortcuts.js';
import { initMobileNav } from './mobile-nav.js';
import { initOfflineScreen } from './offline-screen.js';

async function initProviders() {
  await Promise.all([initLlmProviders(), initAcpProviders()]);
}
async function refreshProviders() {
  await Promise.all([refreshLlmProviders(), refreshAcpProviders()]);
}

const viewRefresh = {
  home: refreshHome,
  agent: refreshAgent,
  skills: refreshSkills,
  plugins: refreshPlugins,
  providers: refreshProviders,
  logs: refreshLogs,
  settings: refreshSettings,
  learning: refreshLearning,
  automation: refreshCI,
  telemetry: refreshTelemetry,
};

function setConnection(status) {
  // Don't downgrade from 'open' to 'closed' — 'closed' is a transient WS
  // state that immediately triggers reconnect. Showing "Offline" for a
  // millisecond before "Reconnecting…" is a false positive.
  if (status === 'closed' && document.documentElement.dataset.backendStatus === 'open') {
    return;
  }
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
  } else if (status === 'closed') {
    // 'closed' is transient — scheduleReconnect will fire immediately.
    // Show "Reconnecting…" instead of "Offline" to avoid false positive.
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

let currentView = null;

// closeFloatingOverlays dismisses body-level overlays (plugin detail drawer,
// plugin install dialog, and global modal dialogs) so an overlay left open on
// one view never bleeds over another after navigation. (Plugin UIs open in a
// separate browser window via window.open, so there is no in-shell plugin
// window to close here.)
function closeFloatingOverlays() {
  try { closePluginOverlays(); } catch { /* view not ready */ }
  try { closeAcpOverlays({ keepPermission: true }); } catch { /* view not ready */ }
  dismissOpenDialogs();
}

function route() {
  const requested = location.hash.slice(1) || 'home';
  const aliases = { mcp: 'plugins' };
  const routed = aliases[requested] || requested;
  const known = [...document.querySelectorAll('.view')].some((view) => view.dataset.view === routed);
  const target = known ? routed : 'home';
  // Close any open floating overlay when the view actually changes.
  if (currentView !== null && currentView !== target) closeFloatingOverlays();
  currentView = target;
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
  const refresher = viewRefresh[target];
  if (refresher) refresher().catch((err) => console.warn(`refresh ${target}:`, err?.message || err));
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
  initMobileNav();

  // Full-window offline overlay + mini window button. Must be wired before
  // the first connection status events fire so a dead backend is covered
  // from the very start.
  initOfflineScreen();
  document.getElementById('mini-window-btn')?.addEventListener('click', async () => {
    try {
      const pip = await import('./pip.js');
      await pip.openMiniWindow();
    } catch (err) {
      // Document PiP can reject (user gesture required, second PiP window,
      // unsupported engine edge cases). The popup fallback already ran when
      // pipSupported() was false, so anything here is a genuine failure.
      console.error('mini window failed:', err);
      toast('Mini window not available in this browser.', 'error');
    }
  });

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
  on('learning.review.started', () => {
    toast('Autolearn spawned in background.', 'info', 3500);
  });
  on('learning.review.done', () => {
    toast('Autolearn finished.', 'success', 2500);
  });
  on('learning.review.error', (payload) => {
    const msg = payload?.error || 'Unknown error';
    toast(`Autolearn failed: ${msg}`, 'error', 6000);
  });

  try {
    const info = await rpc('app.info', {}, { timeoutMs: 4000 });
    document.title = `NusaShell ${info.version ?? ''}`.trim();
  } catch (err) {
    // Only mark offline if WS isn't already open — app.info uses HTTP,
    // which can fail independently of the WS event stream.
    if (document.documentElement.dataset.backendStatus !== 'open') {
      setConnection('offline');
    }
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
    initCI(),
    initTelemetry(),
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

  bindShellShortcuts({
    onNewConversation: () => document.getElementById('new-conversation-btn')?.click(),
  });

  route();
}

boot();
