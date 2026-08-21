// Shared UI helpers: toasts, dialogs, time formatting, DOM shortcuts.

import SlimSelect from '../vendor/slim-select/slimselect.es.js';

export { SlimSelect };

export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k === 'html') node.innerHTML = v;
    else if (k === 'dataset' && typeof v === 'object') Object.assign(node.dataset, v);
    else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2), v);
    else if (typeof v === 'boolean' && k in node) node[k] = v;
    else if (v !== undefined && v !== null) node.setAttribute(k, v);
  }
  for (const child of children.flat()) {
    if (child == null) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return node;
}

export function fmtTime(iso) {
  const d = new Date(iso);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  if (sameDay) return time;
  return `${d.toLocaleDateString([], { month: 'short', day: 'numeric' })} ${time}`;
}

export function fmtClock(iso) {
  return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function timeAgo(iso) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export function toast(message, kind = 'info', timeout = 4000) {
  const container = document.getElementById('toast-container');
  const dismiss = el('button', { class: 'toast-dismiss', text: '×', title: 'Dismiss', 'aria-label': 'Dismiss' });
  const node = el('div', { class: `toast toast-${kind}`, role: 'status' },
    el('span', { class: 'toast-message', text: message }),
    dismiss,
  );
  container.append(node);
  requestAnimationFrame(() => node.classList.add('toast-show'));
  let timer;
  let remaining = timeout;
  let startedAt = Date.now();
  const remove = () => {
    clearTimeout(timer);
    timer = null;
    node.classList.remove('toast-show');
    setTimeout(() => node.remove(), 350);
  };
  const arm = () => {
    clearTimeout(timer);
    startedAt = Date.now();
    timer = setTimeout(remove, Math.max(0, remaining));
  };
  arm();
  node.addEventListener('mouseenter', () => {
    if (timer == null) return;
    clearTimeout(timer);
    timer = null;
    remaining -= Date.now() - startedAt;
  });
  node.addEventListener('mouseleave', () => {
    if (timer != null) return;
    arm();
  });
  dismiss.addEventListener('click', remove);
  return remove;
}

export function createSelect(selectEl, { data = [], value = '', placeholder = '', search = null, onChange } = {}) {
  const ss = new SlimSelect({
    select: selectEl,
    data,
    settings: {
      showSearch: search ?? data.length > 7,
      placeholderText: placeholder,
      contentPosition: 'absolute',
      closeOnSelect: true,
    },
    events: {
      afterChange: (selected) => {
        if (onChange) onChange(selected[0]?.value ?? '');
      },
    },
  });
  if (value !== undefined && value !== '') ss.setSelected([value]);
  return ss;
}

// Open modal dialogs register a dismiss fn here so navigation (or any global
// teardown) can close them and resolve their promise as cancelled — otherwise
// a body-level overlay left open would cover the next view.
const openDialogDismissers = new Set();

// dismissOpenDialogs cancels and removes every open dialog()/confirmDialog()
// overlay. Called by the router on view change to prevent overlay bleed.
export function dismissOpenDialogs() {
  for (const dismiss of [...openDialogDismissers]) dismiss();
}

function dialogFocusables(root) {
  return [...root.querySelectorAll('button, [href], input, textarea, select, [tabindex]:not([tabindex="-1"])')]
    .filter((node) => !node.disabled && node.getAttribute('aria-hidden') !== 'true');
}

export function dialog({ title, message, fields = [], actions = [{ label: 'Cancel', value: null }], danger = false }) {
  return new Promise((resolve) => {
    const overlay = el('div', { class: 'ui-dialog-overlay' });
    const titleId = `ui-dialog-title-${Math.random().toString(36).slice(2, 9)}`;
    const slimInstances = [];
    const settle = (result) => {
      if (!openDialogDismissers.has(dismiss)) return;
      openDialogDismissers.delete(dismiss);
      document.removeEventListener('keydown', onKey, true);
      for (const ss of slimInstances) {
        try { ss.destroy?.(); } catch { /* already gone */ }
      }
      overlay.remove();
      resolve(result);
    };
    const dismiss = () => settle({ value: null, fields: {} });
    const onKey = (event) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        dismiss();
        return;
      }
      if (event.key !== 'Tab') return;
      const nodes = dialogFocusables(overlay);
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
    openDialogDismissers.add(dismiss);
    const body = el('div', { class: 'ui-dialog-body' });
    if (message) body.append(el('p', { class: 'ui-dialog-message', text: message }));
    const values = {};
    for (const field of fields) {
      let input;
      if (field.tag === 'textarea') {
        input = el('textarea', { placeholder: field.placeholder ?? '', rows: field.rows ?? 3 });
        input.value = field.value ?? '';
      } else if (field.tag === 'select') {
        input = el('select', { class: 'slim-select', disabled: field.disabled === true });
      } else {
        input = el('input', { type: field.type ?? 'text', placeholder: field.placeholder ?? '', value: field.value ?? '' });
      }
      values[field.name] = input;
      if (field.tag !== 'select' && typeof field.onChange === 'function') {
        input.addEventListener('change', () => field.onChange(input, values));
      }
      // Append the field to the DOM BEFORE initializing SlimSelect.
      // SlimSelect inserts .ss-main as a sibling of the <select> via
      // parentNode.insertBefore — if the select is detached (no parent),
      // .ss-main is never attached and the field appears empty.
      const fieldEl = el('label', { class: 'ui-dialog-field' }, field.label, input);
      body.append(fieldEl);
      if (field.tag === 'select') {
        const data = (field.options ?? []).map((o) => {
          const opt = typeof o === 'object' ? o : { value: o, label: o };
          return { text: opt.label, value: opt.value };
        });
        const selected = field.value ?? data[0]?.value ?? '';
        const ss = new SlimSelect({
          select: input,
          data,
          settings: {
            showSearch: data.length > 7,
            placeholderText: field.placeholder ?? '',
            contentPosition: 'absolute',
            closeOnSelect: true,
          },
          events: {
            afterChange: () => {
              if (typeof field.onChange === 'function') field.onChange(input, values);
            },
          },
        });
        if (selected) ss.setSelected([selected]);
        slimInstances.push(ss);
      }
    }
    const actionBtns = actions.map((a) => {
      const btn = el('button', {
        type: 'button',
        class: `mini-btn ${a.danger || (danger && a.value !== null) ? 'danger' : ''} ${a.primary ? '' : 'ghost'}`,
        text: a.label,
      });
      btn.addEventListener('click', () => {
        settle({ value: a.value, fields: Object.fromEntries(Object.entries(values).map(([k, n]) => [k, n.value])) });
      });
      return btn;
    });
    const dialogNode = el('div', {
      class: 'ui-dialog',
      role: 'dialog',
      'aria-modal': 'true',
      'aria-labelledby': titleId,
    },
      el('div', { class: 'ui-dialog-header' },
        el('h2', { id: titleId, text: title }),
        el('button', { class: 'ui-dialog-close', type: 'button', text: '×', 'aria-label': 'Close' }),
      ),
      body,
      el('div', { class: 'ui-dialog-actions' }, actionBtns),
    );
    dialogNode.tabIndex = -1;
    dialogNode.querySelector('.ui-dialog-close').addEventListener('click', () => dismiss());
    overlay.addEventListener('mousedown', (e) => {
      if (e.target === overlay) dismiss();
    });
    overlay.append(dialogNode);
    document.body.append(overlay);
    document.addEventListener('keydown', onKey, true);
    const first = overlay.querySelector('input, textarea, .ss-main')
      || dialogFocusables(dialogNode)[0]
      || dialogNode;
    setTimeout(() => first.focus(), 30);
  });
}

export async function confirmDialog(title, message, confirmLabel = 'Delete', danger = true) {
  const res = await dialog({
    title,
    message,
    actions: [
      { label: 'Cancel', value: null },
      { label: confirmLabel, value: true, danger },
    ],
  });
  return res.value === true;
}

export function icon(svg) {
  const wrap = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  wrap.setAttribute('viewBox', svg.viewBox || '0 0 24 24');
  wrap.setAttribute('width', String(svg.width || 14));
  wrap.setAttribute('height', String(svg.height || 14));
  wrap.setAttribute('fill', 'none');
  if (svg.paths) for (const d of svg.paths) {
    const p = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    p.setAttribute('d', d);
    p.setAttribute('stroke', 'currentColor');
    p.setAttribute('stroke-width', svg.strokeWidth || '1.6');
    p.setAttribute('stroke-linecap', 'round');
    p.setAttribute('stroke-linejoin', 'round');
    wrap.append(p);
  }
  return wrap;
}

export function debounce(fn, ms = 250) {
  let t;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...args), ms);
  };
}
