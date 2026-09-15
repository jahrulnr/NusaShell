// Device pairing gate and management UI.
//
// Host-driven flow:
//   - The host (loopback) creates a short-lived challenge via the local-only
//     pairing.challenge.create RPC and displays a QR + manual link/code inside
//     the approval dialog, which is also where the request is approved.
//   - The QR encodes a reachable URL with the challenge id + one-time code,
//     rendered client-side from the trusted loopback response so the secret
//     never appears in a server access log path.
//   - The dialog owns that QR because its overlay covers the Settings panel;
//     the panel keeps a state summary plus a control that reopens the same
//     challenge instead of minting a new one.
//   - The remote device opens that URL, polls the public status route, and
//     exchanges after the host approves. The remote never creates its own
//     challenge.
//
// Security:
//   - The session token is delivered only as an HttpOnly cookie; it is never
//     read by JavaScript, never stored in localStorage, and never appears in
//     the public exchange response body.
//   - Pairing management (create/approve/reject/status/sessions.*) is
//     local-only and enforced by the backend; this UI only exposes it on the
//     host.

import { rpc, on } from './rpc.js';
import { el, registerOverlayDismiss, toast } from './ui.js';
import qrcode from '../vendor/qrcode/qrcode.mjs';

const POLL_INTERVAL_MS = 1500;
const POLL_TIMEOUT_MS = 5 * 60 * 1000; // stop polling after 5 min

let gateEl = null;
let pollTimer = null;
let pollStartedAt = 0;
let countdownTimer = null;

// ---- pairing gate overlay (shown to unauthenticated remote clients) ----

export function initPairingGate() {
  gateEl = document.getElementById('pairing-gate');
  if (!gateEl) return;

  // Listen for PAIRING_REQUIRED errors on the RPC layer.
  on('rpc:error', (detail) => {
    if (detail?.code === 'PAIRING_REQUIRED') {
      showPairingGate();
    } else if (detail?.code === 'REMOTE_ACCESS_DISABLED') {
      showRemoteAccessDisabledGate();
    }
  });

  // If the page was opened with ?pairing_challenge=...&pairing_code=...,
  // start the remote waiting flow immediately. The remote device does NOT
  // create a challenge; it only polls the host-created one.
  const params = new URLSearchParams(location.search);
  const challengeId = params.get('pairing_challenge');
  const code = params.get('pairing_code');
  if (challengeId && code) {
    showPairingGate();
    startRemoteWaiting(challengeId, code);
  }

  // Wire gate buttons (idempotent).
  wireGateButtons();
}

function wireGateButtons() {
  const retryBtn = document.getElementById('pairing-retry-btn');
  if (retryBtn && !retryBtn.dataset.wired) {
    retryBtn.dataset.wired = '1';
    retryBtn.addEventListener('click', () => {
      hideGateError();
      // Retry re-reads the URL params (the host may have generated a new link).
      const params = new URLSearchParams(location.search);
      const challengeId = params.get('pairing_challenge');
      const code = params.get('pairing_code');
      if (challengeId && code) {
        startRemoteWaiting(challengeId, code);
      }
    });
  }
  const helpBtn = document.getElementById('pairing-help-btn');
  if (helpBtn && !helpBtn.dataset.wired) {
    helpBtn.dataset.wired = '1';
    helpBtn.addEventListener('click', () => toggleHelp());
  }
  const helpCloseBtn = document.getElementById('pairing-help-close');
  if (helpCloseBtn && !helpCloseBtn.dataset.wired) {
    helpCloseBtn.dataset.wired = '1';
    helpCloseBtn.addEventListener('click', () => toggleHelp());
  }
}

function showPairingGate() {
  if (!gateEl) return;
  gateEl.hidden = false;
  setTimeout(() => {
    // Focus the visible gate title (not the hidden retry button) so screen
    // readers and keyboard users land on a visible focus target. The retry
    // button is inside the hidden error div and is only focusable after an
    // error is shown.
    const title = document.getElementById('pairing-gate-title');
    title?.focus();
  }, 50);
}

function resetPairingGateCopy() {
  const title = document.getElementById('pairing-gate-title');
  const intro = document.getElementById('pairing-gate-intro');
  if (title) title.textContent = 'Device pairing required';
  if (intro) intro.textContent = 'This NusaShell instance only allows paired remote devices. Open the pairing link shown on the host (Settings → Remote access) to continue.';
}

function showRemoteAccessDisabledGate() {
  showPairingGate();
  const title = document.getElementById('pairing-gate-title');
  const intro = document.getElementById('pairing-gate-intro');
  if (title) title.textContent = 'Remote access disabled';
  if (intro) intro.textContent = 'Remote access is disabled on this NusaShell instance. Ask the host to enable it in Settings → Remote access.';
  const pending = document.getElementById('pairing-pending');
  if (pending) pending.hidden = true;
  showGateError('The host has not enabled remote access yet.', { retry: false });
}

function hidePairingGate() {
  if (!gateEl) return;
  gateEl.hidden = true;
  stopPolling();
}

function showGateError(message, { retry = true } = {}) {
  const errorEl = document.getElementById('pairing-error');
  const errorMsg = document.getElementById('pairing-error-message');
  const retryBtn = document.getElementById('pairing-retry-btn');
  if (!errorEl || !errorMsg) return;
  errorMsg.textContent = message;
  errorEl.hidden = false;
  errorEl.dataset.terminal = retry ? 'false' : 'true';
  if (retryBtn) retryBtn.hidden = !retry;
}

function showTerminalGate(titleText, message) {
  stopPolling();
  const title = document.getElementById('pairing-gate-title');
  const intro = document.getElementById('pairing-gate-intro');
  const pending = document.getElementById('pairing-pending');
  const countdown = document.getElementById('pairing-countdown');
  if (title) title.textContent = titleText;
  if (intro) intro.textContent = 'This pairing link cannot be used anymore. Ask the host to generate a new pairing link.';
  if (pending) pending.hidden = true;
  if (countdown) countdown.hidden = true;
  showGateError(message, { retry: false });
}

function hideGateError() {
  const errorEl = document.getElementById('pairing-error');
  if (errorEl) errorEl.hidden = true;
}

function toggleHelp() {
  const help = document.getElementById('pairing-help');
  const helpBtn = document.getElementById('pairing-help-btn');
  if (!help) return;
  help.hidden = !help.hidden;
  if (helpBtn) helpBtn.setAttribute('aria-expanded', String(!help.hidden));
  if (!help.hidden) {
    const closeBtn = document.getElementById('pairing-help-close');
    closeBtn?.focus();
  }
}

// ---- remote waiting flow (host-driven) ----
//
// The remote device arrives with ?pairing_challenge=...&pairing_code=... from
// the QR/link the host generated. It polls the public status route and, once
// the host approves, exchanges the code for a session cookie. The token is
// never exposed to JavaScript; the server sets the HttpOnly cookie. After
// success, the secret query params are stripped from the address bar and
// history so they are not visible to the user or shared accidentally.

async function startRemoteWaiting(challengeId, code) {
  const pendingEl = document.getElementById('pairing-pending');
  resetPairingGateCopy();
  hideGateError();
  stopPolling();
  if (pendingEl) pendingEl.hidden = false;
  updateRemoteStatus('pending');
  startStatusPolling(challengeId, code);
}

function startStatusPolling(challengeId, code) {
  stopPolling();
  pollStartedAt = Date.now();
  pollTimer = setInterval(async () => {
    if (Date.now() - pollStartedAt > POLL_TIMEOUT_MS) {
      stopPolling();
      showTerminalGate('Pairing link expired', 'The pairing link expired before approval. Ask the host to generate a new QR/link.');
      return;
    }
    try {
      const res = await fetch(`/pairing/status?challenge_id=${encodeURIComponent(challengeId)}`);
      const body = await res.json().catch(() => ({}));
      if (!body.ok) {
        // Backend returned an error (not found, etc.).
        const code = body.error?.code;
        if (code === 'PAIRING_NOT_FOUND') {
          stopPolling();
          showTerminalGate('Pairing link unavailable', 'This pairing link is no longer valid. Ask the host to generate a new QR/link.');
        }
        return;
      }
      const { state, expires_in } = body.result;
      updateRemoteStatus(state);
      if (expires_in > 0) updateCountdown(expires_in);
      if (state === 'approved') {
        stopPolling();
        await exchangeChallenge(challengeId, code);
      } else if (state === 'rejected') {
        stopPolling();
        showTerminalGate('Pairing request declined', 'The host declined this pairing request. Ask the host to generate a new QR/link if needed.');
      } else if (state === 'expired') {
        stopPolling();
        showTerminalGate('Pairing link expired', 'The pairing link has expired. Ask the host to generate a new QR/link.');
      } else if (state === 'used') {
        stopPolling();
        showTerminalGate('Pairing link already used', 'This pairing link was already used. Ask the host to generate a new QR/link.');
      }
    } catch {
      // Network blip — keep polling but surface a reachable state.
      updateRemoteStatus('unreachable');
    }
  }, POLL_INTERVAL_MS);
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
  if (countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
}

function updateRemoteStatus(state) {
  const statusEl = document.getElementById('pairing-status');
  if (!statusEl) return;
  const labels = {
    pending: 'Waiting for approval on the host device…',
    approved: 'Approved! Connecting…',
    rejected: 'Rejected',
    expired: 'Expired',
    used: 'Already used',
    unreachable: 'Cannot reach the host. Retrying…',
  };
  statusEl.textContent = labels[state] || state;
  statusEl.dataset.state = state;
}

function updateCountdown(seconds) {
  if (countdownTimer) clearInterval(countdownTimer);
  const countdownEl = document.getElementById('pairing-countdown');
  if (!countdownEl) return;
  let remaining = seconds;
  countdownEl.textContent = formatCountdown(remaining);
  countdownTimer = setInterval(() => {
    remaining -= 1;
    if (remaining <= 0) {
      clearInterval(countdownTimer);
      countdownEl.textContent = 'expired';
      return;
    }
    countdownEl.textContent = formatCountdown(remaining);
  }, 1000);
}

function formatCountdown(seconds) {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

async function exchangeChallenge(challengeId, code) {
  hideGateError();
  try {
    const res = await fetch('/pairing/exchange', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ challenge_id: challengeId, code }),
    });
    const body = await res.json().catch(() => ({}));
    if (!body.ok) {
      const errCode = body.error?.code;
      if (errCode === 'PAIRING_EXPIRED') {
        showTerminalGate('Pairing link expired', 'The pairing link has expired. Ask the host to generate a new QR/link.');
      } else if (errCode === 'PAIRING_REJECTED') {
        showTerminalGate('Pairing request declined', 'The host declined this pairing request. Ask the host to generate a new QR/link if needed.');
      } else if (errCode === 'PAIRING_ALREADY_USED') {
        showTerminalGate('Pairing link already used', 'This pairing link was already used. Ask the host to generate a new QR/link.');
      } else if (errCode === 'PAIRING_RATE_LIMITED') {
        showGateError('Too many pairing attempts. Please wait a minute and try again.', { retry: true });
      } else if (errCode === 'PAIRING_REQUIRED') {
        showGateError('This pairing request is still pending approval. Please approve it on the host device.', { retry: false });
      } else {
        showGateError(body.error?.message || 'Pairing failed.', { retry: true });
      }
      return;
    }
    // Success — the server set the HttpOnly cookie. Strip the secret query
    // params from the address bar and history so they are not visible or
    // shared accidentally, then reload to apply the session.
    stopPolling();
    const successEl = document.getElementById('pairing-success');
    if (successEl) successEl.hidden = false;
    try {
      const cleanUrl = location.origin + location.pathname;
      history.replaceState(null, '', cleanUrl);
    } catch {
      // Some environments block history mutation; the cookie is still set.
    }
    setTimeout(() => location.reload(), 600);
  } catch {
    showGateError('Backend unreachable. Please check your connection and try again.', { retry: true });
  }
}

// ---- host-side pairing management (local Settings view) ----

// pairingHostOnly becomes true when a pairing management RPC answers
// PAIRING_UNAUTHORIZED: this client is a paired remote device and pairing
// administration is loopback-only. The whole Remote access section is hidden
// so the remote UI shows no dead controls and no permission-error toasts.
let pairingHostOnly = false;

function markPairingHostOnly() {
  pairingHostOnly = true;
  const group = document.getElementById('settings-remote-group');
  if (group) group.hidden = true;
  const navBtn = document.getElementById('settings-jump-remote');
  if (navBtn) navBtn.hidden = true;
}

export async function refreshPairingPanel({ enabled = true } = {}) {
  const panel = document.getElementById('pairing-panel');
  if (!panel) return;
  panel.hidden = !enabled;
  if (!enabled) return;
  // Remote paired client: keep the section hidden and skip every management
  // RPC — they would all 403.
  if (pairingHostOnly) {
    markPairingHostOnly();
    return;
  }

  // Idempotently wire the create button so repeated refreshes do not stack
  // duplicate click listeners.
  const createBtn = document.getElementById('pairing-create-btn');
  if (createBtn && !createBtn.dataset.wired) {
    createBtn.dataset.wired = '1';
    createBtn.addEventListener('click', startHostPairing);
  }
  // The dialog covers this panel while it is open, so the QR lives there. This
  // control brings the same challenge (and its QR) back into view.
  const showQrBtn = document.getElementById('pairing-show-qr-btn');
  if (showQrBtn && !showQrBtn.dataset.wired) {
    showQrBtn.dataset.wired = '1';
    showQrBtn.addEventListener('click', () => {
      if (activeHostChallenge) showChallenge(activeHostChallenge);
    });
  }
  // Idempotently wire copy buttons.
  wireCopyButton('pairing-copy-link-btn', () => document.getElementById('pairing-link-display')?.textContent || '');
  wireCopyButton('pairing-copy-code-btn', () => document.getElementById('pairing-code-display')?.textContent || '');

  // Default the remote address to the current origin (the most useful
  // default for a loopback desktop user who has exposed the same port).
  const addrInput = document.getElementById('pairing-remote-address');
  if (addrInput && !addrInput.value) {
    try {
      addrInput.value = location.origin;
    } catch {
      // location.origin may be unavailable in some test contexts.
    }
  }

  await loadSessions();
}

function wireCopyButton(id, getText) {
  const btn = document.getElementById(id);
  if (!btn || btn.dataset.wired) return;
  btn.dataset.wired = '1';
  btn.addEventListener('click', async () => {
    const text = getText();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      toast('Copied', 'success');
    } catch {
      toast('Copy failed — select and copy manually', 'error');
    }
  });
}

// validateRemoteAddress validates the host's editable remote address for QR
// generation. It requires an absolute http/https URL with a host and no
// username/password. Returns an error message string, or '' if valid.
function validateRemoteAddress(base) {
  let url;
  try {
    url = new URL(base);
  } catch {
    return 'Enter a valid URL (e.g. https://192.168.1.10:8443).';
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return 'Address must use http or https.';
  }
  if (!url.host) {
    return 'Address must include a host.';
  }
  if (url.username || url.password) {
    return 'Address must not contain a username or password.';
  }
  return '';
}

function parseRemoteAddresses(raw) {
  return String(raw || '')
    .split(/\r?\n/)
    .map((value) => value.trim())
    .filter(Boolean);
}

// showAddressNotice displays a message under the remote-address field.
// severity 'error' blocks generation; 'warning' is advisory only — a
// loopback address produces a QR a remote device can never reach, which is
// worth warning about but not worth blocking (local test flows use it).
function showAddressNotice(message, severity) {
  const errEl = document.getElementById('pairing-address-error');
  if (!errEl) return;
  errEl.textContent = message;
  errEl.dataset.severity = severity;
  errEl.hidden = false;
}

function showAddressError(message) {
  showAddressNotice(message, 'error');
}

function showAddressWarning(message) {
  showAddressNotice(message, 'warning');
}

function hideAddressError() {
  const errEl = document.getElementById('pairing-address-error');
  if (errEl) errEl.hidden = true;
}

// isLoopbackHost reports whether a bare hostname is local-only. Wildcard bind
// addresses are handled separately because they accept remote connections but
// are not themselves valid addresses to encode into a QR.
function isLoopbackHost(host) {
  return host === 'localhost' || host.endsWith('.localhost')
    || host === '127.0.0.1' || host.startsWith('127.')
    || host === '::1' || host === '[::1]';
}

function isWildcardHost(host) {
  return host === '0.0.0.0' || host === '::' || host === '[::]';
}

// fetchListenAddr reads the bound listener address ("host:port") reported by
// app.info. Returns '' when unavailable (older backend) — the caller then
// falls back to the loopback-only check.
async function fetchListenAddr() {
  try {
    const info = await rpc('app.info');
    return info?.listen_addr || '';
  } catch {
    return '';
  }
}

// reachabilityWarning returns an advisory message when the pairing address
// cannot be reachable given the bound listener, or '' when it looks fine.
// These are warnings, not errors: tunnels and proxies that forward to the
// listener are legitimate and undetectable from here.
function reachabilityWarning(base, listen) {
  let u;
  try { u = new URL(base); } catch { return ''; }
  const addrHost = u.hostname;
  if (isLoopbackHost(addrHost) || isWildcardHost(addrHost)) {
    return 'This address is loopback — a remote device cannot reach it. Set a LAN/public address or a tunnel URL. NUSASHELL_HOST controls the listener; remote access is controlled in Settings.';
  }
  if (!listen) return '';
  const sep = listen.lastIndexOf(':');
  const listenHost = sep > 0 ? listen.slice(0, sep) : listen;
  const listenPort = sep > 0 ? listen.slice(sep + 1) : '';
  if (isLoopbackHost(listenHost)) {
    return `NusaShell is only listening on ${listen} — a remote device cannot reach this link. Set NUSASHELL_HOST to a reachable bind host, or use a tunnel/proxy that forwards to loopback.`;
  }
  if (isWildcardHost(listenHost)) return '';
  const addrPort = u.port || (u.protocol === 'https:' ? '443' : '80');
  if (listenPort && addrPort !== listenPort) {
    return `This link uses port ${addrPort} but NusaShell listens on port ${listenPort} — a remote device will time out unless a tunnel/proxy forwards it.`;
  }
  if (addrHost !== listenHost) {
    return `NusaShell listens on ${listenHost} but this link uses ${addrHost} — a remote device will time out unless a tunnel/proxy forwards it.`;
  }
  return '';
}

async function startHostPairing() {
  const createBtn = document.getElementById('pairing-create-btn');
  const addrInput = document.getElementById('pairing-remote-address');
  if (createBtn) createBtn.disabled = true;
  try {
    const addresses = parseRemoteAddresses(addrInput?.value);
    if (addresses.length === 0) addresses.push(location.origin);
    for (const address of addresses) {
      // Validate every editable remote address before creating a challenge or
      // generating a QR. A malformed entry shows an actionable error instead
      // of creating a challenge no remote device can use.
      const addrErr = validateRemoteAddress(address);
      if (addrErr) {
        showAddressError(`${addrErr} Check: ${address}`);
        return;
      }
    }
    const listen = await fetchListenAddr();
    const warnings = addresses.map((address) => reachabilityWarning(address, listen)).filter(Boolean);
    if (warnings.length > 0) {
      showAddressWarning(warnings.join(' '));
    } else {
      hideAddressError();
    }
    const result = await rpc('pairing.challenge.create');
    // Build the pairing URL from the user-editable remote address. The QR
    // encodes this URL so the remote device opens the embedded frontend with
    // the challenge id + one-time code as query params.
    const urls = addresses.map((address) => buildPairingURL(address, result.challenge_id, result.code));
    activeHostChallenge = {
      id: result.challenge_id,
      urls,
      code: result.code,
      // One scanned QR is worth more than a warning the user cannot see while
      // the dialog is open, so the caveat travels into the dialog too.
      addressWarning: warnings.length > 0,
    };
    // Render the QR inside the approval dialog and follow the challenge state.
    showChallenge(activeHostChallenge);
    setChallengeStatus('pending', `Code expires in ${Math.ceil(result.expires_in / 60)} minute(s).`);
    pollHostChallenge(result.challenge_id);
  } catch (err) {
    toast(err.message || 'Failed to create pairing code', 'error');
  } finally {
    if (createBtn) createBtn.disabled = false;
  }
}

// showChallenge renders the active challenge inside the approval dialog and
// opens it. The dialog owns the QR, link, and manual code because its overlay
// covers the Settings panel; reopening re-renders the same challenge, so the
// one-time code and the QR stay identical.
function showChallenge(challenge) {
  const qrDisplay = document.getElementById('pairing-qr-display');
  const qrImg = document.getElementById('pairing-qr-img');
  const qrStatus = document.getElementById('pairing-qr-status');
  const linkEl = document.getElementById('pairing-link-display');
  const codeEl = document.getElementById('pairing-code-display');
  const url = challenge.urls[0];
  if (linkEl) linkEl.textContent = url;
  if (codeEl) codeEl.textContent = challenge.code;
  // Render the QR client-side from the trusted loopback response. The secret
  // never hits a server access log path.
  if (qrImg) {
    try {
      qrImg.src = renderQRDataURL(url);
      qrImg.alt = `QR code encoding the pairing URL for a remote device to scan: ${url}`;
      if (qrStatus) {
        qrStatus.textContent = challenge.addressWarning
          ? 'This address may be unreachable from the remote device — check the address notice in Settings.'
          : 'Scan with a remote device, or copy the link/code below.';
      }
    } catch {
      if (qrStatus) qrStatus.textContent = 'QR rendering failed. Use the manual link/code below.';
    }
  }
  if (qrDisplay) qrDisplay.hidden = false;
  renderAlternativePairingLinks(document.getElementById('pairing-qr-alternatives'), challenge.urls.slice(1));
  openApprovalPopup();
}

function renderAlternativePairingLinks(container, urls) {
  if (!container) return;
  container.replaceChildren();
  for (const url of urls) {
    const card = el('div', { class: 'pairing-alternative', dataset: { pairingAddress: url } });
    const image = el('img', {
      class: 'pairing-qr',
      alt: `QR code encoding the pairing URL for a remote device to scan: ${url}`,
    });
    try {
      image.src = renderQRDataURL(url);
    } catch {
      image.alt = `Pairing link: ${url}`;
    }
    card.append(
      image,
      el('code', { class: 'pairing-link', text: url }),
    );
    container.append(card);
  }
}

// buildPairingURL constructs the pairing URL the remote device will open. It
// targets the embedded frontend root with query params the gate detects.
function buildPairingURL(base, challengeId, code) {
  let origin = base;
  // Trim trailing slash so the query lands on the root path.
  if (origin.endsWith('/')) origin = origin.slice(0, -1);
  const params = new URLSearchParams({
    pairing_challenge: challengeId,
    pairing_code: code,
  });
  return `${origin}/?${params.toString()}`;
}

// renderQRDataURL renders the URL as a QR code data URL using the vendored
// qrcode library (Kazuhiko Arase, MIT). typeNumber 0 = auto-detect the
// smallest version that fits; errorCorrectionLevel 'M' balances density and
// resilience.
function renderQRDataURL(text) {
  const qr = qrcode(0, 'M');
  qr.addData(text);
  qr.make();
  return qr.createDataURL(4, 2);
}

let hostPollTimer = null;
// currentHostChallengeId is the mutable challenge ID the approve/reject
// handlers act on. It is updated each time pollHostChallenge is called so a
// second challenge's buttons act on the new ID, not the stale closure value
// from the first wiring.
let currentHostChallengeId = null;

// activeHostChallenge is the challenge currently displayed by the approval
// dialog: { id, urls, code, addressWarning }. It is kept so the dialog can be
// reopened without creating a second challenge (and a second one-time code).
let activeHostChallenge = null;

// HOST_STATE_LABELS maps a host-observed challenge state to the text shown in
// the approval dialog and in the Settings summary.
const HOST_STATE_LABELS = {
  pending: 'Waiting for a remote device to scan the QR code…',
  approved: 'Approved — waiting for the remote device to connect…',
  rejected: 'Pairing request rejected',
  expired: 'Pairing link expired',
  used: 'Paired successfully',
};
// TERMINAL_HOST_STATES end the challenge for good: the dialog closes and the
// panel summary stops offering a QR that can no longer pair anything.
const TERMINAL_HOST_STATES = new Set(['rejected', 'expired', 'used']);

// setChallengeStatus publishes one challenge state to both surfaces: the
// approval dialog (readable while it is open) and the Settings panel summary
// (readable after it is closed, with a reopen control while the link works).
function setChallengeStatus(state, detail = '') {
  const label = HOST_STATE_LABELS[state] || state;
  const text = detail ? `${detail} ${label}` : label;
  const dialogStatus = document.getElementById('pairing-host-status');
  if (dialogStatus) dialogStatus.textContent = text;
  const panelStatus = document.getElementById('pairing-panel-status');
  if (panelStatus) {
    panelStatus.textContent = text;
    panelStatus.dataset.state = state;
    panelStatus.hidden = false;
  }
  const reopenBtn = document.getElementById('pairing-show-qr-btn');
  if (reopenBtn) reopenBtn.hidden = state !== 'pending' || !activeHostChallenge;
}

// setPanelStatusLive keeps exactly one pairing status region live at a time:
// while the dialog is open its own status owns announcements, and the panel
// summary takes over as soon as the dialog closes (the panel stays in the
// accessibility tree behind the overlay).
function setPanelStatusLive(live) {
  document.getElementById('pairing-panel-status')?.setAttribute('aria-live', live ? 'polite' : 'off');
}

let approvalPopupReturnFocus = null;
let unregisterApprovalPopupDismiss = null;

function approvalPopupFocusables(overlay) {
  return [...overlay.querySelectorAll('button, input, textarea, select, [tabindex]:not([tabindex="-1"])')]
    .filter((node) => !node.disabled && !node.hidden);
}

function closeApprovalPopup({ returnFocus = true } = {}) {
  setPanelStatusLive(true);
  const overlay = document.getElementById('pairing-approval-overlay');
  if (!overlay) return;
  if (unregisterApprovalPopupDismiss) {
    unregisterApprovalPopupDismiss();
    unregisterApprovalPopupDismiss = null;
  }
  overlay.hidden = true;
  overlay.setAttribute('aria-hidden', 'true');
  const target = approvalPopupReturnFocus;
  approvalPopupReturnFocus = null;
  if (returnFocus && target?.isConnected) target.focus();
  if (!returnFocus && overlay.contains(document.activeElement)) {
    document.getElementById('pairing-create-btn')?.focus();
  }
}

function wireApprovalPopup() {
  const overlay = document.getElementById('pairing-approval-overlay');
  if (!overlay || overlay.dataset.wired) return;
  overlay.dataset.wired = '1';
  document.getElementById('pairing-approval-close')?.addEventListener('click', () => closeApprovalPopup());
  overlay.addEventListener('mousedown', (event) => {
    if (event.target === overlay) closeApprovalPopup();
  });
  overlay.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      closeApprovalPopup();
      return;
    }
    if (event.key !== 'Tab') return;
    const nodes = approvalPopupFocusables(overlay);
    if (!nodes.length) return;
    const first = nodes[0];
    const last = nodes[nodes.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  });
}

function openApprovalPopup() {
  const overlay = document.getElementById('pairing-approval-overlay');
  if (!overlay) return;
  wireApprovalPopup();
  unregisterApprovalPopupDismiss = registerOverlayDismiss(() => closeApprovalPopup({ returnFocus: false }));
  approvalPopupReturnFocus = document.activeElement;
  overlay.hidden = false;
  overlay.setAttribute('aria-hidden', 'false');
  // The dialog is the visible surface now: its own status region announces
  // state changes, so the panel summary stops announcing until it is back.
  setPanelStatusLive(false);
  // Resolve the focus target now: the deferred callback must not reach for
  // `document` after the dialog's owner may have gone away. Focus the title
  // (not Approve at the bottom) so the dialog opens at the top, with the QR in
  // view and a readable announcement for screen readers.
  const title = document.getElementById('pairing-approval-title');
  setTimeout(() => title?.focus(), 30);
}

function pollHostChallenge(challengeId) {
  if (hostPollTimer) clearInterval(hostPollTimer);
  currentHostChallengeId = challengeId;
  const approveBtn = document.getElementById('pairing-approve-btn');
  const rejectBtn = document.getElementById('pairing-reject-btn');
  // Idempotently wire approve/reject and reset their settled state for the
  // new challenge. The handlers read currentHostChallengeId (not the closure
  // parameter) so a second challenge's buttons act on the new ID.
  if (approveBtn && !approveBtn.dataset.wired) {
    approveBtn.dataset.wired = '1';
    approveBtn.addEventListener('click', () => decideChallenge(currentHostChallengeId, 'approve'));
  }
  if (rejectBtn && !rejectBtn.dataset.wired) {
    rejectBtn.dataset.wired = '1';
    rejectBtn.addEventListener('click', () => decideChallenge(currentHostChallengeId, 'reject'));
  }
  // Reset settled state for a fresh challenge.
  settleDecision(false);

  hostPollTimer = setInterval(async () => {
    try {
      const result = await rpc('pairing.status', { challenge_id: challengeId });
      if (TERMINAL_HOST_STATES.has(result.state)) {
        clearInterval(hostPollTimer);
        hostPollTimer = null;
        // Close first: that hands announcements back to the panel summary, so
        // the terminal state is announced where the user now reads it.
        closeApprovalPopup({ returnFocus: false });
        setChallengeStatus(result.state);
        if (result.state === 'used') {
          toast('Device paired successfully', 'success');
          await loadSessions();
        }
        // Settle the approve/reject controls after a terminal decision.
        settleDecision(true);
        return;
      }
      setChallengeStatus(result.state);
    } catch {
      // Keep polling on transient errors.
    }
  }, POLL_INTERVAL_MS);
}

// settleDecision disables the approve/reject buttons after a decision so the
// user cannot double-act, or re-enables them for a new challenge.
function settleDecision(disabled) {
  const approveBtn = document.getElementById('pairing-approve-btn');
  const rejectBtn = document.getElementById('pairing-reject-btn');
  if (approveBtn) approveBtn.disabled = disabled;
  if (rejectBtn) rejectBtn.disabled = disabled;
}

async function decideChallenge(challengeId, decision) {
  const labelInput = document.getElementById('pairing-device-label');
  const deviceLabel = labelInput?.value?.trim() || '';
  // Settle immediately so a double-click cannot fire twice.
  settleDecision(true);
  try {
    if (decision === 'approve') {
      await rpc('pairing.challenge.approve', { challenge_id: challengeId, device_label: deviceLabel });
      toast('Pairing approved', 'success');
    } else {
      await rpc('pairing.challenge.reject', { challenge_id: challengeId });
      toast('Pairing rejected', 'info');
      closeApprovalPopup({ returnFocus: false });
    }
  } catch (err) {
    // Re-enable on failure so the user can retry.
    settleDecision(false);
    toast(err.message || 'Failed to decide pairing', 'error');
  }
}

async function loadSessions() {
  const listEl = document.getElementById('pairing-sessions-list');
  const emptyEl = document.getElementById('pairing-sessions-empty');
  if (!listEl) return;
  try {
    const result = await rpc('pairing.sessions.list');
    listEl.innerHTML = '';
    // Revoked sessions are terminal and should not remain visible even if a
    // response comes from an older backend that still retains those rows.
    const sessions = (result.sessions || []).filter((sess) => !sess.revoked);
    if (sessions.length === 0) {
      if (emptyEl) emptyEl.hidden = false;
      return;
    }
    if (emptyEl) emptyEl.hidden = true;
    for (const sess of sessions) {
      listEl.append(renderSessionRow(sess));
    }
  } catch (err) {
    if (err?.code === 'PAIRING_UNAUTHORIZED') {
      // Paired remote device: pairing administration is host-only. Hide the
      // section instead of toasting a permission error on every refresh.
      markPairingHostOnly();
      return;
    }
    if (err.code !== 'PAIRING_REQUIRED' && err.code !== 'REMOTE_ACCESS_DISABLED') {
      toast(err.message || 'Failed to load sessions', 'error');
    }
  }
}

function renderSessionRow(sess) {
  return el('div', { class: 'pairing-session-row', dataset: { id: sess.id } },
    el('div', { class: 'pairing-session-info' },
      el('div', { class: 'pairing-session-label', text: sess.device_label || 'Remote device' }),
      el('div', { class: 'pairing-session-meta' },
        `Created ${new Date(sess.created_at).toLocaleDateString()} · Last seen ${new Date(sess.last_seen_at).toLocaleDateString()}`),
    ),
    el('div', { class: 'pairing-session-actions' },
      el('button', {
        class: 'mini-btn danger',
        type: 'button',
        text: 'Revoke',
        onclick: () => revokeSession(sess.id),
      }),
    ),
  );
}

async function revokeSession(id) {
  try {
    await rpc('pairing.sessions.revoke', { id });
    toast('Paired device removed', 'success');
    await loadSessions();
  } catch (err) {
    toast(err.message || 'Failed to revoke session', 'error');
  }
}

export async function revokeAllSessions() {
  try {
    await rpc('pairing.sessions.revoke-all');
    toast('All paired devices removed', 'success');
    await loadSessions();
  } catch (err) {
    toast(err.message || 'Failed to revoke sessions', 'error');
  }
}
