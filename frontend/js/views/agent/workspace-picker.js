// Workspace picker: in-app overlay that browses directories on the NusaShell
// host through `agent.workspace.list-dirs` and resolves an absolute server
// path. It replaces the old native (zenity) folder dialog, so workspace
// selection works from any device, including mobile browsers over the LAN.
//
// The overlay reuses the shared dialog chrome (ui-dialog-* from modals.css)
// and the custom-overlay lifecycle (registerOverlayDismiss) the same way
// lightboxes do; it does not use dialog() because that helper's body is a
// form, while this picker needs an async directory list, breadcrumbs, inline
// loading/error states, and a Select action that stays under its control.
import { el, registerOverlayDismiss } from '../../ui.js';
import { rpc } from '../../rpc.js';

function pathSegments(path) {
  return String(path || '').split('/').filter(Boolean);
}

export function openWorkspacePicker({ initial = '' } = {}) {
  return new Promise((resolve) => {
    const previousFocus = document.activeElement;
    let current = '';
    let entries = [];
    let loading = false;
    let pendingSelect = false;
    let unregister = null;
    let settled = false;

    const close = (value) => {
      if (settled) return;
      settled = true;
      unregister?.();
      document.removeEventListener('keydown', onKey, true);
      overlay.remove();
      // Safe focus return for overlays: hand focus back to the control that
      // opened the picker when it still exists.
      if (previousFocus && typeof previousFocus.focus === 'function' && previousFocus.isConnected) {
        previousFocus.focus();
      }
      resolve(value);
    };

    const focusables = () => [...overlay.querySelectorAll(
      'button, [href], input, textarea, select, [tabindex]:not([tabindex="-1"])',
    )].filter((node) => !node.disabled && node.getAttribute('aria-hidden') !== 'true');

    const onKey = (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        close(null);
        return;
      }
      if (event.key !== 'Tab') return;
      const nodes = focusables();
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
    };

    // ---- DOM ----
    const titleId = `workspace-picker-title-${Math.random().toString(36).slice(2, 9)}`;
    const overlay = el('div', { class: 'ui-dialog-overlay' });
    const crumbEl = el('nav', { class: 'workspace-picker-crumbs', 'aria-label': 'Current folder' });
    const pathInput = el('input', {
      type: 'text',
      class: 'workspace-picker-path',
      placeholder: '/home/user/projects',
      autocomplete: 'off',
      spellcheck: false,
      'aria-label': 'Folder path',
    });
    const goBtn = el('button', { class: 'mini-btn ghost', type: 'button', text: 'Go' });
    const listEl = el('div', { class: 'workspace-picker-list' });
    const statusEl = el('div', { class: 'workspace-picker-status', role: 'status', 'aria-live': 'polite' });
    const cancelBtn = el('button', { class: 'mini-btn ghost', type: 'button', text: 'Cancel' });
    const selectBtn = el('button', { class: 'mini-btn', type: 'button', text: 'Use this folder', disabled: true });

    const renderCrumbs = (path) => {
      crumbEl.replaceChildren();
      const root = el('button', { class: 'workspace-picker-crumb', type: 'button', text: '/', title: '/' });
      root.addEventListener('click', () => load('/'));
      crumbEl.append(root);
      const parts = pathSegments(path);
      if (!parts.length) return;
      let acc = '';
      parts.forEach((part, i) => {
        acc += `/${part}`;
        // The root crumb already renders the leading slash, so separators
        // only join the following segments ("/ home / projects").
        if (i > 0) crumbEl.append(el('span', { class: 'workspace-picker-crumb-sep', text: '/' }));
        const crumb = el('button', {
          class: `workspace-picker-crumb${i === parts.length - 1 ? ' is-current' : ''}`,
          type: 'button',
          text: part,
          title: acc,
        });
        crumb.addEventListener('click', () => load(acc));
        crumbEl.append(crumb);
      });
    };

    const renderList = () => {
      listEl.replaceChildren();
      for (const entry of entries) {
        if (!entry || !entry.path) continue;
        const name = pathSegments(entry.path).pop() || entry.name || entry.path;
        const row = el('button', {
          class: 'workspace-picker-row',
          type: 'button',
          title: entry.path,
        },
          el('span', { class: 'workspace-picker-row-icon', 'aria-hidden': true, text: '📁' }),
          el('span', { class: 'workspace-picker-row-name', text: name }),
        );
        row.addEventListener('click', () => load(entry.path));
        listEl.append(row);
      }
      listEl.setAttribute('aria-busy', 'false');
    };

    const renderStatus = (state) => {
      statusEl.replaceChildren();
      if (!state) return;
      const cls = state.kind === 'error'
        ? 'workspace-picker-status-text is-error'
        : 'workspace-picker-status-text';
      statusEl.append(el('span', { class: cls, text: state.message }));
    };

    const load = async (path, { selectAfter = false } = {}) => {
      if (loading) return;
      loading = true;
      pendingSelect = Boolean(selectAfter);
      selectBtn.disabled = true;
      listEl.setAttribute('aria-busy', 'true');
      renderStatus({ kind: 'loading', message: 'Loading…' });
      try {
        const res = await rpc('agent.workspace.list-dirs', { path: String(path ?? '').trim() });
        current = String(res.path ?? '').trim() || String(path ?? '').trim();
        entries = Array.isArray(res.entries) ? res.entries : [];
        pathInput.value = current;
        if (pendingSelect && current) {
          close(current);
          return;
        }
        renderCrumbs(current);
        renderList();
        renderStatus(entries.length ? null : { kind: 'info', message: 'No subfolders here.' });
        selectBtn.disabled = !current;
      } catch (error) {
        pendingSelect = false;
        renderList();
        renderStatus({ kind: 'error', message: error?.message || 'Could not read that folder.' });
      } finally {
        loading = false;
      }
    };

    // ---- Wiring ----
    goBtn.addEventListener('click', () => load(pathInput.value));
    pathInput.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter' || event.isComposing) return;
      event.preventDefault();
      load(pathInput.value);
    });
    selectBtn.addEventListener('click', () => {
      const typed = pathInput.value.trim();
      if (typed === current) {
        close(current);
        return;
      }
      // The user typed a path without pressing Go: load it first and resolve
      // only when the server confirms the directory, keeping the popup open
      // (with an inline error) when the path is invalid.
      load(typed, { selectAfter: true });
    });
    cancelBtn.addEventListener('click', () => close(null));
    overlay.addEventListener('mousedown', (event) => {
      if (event.target === overlay) close(null);
    });

    const dialogNode = el('div', {
      class: 'ui-dialog workspace-picker-dialog',
      role: 'dialog',
      'aria-modal': 'true',
      'aria-labelledby': titleId,
    },
      el('div', { class: 'ui-dialog-header' },
        el('h2', { id: titleId, text: 'Choose workspace folder' }),
        el('button', { class: 'ui-dialog-close', type: 'button', text: '×', 'aria-label': 'Close' }),
      ),
      el('div', { class: 'workspace-picker-body' },
        crumbEl,
        el('div', { class: 'workspace-picker-path-row' }, pathInput, goBtn),
        listEl,
        statusEl,
      ),
      el('div', { class: 'ui-dialog-actions' }, cancelBtn, selectBtn),
    );
    dialogNode.querySelector('.ui-dialog-close').addEventListener('click', () => close(null));
    overlay.append(dialogNode);
    document.body.append(overlay);
    document.addEventListener('keydown', onKey, true);
    unregister = registerOverlayDismiss(() => close(null));
    setTimeout(() => pathInput.focus(), 30);

    // Empty path means "server home directory" to the backend.
    load(initial || '', { selectAfter: false });
  });
}
