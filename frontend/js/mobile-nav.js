// Mobile navigation drawer controller.
//
// Below the 680px shell breakpoint the sidebar leaves normal flow and
// becomes an off-canvas drawer (see layout.css). This module owns that
// state: one class on .body, mirrored onto the hamburger's aria-expanded.
// The drawer closes on backdrop click, Escape, or selecting a nav item —
// mirroring how views/agent.js closes its conversations panel.

function isOpen(body) {
  return body.classList.contains('is-nav-open');
}

function isMobileViewport(doc) {
  const win = doc.defaultView || globalThis.window;
  if (!win) return true;
  if (typeof win.matchMedia === 'function') return win.matchMedia('(max-width: 680px)').matches;
  return win.innerWidth <= 680;
}

function setOpen(doc, body, toggle, drawer, backdrop, value, { focusToggle = true } = {}) {
  const mobile = isMobileViewport(doc);
  const open = mobile && value;
  body.classList.toggle('is-nav-open', open);
  toggle.setAttribute('aria-expanded', String(open));
  drawer.setAttribute('aria-hidden', String(mobile ? !open : false));
  backdrop.setAttribute('aria-hidden', String(!open));
  if (open) drawer.removeAttribute('inert');
  else if (mobile) drawer.setAttribute('inert', '');
  else drawer.removeAttribute('inert');
  if (open) {
    const target = drawer.querySelector('.nav-item.active')
      || body.querySelector('.sidebar .nav-item');
    if (target) target.focus();
  } else if (focusToggle && mobile) {
    toggle.focus();
  }
}

export function initMobileNav(doc = document) {
  const body = doc.querySelector('.body');
  const toggle = doc.getElementById('mobile-nav-toggle');
  const backdrop = doc.getElementById('mobile-nav-backdrop');
  const drawer = doc.querySelector('.sidebar');
  if (!body || !toggle || !backdrop || !drawer) return null;

  const set = (value, options) => setOpen(doc, body, toggle, drawer, backdrop, value, options);

  // Normalize the initial state as well as the accessible relationship. This
  // keeps a stale class left by hot reload from exposing a hidden drawer.
  set(isOpen(body), { focusToggle: false });

  const syncViewport = () => set(isOpen(body), { focusToggle: false });
  doc.defaultView?.addEventListener('resize', syncViewport, { passive: true });

  toggle.addEventListener('click', () => set(!isOpen(body)));
  backdrop.addEventListener('click', () => set(false));
  // Delegated: selecting any sidebar nav item navigates and dismisses the
  // drawer in the same gesture.
  body.addEventListener('click', (event) => {
    if (isOpen(body) && event.target.closest?.('.sidebar [data-nav]')) set(false);
  });
  doc.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && isOpen(body)) set(false);
  });
  return {};
}
