import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

const html = await readFile(new URL('../index.html', import.meta.url), 'utf8');

test('pairing gate overlay exists with accessible attributes', () => {
  assert.match(html, /id="pairing-gate"[^>]+role="dialog"[^>]+aria-modal="true"[^>]+aria-labelledby="pairing-gate-title"/);
  assert.match(html, /id="pairing-gate-title"[^>]+tabindex="-1"/);
  assert.match(html, /id="pairing-retry-btn"/);
  assert.match(html, /id="pairing-error"[^>]+role="alert"/);
  assert.match(html, /id="pairing-help-btn"[^>]+aria-expanded="false"[^>]+aria-controls="pairing-help"/);
});

test('pairing gate has hidden attribute by default', () => {
  assert.match(html, /id="pairing-gate"[^>]+hidden/);
});

test('pairing gate includes all recovery states', () => {
  // Waiting state (host-driven: remote polls, no manual code entry)
  assert.match(html, /id="pairing-pending"/);
  assert.match(html, /id="pairing-status"/);
  // Success state
  assert.match(html, /id="pairing-success"/);
  // Error state with retry
  assert.match(html, /id="pairing-error"/);
  assert.match(html, /id="pairing-retry-btn"/);
  // Help/manual fallback
  assert.match(html, /id="pairing-help"/);
});

test('pairing gate does not use native alert/confirm/prompt', () => {
  const gateSection = html.slice(
    html.indexOf('id="pairing-gate"'),
    html.indexOf('</div>', html.indexOf('id="pairing-gate"')) + 6,
  );
  assert.doesNotMatch(gateSection, /\balert\s*\(/);
  assert.doesNotMatch(gateSection, /\bconfirm\s*\(/);
  assert.doesNotMatch(gateSection, /\bprompt\s*\(/);
});

test('settings view includes remote access pairing panel', () => {
  assert.match(html, /id="pairing-panel"/);
  assert.match(html, /id="pairing-approval-overlay"/);
  assert.match(html, /class="ui-dialog pairing-approval-dialog"[^>]+role="dialog"/);
  assert.match(html, /id="pairing-approval-title"/);
  assert.match(html, /id="pairing-approval-close"/);
  assert.match(html, /id="settings-group-remote"/);
  assert.match(html, /id="pairing-create-btn"/);
  assert.match(html, /id="pairing-approve-btn"/);
  assert.match(html, /id="pairing-reject-btn"/);
  assert.match(html, /id="pairing-sessions-list"/);
  assert.match(html, /id="pairing-revoke-all-btn"/);
  assert.match(html, /id="pairing-sessions-empty"/);
});

test('settings section nav includes remote access jump link', () => {
  assert.match(html, /id="settings-jump-remote"[^>]+data-settings-section="settings-group-remote"/);
});

test('pairing CSS is linked in index.html', () => {
  assert.match(html, /<link[^>]+href="styles\/pairing.css"/);
});

test('pairing gate has keyboard-accessible buttons with type=button', () => {
  const gateStart = html.indexOf('id="pairing-gate"');
  const gateEnd = html.indexOf('</div>', gateStart + 5000);
  const gateSection = html.slice(gateStart, gateEnd);
  // All buttons in the gate should have type="button"
  const buttons = gateSection.match(/<button[^>]*>/g) || [];
  for (const btn of buttons) {
    assert.match(btn, /type="button"/, `button missing type=button: ${btn}`);
  }
});

test('pairing gate error message element exists for state mapping', () => {
  assert.match(html, /id="pairing-error-message"/);
});

test('pairing gate countdown element exists for expiry display', () => {
  assert.match(html, /id="pairing-countdown"/);
});

test('rpc.js emits rpc:error event on PAIRING_REQUIRED', async () => {
  const rpcSrc = await readFile(new URL('../js/rpc.js', import.meta.url), 'utf8');
  assert.match(rpcSrc, /emit\('rpc:error', \{ code: 'PAIRING_REQUIRED', method \}\)/);
  assert.match(rpcSrc, /PAIRING_REQUIRED/);
});

test('pairing.js module exports init and refresh functions', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /export function initPairingGate/);
  assert.match(pairingSrc, /export async function refreshPairingPanel/);
  assert.match(pairingSrc, /export async function revokeAllSessions/);
});

test('pairing.js handles all required pairing states', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // State mapping for pending/approved/rejected/expired/used
  assert.match(pairingSrc, /pending.*Waiting for approval/);
  assert.match(pairingSrc, /approved.*Approved/);
  assert.match(pairingSrc, /rejected.*Rejected/);
  assert.match(pairingSrc, /expired.*Expired/);
  assert.match(pairingSrc, /used.*Already used/);
  // Error recovery messages
  assert.match(pairingSrc, /PAIRING_EXPIRED/);
  assert.match(pairingSrc, /PAIRING_REJECTED/);
  assert.match(pairingSrc, /PAIRING_ALREADY_USED/);
  assert.match(pairingSrc, /PAIRING_RATE_LIMITED/);
  assert.match(pairingSrc, /Backend unreachable/);
});

test('pairing.js does not store tokens in localStorage', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.doesNotMatch(pairingSrc, /localStorage\.setItem.*token/i);
  assert.doesNotMatch(pairingSrc, /localStorage\.setItem.*session/i);
});

test('pairing.js does not use native alert/confirm/prompt', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.doesNotMatch(pairingSrc, /\balert\s*\(/);
  assert.doesNotMatch(pairingSrc, /\bconfirm\s*\(/);
  assert.doesNotMatch(pairingSrc, /\bprompt\s*\(/);
});

test('host pairing controls include QR display, remote address field, and copy controls', () => {
  assert.match(html, /id="pairing-qr-display"/);
  assert.match(html, /id="pairing-qr-img"/);
  assert.match(html, /id="pairing-qr-status"/);
  assert.match(html, /id="pairing-remote-address"/);
  assert.match(html, /id="pairing-link-display"/);
  assert.match(html, /id="pairing-code-display"/);
  assert.match(html, /id="pairing-copy-link-btn"/);
  assert.match(html, /id="pairing-copy-code-btn"/);
});

test('the approval dialog owns the pairing QR, link, code, and approve controls', () => {
  const document = new JSDOM(html).window.document;
  const dialog = document.getElementById('pairing-approval-overlay');
  const panel = document.getElementById('pairing-panel');
  assert.notEqual(dialog, null);
  assert.notEqual(panel, null);
  for (const id of [
    'pairing-qr-display',
    'pairing-qr-img',
    'pairing-qr-status',
    'pairing-qr-alternatives',
    'pairing-link-display',
    'pairing-code-display',
    'pairing-copy-link-btn',
    'pairing-copy-code-btn',
    'pairing-host-status',
    'pairing-device-label',
    'pairing-approve-btn',
    'pairing-reject-btn',
  ]) {
    assert.ok(dialog.querySelector(`#${id}`), `#${id} must live inside the approval dialog`);
  }
  // The dialog covers the panel while it is open, so the panel must not keep a
  // second QR/link copy behind it.
  assert.equal(panel.querySelector('#pairing-qr-img'), null, 'no QR behind the dialog');
  assert.equal(panel.querySelector('#pairing-link-display'), null, 'no pairing link behind the dialog');
});

test('the panel keeps a status summary and a reopen control for the active challenge', () => {
  const document = new JSDOM(html).window.document;
  const panelStatus = document.getElementById('pairing-panel-status');
  const reopenBtn = document.getElementById('pairing-show-qr-btn');
  assert.notEqual(panelStatus, null, 'the panel needs its own live status for the closed-dialog state');
  assert.equal(panelStatus.getAttribute('role'), 'status');
  assert.equal(panelStatus.hidden, true, 'the summary stays hidden until a challenge exists');
  assert.notEqual(reopenBtn, null, 'the host must be able to bring the QR back without a new challenge');
  assert.equal(reopenBtn.getAttribute('type'), 'button');
  assert.equal(reopenBtn.hidden, true, 'reopen stays hidden until a challenge exists');
  assert.ok(document.getElementById('pairing-panel').contains(panelStatus));
  assert.ok(document.getElementById('pairing-panel').contains(reopenBtn));
});

test('index.html uses unique element ids', () => {
  const seen = new Map();
  for (const node of new JSDOM(html).window.document.querySelectorAll('[id]')) {
    seen.set(node.id, (seen.get(node.id) || 0) + 1);
  }
  const duplicates = [...seen].filter(([, count]) => count > 1).map(([id, count]) => `${id} (${count}x)`);
  // Duplicate ids silently break the getElementById-based wiring (a second
  // pairing-host-status previously shadowed the dialog's live status).
  assert.deepEqual(duplicates, [], `duplicate ids: ${duplicates.join(', ')}`);
});

test('host settings QR image has accessible alt text', () => {
  assert.match(html, /id="pairing-qr-img"[^>]+alt="QR code encoding the pairing URL for a remote device to scan"/);
});

test('host settings explains QR does not create reachability or rebind the listener', () => {
  assert.match(html, /does not create reachability/i);
  assert.match(html, /does not change where NusaShell listens/i);
  assert.match(html, /NUSASHELL_HOST/);
  assert.doesNotMatch(html, /NUSASHELL_ALLOW_REMOTE/);
  assert.match(html, /remote access.*off by default/i);
});

test('pairing.js imports the vendored QR library', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /import qrcode from '\.\.\/vendor\/qrcode\/qrcode\.mjs'/);
});

test('pairing.js builds a host-driven pairing URL with query params', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /pairing_challenge/);
  assert.match(pairingSrc, /pairing_code/);
  assert.match(pairingSrc, /buildPairingURL/);
});

test('pairing.js renders QR client-side from the trusted loopback response', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /renderQRDataURL/);
  assert.match(pairingSrc, /qr\.createDataURL/);
});

test('pairing.js remote flow detects query params and never creates a challenge', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // Reads pairing_challenge / pairing_code from the URL.
  assert.match(pairingSrc, /params\.get\('pairing_challenge'\)/);
  assert.match(pairingSrc, /params\.get\('pairing_code'\)/);
  // Does NOT call the removed public challenge creation route.
  assert.doesNotMatch(pairingSrc, /fetch\(['"`]\/pairing\/challenge['"`]/);
});

test('pairing.js strips secret query params from URL/history after success', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /history\.replaceState/);
  assert.match(pairingSrc, /pairing_challenge/);
});

test('pairing.js exchange does not read a token from the response body', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // The exchange handler reads only ok/error; it must not read a token field.
  assert.doesNotMatch(pairingSrc, /body\.result\.token/);
  assert.doesNotMatch(pairingSrc, /body\.token/);
  assert.doesNotMatch(pairingSrc, /localStorage\.setItem/);
});

test('pairing.js handles rate-limited exchange state', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /PAIRING_RATE_LIMITED/);
});

test('pairing.js wiring is idempotent (uses dataset.wired)', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // Multiple distinct controls are guarded by dataset.wired.
  const wiredGuards = (pairingSrc.match(/dataset\.wired/g) || []).length;
  assert.ok(wiredGuards >= 4, `expected >=4 dataset.wired guards, got ${wiredGuards}`);
});

test('pairing.js settles approve/reject after a decision', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /settleDecision/);
  assert.match(pairingSrc, /settleDecision\(true\)/);
});

test('approval requests use a popup and terminal client states do not offer retry', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /openApprovalPopup/);
  assert.match(pairingSrc, /pairing-approval-overlay/);
  assert.match(pairingSrc, /PAIRING_EXPIRED/);
  assert.match(pairingSrc, /retry: false/);
  assert.match(pairingSrc, /pairing-gate-title/);
  assert.match(pairingSrc, /\.focus\(\)/);
});

test('pairing.js validates remote address before generating QR', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /validateRemoteAddress/);
  assert.match(pairingSrc, /showAddressError/);
  assert.match(pairingSrc, /hideAddressError/);
  // Must require http/https, a host, and reject username/password.
  assert.match(pairingSrc, /http:|https:/);
  assert.match(pairingSrc, /url\.host/);
  assert.match(pairingSrc, /url\.username|url\.password/);
});

test('pairing.js uses mutable current challenge ID for approve/reject handlers', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // The handlers must read currentHostChallengeId, not a closure parameter,
  // so a second challenge's buttons act on the new ID.
  assert.match(pairingSrc, /currentHostChallengeId/);
  assert.match(pairingSrc, /decideChallenge\(currentHostChallengeId/);
});

test('pairing.js focuses gate title not hidden retry button', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // showPairingGate must focus the visible title, not the hidden retry button.
  // Extract the showPairingGate function body to check specifically.
  const fnStart = pairingSrc.indexOf('function showPairingGate()');
  const fnEnd = pairingSrc.indexOf('\n}', fnStart);
  const fnBody = pairingSrc.slice(fnStart, fnEnd);
  assert.match(fnBody, /pairing-gate-title/);
  assert.match(fnBody, /\.focus\(\)/);
  assert.doesNotMatch(fnBody, /pairing-retry-btn/);
});

test('pairing address field has inline error element', () => {
  assert.match(html, /id="pairing-address-error"[^>]+role="alert"[^>]+hidden/);
});

test('pairing.js hides the Remote access section for paired remote clients', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // PAIRING_UNAUTHORIZED (host-only management) hides the section instead of
  // showing dead controls or a permission toast on every refresh.
  assert.match(pairingSrc, /PAIRING_UNAUTHORIZED/);
  assert.match(pairingSrc, /markPairingHostOnly/);
  assert.match(pairingSrc, /settings-remote-group/);
  assert.match(pairingSrc, /settings-jump-remote/);
  // index.html: the group container carries the id the hider needs.
  assert.match(html, /id="settings-remote-group"/);
});

test('pairing.js warns when the pairing address cannot be reached', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  // A loopback address produces a QR a remote device can never reach —
  // warn (not block) so local test flows still work. The app.info listen_addr
  // check also catches a LAN link while the listener is loopback-only — the
  // failure mode that previously produced a silent remote timeout.
  assert.match(pairingSrc, /reachabilityWarning/);
  assert.match(pairingSrc, /showAddressWarning/);
  assert.match(pairingSrc, /loopback — a remote device cannot reach it/);
  assert.match(pairingSrc, /only listening on/);
  assert.match(pairingSrc, /listen_addr/);
});

test('pairing.js supports multiple persisted remote addresses', async () => {
  const pairingSrc = await readFile(new URL('../js/pairing.js', import.meta.url), 'utf8');
  assert.match(pairingSrc, /parseRemoteAddresses/);
  assert.match(pairingSrc, /pairing-qr-alternatives/);
  assert.match(pairingSrc, /urls\.slice\(1\)/);
});
