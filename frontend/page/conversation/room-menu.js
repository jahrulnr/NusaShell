// Room action menu — the single local menu shared by a room row's ellipsis
// button and its contextmenu (right-click) gesture. Intentionally not a
// generic framework: one consumer (the conversation room list) supplies the
// items; this module only owns positioning, keyboard roving, dismissal, and
// focus restoration.
//
// Contract:
// - role="menu" popup with button menuitems; ArrowUp/ArrowDown/Home/End rove
//   focus, Enter/Space activate natively, Tab dismisses.
// - Tab first restores focus to the opener, then the browser's native
//   traversal continues to the next (or previous, with Shift) focusable
//   instead of restarting from <body> after the focused menuitem is removed.
// - Escape and outside pointerdown close; Escape restores focus to the
//   opener's restoreTarget, outside dismissal does not steal focus.
// - Positioned from an anchor rect (below, right-aligned, flipping up when
//   the bottom edge would clip) or a raw pointer point, then clamped inside
//   the viewport with a small margin.
// - A single menu exists at a time: opening another replaces the open one.

let openMenu = null;

function menuItemButtons(root) {
  return [...root.querySelectorAll('[role="menuitem"]')];
}

export function isRoomMenuOpenFor(anchor) {
  return Boolean(anchor) && openMenu?.anchor === anchor;
}

export function closeRoomMenu({ restoreFocus = false } = {}) {
  const menu = openMenu;
  if (!menu) return false;
  openMenu = null;
  menu.dispose();
  menu.root.remove();
  menu.onClose?.();
  if (restoreFocus) menu.restoreTarget?.focus?.({ preventScroll: true });
  return true;
}

export function openRoomActionsMenu({ doc = document, anchor = null, point = null, restoreTarget = null, items = [], onClose = null } = {}) {
  if (!doc?.body || !items.length) return null;
  closeRoomMenu();
  const win = doc.defaultView;

  const root = doc.createElement('div');
  root.className = 'room-action-menu';
  root.setAttribute('role', 'menu');
  root.setAttribute('aria-label', 'Conversation actions');

  for (const item of items) {
    const button = doc.createElement('button');
    button.type = 'button';
    button.className = `room-action-menu-item${item.danger ? ' is-danger' : ''}`;
    button.setAttribute('role', 'menuitem');
    if (item.id) button.dataset.action = item.id;
    if (item.icon) {
      const icon = doc.createElement('i');
      icon.setAttribute('data-lucide', item.icon);
      icon.setAttribute('aria-hidden', 'true');
      button.append(icon);
    }
    const label = doc.createElement('span');
    label.textContent = item.label;
    button.append(label);
    button.addEventListener('click', () => {
      closeRoomMenu();
      item.onSelect?.();
    });
    root.append(button);
  }

  root.addEventListener('keydown', (event) => {
    const buttons = menuItemButtons(root);
    const index = buttons.indexOf(doc.activeElement);
    // Escape is handled once by the document capture listener below — it
    // resolves the menu even when focus slipped outside the panel.
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const next = event.key === 'ArrowDown'
        ? (index + 1) % buttons.length
        : (index - 1 + buttons.length) % buttons.length;
      buttons[next]?.focus();
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      (event.key === 'Home' ? buttons[0] : buttons.at(-1))?.focus();
    } else if (event.key === 'Tab') {
      // Restore the opener before the default action so native Tab moves to
      // the next focusable after the trigger (Shift+Tab to the previous one)
      // rather than restarting traversal from <body>.
      closeRoomMenu({ restoreFocus: true });
    }
  });

  const onPointerDown = (event) => {
    // Ignore presses on the anchor: its click handler toggles the menu, so a
    // dismissal here would reopen instead of closing.
    if (anchor?.contains?.(event.target)) return;
    if (!root.contains(event.target)) closeRoomMenu();
  };
  const onDocumentKeydown = (event) => {
    // Escape still resolves the menu when focus slipped out of it; the panel
    // Escape handler sees a closed menu afterwards.
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      closeRoomMenu({ restoreFocus: true });
    }
  };
  const onViewportChange = () => closeRoomMenu();
  doc.addEventListener('pointerdown', onPointerDown, true);
  doc.addEventListener('keydown', onDocumentKeydown, true);
  win?.addEventListener('resize', onViewportChange);
  const dispose = () => {
    doc.removeEventListener('pointerdown', onPointerDown, true);
    doc.removeEventListener('keydown', onDocumentKeydown, true);
    win?.removeEventListener('resize', onViewportChange);
  };

  // Measure after attach, then clamp inside the viewport.
  root.style.visibility = 'hidden';
  doc.body.append(root);
  const margin = 6;
  const rect = root.getBoundingClientRect();
  const viewportW = win?.innerWidth ?? 1024;
  const viewportH = win?.innerHeight ?? 768;
  let left;
  let top;
  if (anchor) {
    const box = anchor.getBoundingClientRect();
    left = box.right - rect.width;
    top = box.bottom + 4;
    if (top + rect.height + margin > viewportH && box.top - rect.height - 4 >= margin) {
      top = box.top - rect.height - 4;
    }
  } else {
    left = point?.x ?? margin;
    top = point?.y ?? margin;
  }
  left = Math.min(Math.max(margin, left), Math.max(margin, viewportW - rect.width - margin));
  top = Math.min(Math.max(margin, top), Math.max(margin, viewportH - rect.height - margin));
  root.style.left = `${Math.round(left)}px`;
  root.style.top = `${Math.round(top)}px`;
  root.style.visibility = '';

  openMenu = {
    root,
    anchor,
    restoreTarget: restoreTarget || anchor || doc.activeElement,
    dispose,
    onClose,
  };

  win?.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  menuItemButtons(root)[0]?.focus();

  return {
    root,
    close: (options) => (openMenu?.root === root ? closeRoomMenu(options) : false),
  };
}
