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

function setOpen(body, toggle, value) {
  body.classList.toggle('is-nav-open', value);
  toggle.setAttribute('aria-expanded', String(value));
  if (value) {
    const target = body.querySelector('.sidebar .nav-item.active')
      || body.querySelector('.sidebar .nav-item');
    if (target) target.focus();
  }
}

export function initMobileNav(doc = document) {
  const body = doc.querySelector('.body');
  const toggle = doc.getElementById('mobile-nav-toggle');
  const backdrop = doc.getElementById('mobile-nav-backdrop');
  if (!body || !toggle || !backdrop || !doc.querySelector('.sidebar')) return null;

  const set = (value) => setOpen(body, toggle, value);

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
