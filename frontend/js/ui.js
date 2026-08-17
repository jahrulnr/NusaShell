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
  const node = el('div', { class: `toast toast-${kind}` },
    el('span', { class: 'toast-message', text: message }),
    dismiss,
  );
  container.append(node);
  requestAnimationFrame(() => node.classList.add('toast-show'));
  let timer;
  const remove = () => {
    clearTimeout(timer);
    node.classList.remove('toast-show');
    setTimeout(() => node.remove(), 350);
  };
  timer = setTimeout(remove, timeout);
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

export function dialog({ title, message, fields = [], actions = [{ label: 'Cancel', value: null }], danger = false }) {
  return new Promise((resolve) => {
    const overlay = el('div', { class: 'ui-dialog-overlay' });
    const body = el('div', { class: 'ui-dialog-body' });
    if (message) body.append(el('p', { class: 'ui-dialog-message', text: message }));
    const values = {};
    const slimInstances = [];
    for (const field of fields) {
      let input;
      if (field.tag === 'textarea') {
        input = el('textarea', { placeholder: field.placeholder ?? '', rows: field.rows ?? 3 });
        input.value = field.value ?? '';
      } else if (field.tag === 'select') {
        input = el('select', { class: 'slim-select', disabled: field.disabled === true });
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
        slimInstances.push(input);
      } else {
        input = el('input', { type: field.type ?? 'text', placeholder: field.placeholder ?? '', value: field.value ?? '' });
      }
      values[field.name] = input;
      if (field.tag !== 'select' && typeof field.onChange === 'function') {
        input.addEventListener('change', () => field.onChange(input, values));
      }
      body.append(el('label', { class: 'ui-dialog-field' }, field.label, input));
    }
    const actionBtns = actions.map((a) => {
      const btn = el('button', {
        class: `mini-btn ${a.danger || (danger && a.value !== null) ? 'danger' : ''} ${a.primary ? '' : 'ghost'}`,
        text: a.label,
      });
      btn.addEventListener('click', () => {
        overlay.remove();
        resolve({ value: a.value, fields: Object.fromEntries(Object.entries(values).map(([k, n]) => [k, n.value])) });
      });
      return btn;
    });
    const dialogNode = el('div', { class: 'ui-dialog' },
      el('div', { class: 'ui-dialog-header' },
        el('h2', { text: title }),
        el('button', { class: 'ui-dialog-close', text: '×', 'aria-label': 'Close' }),
      ),
      body,
      el('div', { class: 'ui-dialog-actions' }, actionBtns),
    );
    dialogNode.querySelector('.ui-dialog-close').addEventListener('click', () => {
      overlay.remove();
      resolve({ value: null, fields: {} });
    });
    overlay.addEventListener('mousedown', (e) => {
      if (e.target === overlay) {
        overlay.remove();
        resolve({ value: null, fields: {} });
      }
    });
    overlay.append(dialogNode);
    document.body.append(overlay);
    const first = overlay.querySelector('input, textarea, .ss-main');
    if (first) setTimeout(() => first.focus(), 30);
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
