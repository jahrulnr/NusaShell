// NusaShell — desktop pet launcher (sidebar) + one-click install dialog.
//
// Sidebar icon (#pet-btn) lives in the .sidebar-actions row above the
// Settings icon. State machine:
//
//   not-supported → button hidden entirely (macOS / Windows)
//   not-installed → click opens the install dialog
//   installed     → click toggles settings.pets_launch (spawn if idle, stop if running)
//   installing    → click is a no-op; progress rides the Bus events
//   error         → click re-opens the install dialog so the user can retry
//
// The status snapshot comes from settings.pets_status; install progress
// arrives as `pets.install.*` Bus events (mirrors tts.install.* pattern).
// Launch failures are surfaced as a transient toast so the user can read
// them in passing.

import { rpc, on } from './rpc.js';
import { toast } from './ui.js';
import { registerOverlayDismiss } from './ui.js';

const PET_STATUS_METHOD = 'settings.pets_status';
const PET_INSTALL_START = 'settings.pets_install_start';
const PET_LAUNCH_METHOD = 'settings.pets_launch';

const PET_INSTALL_PROGRESS = 'pets.install.progress';
const PET_INSTALL_DONE = 'pets.install.done';
const PET_INSTALL_ERROR = 'pets.install.error';

const state = { running: false, launchBusy: false, returnFocus: null };

export async function initPets() {
  const btn = document.getElementById('pet-btn');
  if (!btn) return;
  btn.addEventListener('click', onPetClick);
  bindPetInstallDialog();
  window.addEventListener('nusashell:connection-status', refreshPetStatus);
  // The Settings card drives the same paths via this cross-module event.
  // Clicking "Install" / "Launch pet" / "Stop pet" in the card dispatches
  // nusashell:open-pet-launcher; we route it through onPetClick so the
  // sidebar dot + dialog stay in sync.
  window.addEventListener('nusashell:open-pet-launcher', onPetClick);
  on(PET_INSTALL_PROGRESS, onPetProgress);
  on(PET_INSTALL_DONE, onPetDone);
  on(PET_INSTALL_ERROR, onPetError);
  refreshPetStatus();
}

async function refreshPetStatus() {
  const btn = document.getElementById('pet-btn');
  if (!btn) return;
  let status = null;
  try {
    status = await rpc(PET_STATUS_METHOD, {});
  } catch {
    btn.hidden = true;
    return;
  }
  applyStatus(btn, status);
}

function applyStatus(btn, status) {
  if (!status || !status.supported) {
    btn.hidden = true;
    return;
  }
  btn.hidden = false;
  btn.classList.toggle('is-installed', Boolean(status.installed));
  btn.classList.toggle('is-running', Boolean(status.running));
  btn.classList.toggle('is-installing', Boolean(status.install_active));
  btn.classList.toggle('is-error', false);
  if (status.installed) {
    const version = status.version ? ` · ${status.version}` : '';
    btn.title = status.running
      ? 'Desktop pet is running · click to stop'
      : `Desktop pet${version} · click to launch`;
  } else {
    btn.title = 'Desktop pet not installed · click to install';
  }
}

async function onPetClick() {
  const btn = document.getElementById('pet-btn');
  if (!btn || btn.hidden) return;
  if (btn.classList.contains('is-installing')) {
    toast('Desktop pet install is in progress.', 'info', 2500);
    return;
  }
  let status = null;
  try {
    status = await rpc(PET_STATUS_METHOD, {});
  } catch (err) {
    toast(`Pet status failed: ${err.message || err}`, 'error', 4000);
    return;
  }
  if (!status.supported) {
    btn.hidden = true;
    return;
  }
  if (status.install_active) {
    toast('Desktop pet install is in progress.', 'info', 2500);
    return;
  }
  if (status.installed) {
    if (state.launchBusy) return;
    state.launchBusy = true;
    try {
      await launchPet();
    } finally {
      state.launchBusy = false;
    }
    return;
  }
  openPetInstallDialog();
}

async function launchPet() {
  try {
    const res = await rpc(PET_LAUNCH_METHOD, {});
    if (res?.stopped) {
      toast('Desktop pet stopped.', 'success', 2200);
    } else if (res?.launched) {
      toast('Desktop pet launched.', 'success', 2200);
    } else {
      btnErrorStyle();
      toast(res?.message ? `Pet launch failed: ${res.message}` : 'Pet launch failed.', 'error', 4500);
    }
  } catch (err) {
    btnErrorStyle();
    toast(`Pet launch failed: ${err.message || err}`, 'error', 4500);
  }
  refreshPetStatus();
}

function btnErrorStyle() {
  const btn = document.getElementById('pet-btn');
  if (!btn) return;
  btn.classList.remove('is-installed', 'is-running', 'is-installing');
  btn.classList.add('is-error');
}

// ---- install dialog ----

function bindPetInstallDialog() {
  const overlay = document.getElementById('pet-install-overlay');
  if (!overlay) return;
  document.getElementById('pet-install-close')?.addEventListener('click', closePetInstallDialog);
  document.getElementById('pet-install-cancel')?.addEventListener('click', closePetInstallDialog);
  document.getElementById('pet-install-confirm')?.addEventListener('click', confirmPetInstall);
  registerOverlayDismiss(() => {
    if (!overlay.hidden && !state.running) closePetInstallDialog();
  });
}

function openPetInstallDialog() {
  const overlay = document.getElementById('pet-install-overlay');
  if (!overlay) return;
  state.returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  showPetInstallError('');
  const progress = document.getElementById('pet-install-progress');
  if (progress) progress.hidden = true;
  document.getElementById('pet-install-confirm').disabled = false;
  overlay.hidden = false;
  overlay.removeAttribute('aria-hidden');
  setTimeout(() => document.getElementById('pet-install-confirm')?.focus(), 30);
}

function closePetInstallDialog() {
  const overlay = document.getElementById('pet-install-overlay');
  if (!overlay || overlay.hidden) return;
  overlay.hidden = true;
  overlay.setAttribute('aria-hidden', 'true');
  if (!state.running) {
    const progress = document.getElementById('pet-install-progress');
    if (progress) progress.hidden = true;
  }
  if (state.returnFocus?.isConnected) state.returnFocus.focus();
  state.returnFocus = null;
}

async function confirmPetInstall() {
  const confirmBtn = document.getElementById('pet-install-confirm');
  const cancelBtn = document.getElementById('pet-install-cancel');
  if (!confirmBtn || state.running) return;
  showPetInstallError('');
  state.running = true;
  setPetInstallBusy(true);
  try {
    const res = await rpc(PET_INSTALL_START, {});
    if (res?.started === false) {
      // Another install was already running; surface the state and wait.
      toast(res?.message || 'A pet install is already running.', 'info', 3000);
      showPetProgress('Already running on the server', null, null);
    } else {
      showPetProgress('Starting…', 0, null);
    }
  } catch (err) {
    state.running = false;
    setPetInstallBusy(false);
    showPetInstallError(err.message || 'Install failed to start.');
  }
  void cancelBtn;
  void confirmBtn;
}

function setPetInstallBusy(busy) {
  document.getElementById('pet-install-confirm')?.toggleAttribute('disabled', busy);
  document.getElementById('pet-install-cancel')?.toggleAttribute('disabled', busy);
}

function showPetProgress(phase, fetched, total) {
  const progress = document.getElementById('pet-install-progress');
  const phaseEl = document.getElementById('pet-install-phase');
  const bar = document.getElementById('pet-install-bar');
  const track = document.getElementById('pet-install-bar-track');
  const bytesEl = document.getElementById('pet-install-bytes');
  if (!progress || !phaseEl || !bar || !track || !bytesEl) return;
  progress.hidden = false;
  phaseEl.textContent = phase;
  if (total && total > 0) {
    const ratio = Math.max(0, Math.min(1, fetched / total));
    bar.style.width = `${(ratio * 100).toFixed(1)}%`;
    bar.style.transform = 'none';
    track.classList.remove('indeterminate');
    bytesEl.textContent = `${formatBytes(fetched)} / ${formatBytes(total)}`;
  } else if (fetched && fetched > 0) {
    track.classList.add('indeterminate');
    bytesEl.textContent = `${formatBytes(fetched)} downloaded`;
  } else {
    track.classList.add('indeterminate');
    bytesEl.textContent = '';
  }
}

function showPetInstallError(message) {
  const banner = document.getElementById('pet-install-error');
  if (!banner) return;
  if (!message) {
    banner.textContent = '';
    banner.hidden = true;
    return;
  }
  banner.textContent = message;
  banner.hidden = false;
}

function formatBytes(n) {
  if (!Number.isFinite(n)) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MiB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GiB`;
}

// ---- event handlers ----

function onPetProgress(payload) {
  if (!state.running && !payload?.message) return;
  showPetProgress(
    payload?.message || payload?.phase || 'Working…',
    Number(payload?.bytes_fetched) || 0,
    Number(payload?.bytes_total) || 0,
  );
}

function onPetDone() {
  state.running = false;
  setPetInstallBusy(false);
  showPetProgress('Desktop pet ready', null, null);
  closePetInstallDialog();
  refreshPetStatus();
  toast('Desktop pet installed.', 'success', 2500);
}

function onPetError(payload) {
  state.running = false;
  setPetInstallBusy(false);
  showPetInstallError(payload?.message || 'Install failed.');
}
