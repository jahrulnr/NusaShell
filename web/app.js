// NusaShell WebUI entry: theme, layout shell, and boot.
import { html, render, useEffect, useRef } from '/vendor/preact/htm-preact.mjs';
import { useStore, state, emit } from './lib/store.js';
import { start } from './lib/chat.js';
import { api, setCsrf, onUnauthenticated, errorText } from './lib/api.js';
import { LoginView } from './components/login.js';
import { Spinner, ErrorBlock } from './components/ui.js';
import { Sidebar } from './components/sidebar.js';
import { ChatView, HomeView, MissingView } from './components/chat.js';
import { SettingsView, SettingsNav } from './components/settings.js';
import { ConfirmHost, Toasts } from './components/ui.js';
import { installTooltips } from './lib/tooltip.js';
import { FilePreview } from './components/preview.js';
import { ProjectEditor } from './components/projects.js';
import { PaneSplitter } from './components/splitter.js';

const dark = window.matchMedia('(prefers-color-scheme: dark)');
function applyAppearance() {
  const root = document.documentElement;
  const { theme, font } = state.prefs;
  root.dataset.theme = theme === 'system' ? (dark.matches ? 'dark' : 'light') : theme;
  if (font) root.dataset.font = font; else delete root.dataset.font;
}
dark.addEventListener('change', applyAppearance);

// The drawer is shown only below the md breakpoint. Widening the window while it is
// open would leave it "open" with nothing showing it, and its held toasts with it.
const desktop = window.matchMedia('(min-width: 768px)');
desktop.addEventListener('change', (e) => { if (e.matches) state.sidebarOpen = false; });

function App() {
  const s = useStore();
  const sidebar = useRef();
  useEffect(applyAppearance, [s.prefs.theme, s.prefs.font]);
  if (s.auth.phase === 'checking') {
    return html`<div class="h-full flex items-center justify-center text-muted gap-2"><${Spinner} /> Connecting…</div>`;
  }
  if (s.auth.phase === 'error') {
    return html`<div class="h-full flex items-center justify-center px-4"><${ErrorBlock} class="max-w-md w-full" title="Cannot reach the server" message=${s.auth.error} onRetry=${boot} /></div>`;
  }
  if (s.auth.phase === 'login') return html`<${LoginView} onLoggedIn=${loggedIn} />`;
  const view = s.route.view === 'chat'
    ? html`<${ChatView} key=${s.route.id} id=${s.route.id} />`
    : s.route.view === 'settings' ? html`<${SettingsView} />`
      : s.route.view === 'missing' ? html`<${MissingView} path=${s.route.path} />`
        : html`<${HomeView} />`;
  const close = () => { state.sidebarOpen = false; emit(); };
  // Settings takes over the sidebar: its pages instead of the threads.
  const nav = s.route.view === 'settings' ? html`<${SettingsNav} />` : html`<${Sidebar} />`;
  return html`<div class="flex h-full">
    <aside ref=${sidebar} id="thread-sidebar"
      style=${{ '--pane-width': s.prefs.threadSidebarWidth ? `${s.prefs.threadSidebarWidth}px` : undefined }}
      class="thread-sidebar relative hidden md:flex shrink-0 border-r border-line">
      ${nav}
      <${PaneSplitter} edge="left" label="Resize threads sidebar" panelRef=${sidebar} pref="threadSidebarWidth" />
    </aside>
    ${s.sidebarOpen && html`<div class="md:hidden fixed inset-0 z-40 flex" onKeyDown=${(e) => e.key === 'Escape' && close()}>
      <div class="w-[min(300px,86vw)] h-full border-r border-line shadow-pop">${nav}</div>
      <button class="flex-1 bg-black/50" aria-label="Close sidebar" onClick=${close}></button>
    </div>`}
    <main class="flex-1 min-w-0 h-full">${view}</main>
    <${FilePreview} />
    <${ProjectEditor} />
    <${Toasts} />
    <${ConfirmHost} />
  </div>`;
}

// boot asks whether a login is needed before anything else talks to the
// server; the socket and data loads start only with a usable session.
let started = false;
async function boot() {
  state.auth = { ...state.auth, phase: 'checking', error: null };
  emit();
  try {
    const st = await api.authStatus();
    state.auth.required = st.required;
    state.auth.hasPassword = !!st.hasPassword;
    if (st.required && !st.authenticated) { state.auth.phase = 'login'; emit(); return; }
    setCsrf(st.csrfToken);
    state.auth.phase = 'ready';
    if (!started) { started = true; start(); }
  } catch (err) {
    state.auth.phase = 'error';
    state.auth.error = errorText(err);
  }
  emit();
}

function loggedIn() {
  // A session that lapsed mid-use reloads into a clean state; a fresh one
  // boots, which also reads the product mode.
  if (started) { location.reload(); return; }
  boot();
}

onUnauthenticated(() => { if (state.auth.phase !== 'login') { state.auth.phase = 'login'; emit(); } });

applyAppearance();
installTooltips();
boot();
const root = document.getElementById('app');
root.textContent = '';
render(html`<${App} />`, root);
