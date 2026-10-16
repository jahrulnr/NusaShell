// Production shell wiring shared across the native feature pages.
// Reuse the browser helpers for pairing, PWA, pets, shortcuts, and mini windows;
// page/conversation/app.js owns routing and conversation-panel state.

import './js/pwa.js';
import { initPairingGate } from './js/pairing.js';
import { initPets } from './js/pet-launcher.js';
import { bindShellShortcuts } from './js/shell-shortcuts.js';
import { pipSupported, openMiniWindow } from './js/pip.js';
import { isElectronRuntime } from './js/desktop-file-path.js';

// ── Route state ────────────────────────────────────────────────────────────
// The default surface is Home.
const ROUTES = new Set([
  'home', 'agent', 'skills', 'learning', 'automation',
  'plugins', 'providers', 'logs', 'telemetry', 'settings',
]);

function protoRoute() {
  const route = String(location.hash || '').replace(/^#/, '').toLowerCase();
  return ROUTES.has(route) ? route : 'home';
}

// ── Pairing gate ───────────────────────────────────────────────────────────
// Blocks remote (non-loopback) clients until paired: the gate is driven by
// rpc:error events (PAIRING_REQUIRED / REMOTE_ACCESS_DISABLED) and by
// ?pairing_challenge=…&pairing_code=… URLs. Loopback never emits those, so the
// gate stays hidden for local sessions.
initPairingGate();

// ── Mobile navigation drawer ───────────────────────────────────────────────
// Owned entirely by page/conversation/app.js: #global-nav-toggle is the only
// opener and setGlobalNavOpen drives [hidden], global-nav-closed, the scrim
// class, inert/aria-hidden, and focus — the titlebar hamburger and the
// observer bridge it needed are gone, so nothing here may toggle the drawer
// a second time.

// ── Desktop pet launcher ───────────────────────────────────────────────────
// initPets() binds the hidden #pet-btn proxy, the #pet-install-* dialog, and
// the nusashell:open-pet-launcher event that #global-nav-pet dispatches below.
// The navigation affordance the anchor still navigates to the
// Settings pet card, and the dispatched event runs the production
// install/launch flow on top of it (route change dismisses the overlay via
// registerOverlayDismiss when it must close).
void initPets();
document.getElementById('global-nav-pet')?.addEventListener('click', () => {
  window.dispatchEvent(new CustomEvent('nusashell:open-pet-launcher'));
});

// ── Mini window (document PiP / popup fallback) ────────────────────────────
// pip.js moves #agent-thread + #agent-composer-stack into the PiP document —
// The conversation controller mounts aliases for the live thread and composer.
const miniWindowButton = document.getElementById('mini-window-btn');
if (isElectronRuntime()) {
  if (miniWindowButton) miniWindowButton.hidden = true;
}
miniWindowButton?.addEventListener('click', () => {
  if (!pipSupported() || document.getElementById('agent-thread')) {
    void openMiniWindow().catch(() => {});
    return;
  }
  window.open(
    `${window.location.pathname}?mini=1#agent`,
    'nusashell-mini',
    'popup=yes,width=460,height=680',
  );
});

// ── Shell shortcuts ────────────────────────────────────────────────────────
// bindShellShortcuts adds Ctrl/Cmd+N (agent view → new conversation) and
// covers Ctrl/Cmd+K + '/' for views the page keymap does not already claim.
// Its keydown listener early-returns on event.defaultPrevented, and it is
// registered after the page handlers, so their claimed shortcuts win.
bindShellShortcuts({
  getView: protoRoute,
  onNewConversation: () => document.getElementById('new-chat')?.click(),
});

// ── Agent strips (tool runs / steer queue) ─────────────────────────────────
// Static strip toggles flip aria-expanded and list visibility. Strips stay
// hidden until an active tool job or queued steer supplies their data.
for (const [toggleId, listId] of [
  ['tool-job-strip-toggle', 'tool-job-list'],
  ['agent-steer-queue-toggle', 'agent-steer-queue-list'],
]) {
  const toggle = document.getElementById(toggleId);
  const list = document.getElementById(listId);
  if (!toggle || !list) continue;
  toggle.addEventListener('click', () => {
    const open = toggle.getAttribute('aria-expanded') === 'true';
    toggle.setAttribute('aria-expanded', String(!open));
    list.hidden = open;
  });
}

// ── .view.active mirror ────────────────────────────────────────────────────
// Route state uses hidden; shared view CSS and browser helpers also consume
// .view.active. Keep that class aligned with the visible routed section.
const viewSections = document.querySelectorAll('.view[data-view]');
function syncViewActive() {
  for (const view of viewSections) view.classList.toggle('active', !view.hidden);
}
syncViewActive();
if (viewSections.length) {
  const viewObserver = new MutationObserver((records) => {
    if (records.some((record) => record.attributeName === 'hidden')) syncViewActive();
  });
  for (const view of viewSections) {
    viewObserver.observe(view, { attributes: true, attributeFilter: ['hidden'] });
  }
}

// Render icons for any chrome wired above (app.js already runs this during
// its own init; createIcons is idempotent).
window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
