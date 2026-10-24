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

const activeToasts = new Map();

function toastKey(message, kind) {
  return `${kind}\0${String(message ?? '')}`;
}

function toastIsLive(node) {
  return Boolean(node) && (node.isConnected || document.contains(node));
}

export function toast(message, kind = 'info', timeout = 4000) {
  const key = toastKey(message, kind);
  const existing = activeToasts.get(key);
  if (existing && toastIsLive(existing.node)) {
    existing.restart(timeout);
    return existing.remove;
  }

  const container = document.getElementById('toast-container');
  if (!container) return () => {};
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
    if (activeToasts.get(key)?.node === node) activeToasts.delete(key);
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
  const restart = (nextTimeout = timeout) => {
    remaining = nextTimeout;
    arm();
  };
  arm();
  activeToasts.set(key, { node, remove, restart });
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

function setSelectPortalExpanded(selectEl, expanded) {
  const trigger = selectEl?.nextElementSibling;
  const listId = trigger?.getAttribute('aria-controls');
  const portal = listId ? document.getElementById(listId)?.closest('.ss-content') : null;
  if (!portal) return;
  portal.setAttribute('aria-hidden', String(!expanded));
  portal.toggleAttribute('inert', !expanded);
}

function bindSelectPortalObserver(selectEl, ss) {
  const trigger = selectEl.nextElementSibling;
  const syncPortal = () => setSelectPortalExpanded(selectEl, trigger?.getAttribute('aria-expanded') === 'true');
  const observer = new MutationObserver(syncPortal);
  if (trigger) observer.observe(trigger, { attributes: true, attributeFilter: ['aria-expanded'] });
  const destroy = ss.destroy.bind(ss);
  ss.destroy = () => {
    observer.disconnect();
    destroy();
  };
  syncPortal();
}

export function createSelect(selectEl, { data = [], value = '', placeholder = '', search = null, searchFilter, onChange } = {}) {
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
      ...(searchFilter ? { searchFilter } : {}),
      afterChange: (selected) => {
        if (onChange) onChange(selected[0]?.value ?? '');
      },
    },
  });
  bindSelectPortalObserver(selectEl, ss);
  if (value !== undefined && value !== '') ss.setSelected([value]);
  return ss;
}

// Normalized entries keep the input's two shapes: `{label, options}` for
// optgroups and a bare option object for flat data (marked by label === null
// so windowData can emit it without a phantom empty optgroup label row).
function normalizeSelectGroups(data) {
  return (Array.isArray(data) ? data : []).flatMap((entry) => {
    if (entry?.options) {
      const options = entry.options.filter(Boolean);
      return options.length ? [{ label: entry.label, options }] : [];
    }
    return entry ? [{ label: null, options: [entry] }] : [];
  });
}

// SlimSelect rebuilds every option node on each setData/setSelected call,
// which freezes the main thread once a catalog reaches thousands of entries.
// This wrapper keeps the complete grouped dataset outside SlimSelect and only
// hands it a capped window: the first page of the current filter plus the
// selected options. Scrolling near the list end appends the next page, and
// the search box filters the full dataset instead of only mounted nodes.
// The internal couplings (render.content.list, store.setSelectedBy's
// no-fallback flag, the debounced events.search contract) follow the vendored
// SlimSelect implementation — re-verify them on a vendor upgrade.
export function createLazySelect(selectEl, { data = [], value = '', placeholder = '', searchFilter, onChange, pageSize = 60 } = {}) {
  const page = Math.max(10, Math.floor(Number(pageSize) || 60));
  let allData = normalizeSelectGroups(data);
  let term = '';
  let shown = page;

  const matchingGroups = (needle = term) => {
    const query = String(needle || '').trim().toLowerCase();
    const pool = query
      ? allData.map((group) => ({
          label: group.label,
          options: group.options.filter((option) => (searchFilter
            ? searchFilter(option, query)
            : String(option?.text || option?.value || '').toLowerCase().includes(query))),
        }))
      : allData;
    return pool.filter((group) => group.options.length);
  };

  const emitEntry = (group, options) => (group.label === null ? options[0] : { label: group.label, options });

  const windowData = (extraValues = []) => {
    const groups = [];
    let count = 0;
    for (const group of matchingGroups()) {
      if (count >= shown) break;
      const take = group.options.slice(0, shown - count);
      count += take.length;
      groups.push(emitEntry(group, take));
    }
    // SlimSelect falls back to the first option when setSelected cannot find
    // the wanted value, so every selected value must exist in the window even
    // when it sits beyond the current page.
    const present = new Set(groups.flatMap((entry) => (entry.options || [entry]).map((option) => String(option.value))));
    for (const wanted of extraValues.map(String).filter(Boolean)) {
      if (present.has(wanted)) continue;
      let injected = false;
      for (const group of allData) {
        const found = group.options.find((option) => String(option.value) === wanted);
        if (!found) continue;
        if (group.label === null) groups.push(found);
        else {
          const existing = groups.find((entry) => entry.label === group.label);
          if (existing) existing.options.push(found);
          else groups.push({ label: group.label, options: [found] });
        }
        injected = true;
        break;
      }
      if (!injected) groups.unshift({ text: wanted, value: wanted });
      present.add(wanted);
    }
    return groups;
  };

  const ss = new SlimSelect({
    select: selectEl,
    data: windowData(),
    settings: {
      showSearch: true,
      placeholderText: placeholder,
      contentPosition: 'absolute',
      closeOnSelect: true,
    },
    events: {
      search: (needle) => {
        term = needle;
        shown = page;
        return windowData();
      },
      afterChange: (selected) => {
        if (onChange) onChange(selected[0]?.value ?? '');
      },
    },
  });
  bindSelectPortalObserver(selectEl, ss);

  // Programmatic window updates must never go through ss.setData: it resets
  // every option's selected flag, then its single-select fallback force-picks
  // the first option and fires afterChange — silently changing the user's
  // model on a scroll. Write the store directly, reselect by value with the
  // no-fallback flag, and repaint. No afterChange fires on this path.
  function applyWindow(extraValues = []) {
    const selected = ss.store.getSelectedValues().map(String);
    ss.store.setData(windowData([...selected, ...extraValues]));
    ss.store.setSelectedBy('value', selected, true);
    const rendered = ss.store.getData();
    ss.select.updateOptions(rendered);
    ss.render.renderValues();
    ss.render.renderOptions(rendered);
  }

  // Scrolling near the rendered tail grows the window by one page; the next
  // re-render is still cheap because it only ever sees the window.
  const list = ss.render?.content?.list;
  list?.addEventListener('scroll', () => {
    if (list.scrollTop + list.clientHeight < list.scrollHeight - 32) return;
    const total = matchingGroups().reduce((sum, group) => sum + group.options.length, 0);
    if (shown >= total) return;
    shown = Math.min(total, shown + page);
    const scrollTop = list.scrollTop;
    applyWindow();
    list.scrollTop = scrollTop;
  });
  // Clearing the search input restores the unfiltered first page — SlimSelect
  // would otherwise keep rendering the last (possibly stale) search subset.
  const searchInput = ss.render?.content?.search?.input;
  searchInput?.addEventListener('input', () => {
    if (searchInput.value !== '' || !term) return;
    term = '';
    shown = page;
    applyWindow();
  });
  // Closing drops the active search term; without this the next open renders
  // the stale filtered window while the input reads empty.
  const afterClose = ss.events.afterClose;
  ss.events.afterClose = (...args) => {
    term = '';
    shown = page;
    applyWindow();
    if (typeof afterClose === 'function') afterClose(...args);
  };

  const api = {
    inner: ss,
    setData(groups) {
      allData = normalizeSelectGroups(groups);
      term = '';
      shown = page;
      // Replacing the dataset keeps SlimSelect's original semantics: the
      // selection resets and the first window option wins. The preservation
      // path (applyWindow) is only for window growth, not new data.
      ss.setData(windowData());
    },
    setSelected(values) {
      const wanted = (Array.isArray(values) ? values : [values]).filter((item) => item != null && item !== '').map(String);
      const inStore = new Set(ss.store.getDataOptions().map((option) => String(option.value)));
      if (wanted.some((item) => !inStore.has(item))) applyWindow(wanted);
      ss.setSelected(wanted);
    },
    getSelected: () => ss.getSelected(),
    queryOptions: (needle) => matchingGroups(needle).flatMap((group) => group.options),
    enable: () => ss.enable(),
    disable: () => ss.disable(),
    open: () => ss.open(),
    close: () => ss.close(),
    destroy: () => ss.destroy(),
  };
  if (value !== undefined && value !== '') api.setSelected([value]);
  return api;
}

export function bindTablistKeyboard(tablist) {
  if (!tablist) return null;
  const tabs = [...tablist.querySelectorAll('[role="tab"]')];
  if (!tabs.length) return null;

  const syncTabStops = (fallback = tabs[0]) => {
    const selected = tabs.find((tab) => tab.getAttribute('aria-selected') === 'true') || fallback;
    tabs.forEach((tab) => { tab.tabIndex = tab === selected ? 0 : -1; });
  };
  syncTabStops();

  tablist.addEventListener('click', (event) => {
    const tab = event.target.closest?.('[role="tab"]');
    if (tab && tabs.includes(tab)) syncTabStops(tab);
  });
  tablist.addEventListener('keydown', (event) => {
    const tab = event.target.closest?.('[role="tab"]');
    const index = tabs.indexOf(tab);
    if (index < 0) return;
    let nextIndex;
    if (event.key === 'ArrowRight') nextIndex = (index + 1) % tabs.length;
    else if (event.key === 'ArrowLeft') nextIndex = (index - 1 + tabs.length) % tabs.length;
    else if (event.key === 'Home') nextIndex = 0;
    else if (event.key === 'End') nextIndex = tabs.length - 1;
    else return;
    event.preventDefault();
    const next = tabs[nextIndex];
    next.click();
    syncTabStops(next);
    next.focus();
  });
  return { sync: syncTabStops };
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

// registerOverlayDismiss lets custom overlays (lightbox, etc.) close when
// the router tears down dialogs on view change. Returns an unregister fn.
export function registerOverlayDismiss(dismiss) {
  openDialogDismissers.add(dismiss);
  return () => openDialogDismissers.delete(dismiss);
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
    const selectFields = [];
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
        const ss = createSelect(input, {
          data,
          value: selected,
          placeholder: field.placeholder ?? '',
          search: data.length > 7,
          onChange: () => {
            if (typeof field.onChange === 'function') field.onChange(input, values);
          },
        });
        slimInstances.push(ss);
        selectFields.push(field);
      }
    }
    // Fire onChange once for selects after every field exists, so
    // dependent fields (e.g. visibility based on the selected value)
    // start in sync.
    for (const field of selectFields) {
      if (typeof field.onChange === 'function' && values[field.name]) {
        field.onChange(values[field.name], values);
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

export function debounce(fn, ms = 250) {
  let t;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...args), ms);
  };
}
