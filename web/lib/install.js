// Settling an install: the page has to survive the server disappearing under
// it. Three things can happen after the install request is accepted, and only
// one of them is the server's word:
//
//  - the progress endpoint keeps answering and ends in `failed` — a real
//    failure, reported and never reloaded away;
//  - the endpoint keeps answering and ends in `succeeded` — the install ran a
//    restart hook that may put a new process in place;
//  - the process is replaced (reexec) before it can say anything, so the poll
//    sees the connection drop and then a *new* process answering `idle` — the
//    state the install was in never comes back.
//
// So the install state cannot be the signal: it dies with the process that
// held it. The version can, and it is what the page reloads on. Every path
// except `failed` therefore ends the same way — wait for the server to answer
// again with a version different from the one we started on, then reload. A
// bound on every wait, so a server that never comes back, or never changes
// version, leaves a page that says so instead of a page that spins forever.
//
// The waits are counted in ticks rather than read from the clock: the sleep is
// injected, so a test drives the whole settle without waiting.
export const INSTALL_POLL_MS = 1000;
// A download can take a while; this is the longest a running install is read.
export const INSTALL_POLL_LIMIT_MS = 10 * 60_000;
// Reads that fail in a row, with no answer between them, mean the server went
// away for its restart rather than that the network hiccuped.
export const INSTALL_POLL_FAILS = 5;
// How long a new version has to answer after the old server is gone.
export const INSTALL_WAIT_LIMIT_MS = 2 * 60_000;

// settleInstall follows the install to its settlement and calls `reload` once
// the running server answers with a version other than `from`. It answers
// { outcome, progress, version, error }:
//   reloaded — the new version answered; the page was reloaded
//   failed   — the install reported a failure; nothing was reloaded
//   timeout  — the server never answered with a new version inside the bound
// Injected: progress() reads the install state, version() reads the running
// version, sleep(ms) waits, reload() refreshes the page.
export async function settleInstall({
  progress,
  version,
  sleep,
  reload,
  onProgress = () => {},
  from = '',
  pollMs = INSTALL_POLL_MS,
  pollFails = INSTALL_POLL_FAILS,
  pollLimitMs = INSTALL_POLL_LIMIT_MS,
  waitLimitMs = INSTALL_WAIT_LIMIT_MS,
}) {
  let last = null;
  let fails = 0;
  const polls = Math.max(1, Math.ceil(pollLimitMs / pollMs));
  for (let i = 0; i < polls; i++) {
    try {
      const p = await progress();
      fails = 0;
      if (p) {
        last = p;
        onProgress(p);
        if (p.state && p.state !== 'running') break;
      }
    } catch {
      // The request failing is the restart showing itself: the process is
      // being replaced, and the next answer comes from the new one.
      fails += 1;
      if (fails >= pollFails) break;
    }
    await sleep(pollMs);
  }
  if (last?.state === 'failed') return { outcome: 'failed', progress: last, error: last.error || '' };

  // From here the version is the only witness. When the page never learned
  // the version it is running, the first answer it gets is the anchor.
  let base = from;
  if (!base) {
    try { base = (await version())?.current || ''; } catch { /* still down: the loop below waits */ }
  }
  let seen = '';
  const waits = Math.max(1, Math.ceil(waitLimitMs / pollMs));
  for (let i = 0; i < waits; i++) {
    await sleep(pollMs);
    try {
      const v = await version();
      seen = v?.current || '';
      if (!seen) continue;
      // A server that never restarted keeps answering the old version; only a
      // different one means the install actually landed.
      if (!base || seen !== base) {
        reload();
        return { outcome: 'reloaded', version: seen, progress: last };
      }
    } catch {
      // Not back yet.
    }
  }
  return { outcome: 'timeout', version: seen, progress: last };
}
