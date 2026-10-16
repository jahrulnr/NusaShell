const controllers = new WeakMap();

function now() {
  return Date.now();
}

function wait(duration) {
  return duration > 0 ? new Promise((resolve) => setTimeout(resolve, duration)) : Promise.resolve();
}

function createLayer(host, label) {
  const document = host?.ownerDocument || globalThis.document;
  if (!document?.createElement) return null;
  const layer = document.createElement('div');
  layer.className = 'widget-loading-layer';
  layer.setAttribute('role', 'status');
  layer.setAttribute('aria-live', 'polite');
  layer.setAttribute('aria-label', label);
  const spinner = document.createElement('span');
  spinner.className = 'widget-loading-spinner';
  spinner.setAttribute('aria-hidden', 'true');
  const copy = document.createElement('span');
  copy.className = 'widget-loading-label';
  copy.textContent = label;
  layer.append(spinner, copy);
  return layer;
}

/**
 * Coordinates a delayed, minimum-duration loading veil for an async widget.
 * The delay keeps fast markdown/highlight work from flashing; the minimum
 * duration prevents a one-frame veil when a real request completes quickly.
 * Concurrent runs share one layer, so streamed updates cannot stack overlays.
 */
export function createLoadingController(host, {
  delay = 140,
  minimum = 220,
  label = 'Loading…',
} = {}) {
  if (!host) return { run: (task) => task(), show: () => {}, hide: () => Promise.resolve(), isLoading: () => false };
  const existing = controllers.get(host);
  if (existing) return existing;

  let pending = 0;
  let timer = null;
  let layer = null;
  let visibleAt = 0;

  const clearTimer = () => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  };

  const mount = () => {
    if (layer?.isConnected) return;
    if (layer && !layer.isConnected) layer = null;
    if (pending === 0) return;
    layer = createLayer(host, label);
    if (!layer) return;
    host.classList.add('widget-loading-host');
    host.setAttribute('aria-busy', 'true');
    host.append(layer);
    visibleAt = now();
  };

  const show = () => {
    pending += 1;
    // Expose busy state immediately, even while the visual veil waits out
    // its anti-flicker delay. This keeps assistive technology in sync without
    // flashing a layer for work that completes in the same frame.
    host.setAttribute('aria-busy', 'true');
    // A widget may replace its children while a render is in flight (for
    // example, when a subagent transcript receives a fresh snapshot). That
    // detaches the veil from the DOM without clearing our reference. Treat a
    // detached layer as stale so the next run can mount a fresh one.
    if (layer?.isConnected || timer !== null) return;
    if (layer && !layer.isConnected) layer = null;
    timer = setTimeout(() => {
      timer = null;
      mount();
    }, Math.max(0, Number(delay) || 0));
  };

  const hide = async () => {
    pending = Math.max(0, pending - 1);
    if (pending > 0) return;
    clearTimer();
    if (!layer) {
      host.removeAttribute('aria-busy');
      host.classList.remove('widget-loading-host');
      return;
    }
    const remaining = Math.max(0, (Number(minimum) || 0) - (now() - visibleAt));
    if (remaining) await wait(remaining);
    if (pending > 0) return;
    layer.remove();
    layer = null;
    host.removeAttribute('aria-busy');
    host.classList.remove('widget-loading-host');
  };

  const controller = {
    run: async (task) => {
      show();
      try {
        return await task();
      } finally {
        await hide();
      }
    },
    show,
    hide,
    isLoading: () => pending > 0,
  };
  controllers.set(host, controller);
  return controller;
}

export function hidePageLoading(element = globalThis.document?.querySelector?.('#page-loading')) {
  if (!element) return;
  element.setAttribute('aria-busy', 'false');
  element.classList.add('is-hidden');
  const remove = () => element.remove();
  element.addEventListener?.('transitionend', remove, { once: true });
  setTimeout(remove, 280);
}
