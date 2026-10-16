export function transcriptWindow(total, visibleCount, maximum, endAt = total) {
  const eventTotal = Math.max(0, Math.floor(Number(total) || 0));
  const end = Math.min(eventTotal, Math.max(0, Math.floor(Number(endAt) || 0)));
  const cap = Math.max(1, Math.floor(Number(maximum) || 1));
  const visible = Math.min(end, cap, Math.max(1, Math.floor(Number(visibleCount) || 1)));
  const start = Math.max(0, end - visible);
  return { start, end, hidden: start };
}

export function shiftWindowOlder(total, current, batch, maximum) {
  const eventTotal = Math.max(0, Math.floor(Number(total) || 0));
  const cap = Math.max(1, Math.floor(Number(maximum) || 1));
  const step = Math.max(1, Math.floor(Number(batch) || 1));
  const currentEnd = Math.min(eventTotal, Math.max(0, Math.floor(Number(current?.end) || eventTotal)));
  const end = Math.max(Math.min(cap, eventTotal), currentEnd - step);
  const start = Math.max(0, end - cap);
  return { start, end, hidden: start, newer: eventTotal - end };
}

export function growVisibleCount(total, current, batch, maximum) {
  const next = Math.max(1, Math.floor(Number(current) || 1))
    + Math.max(1, Math.floor(Number(batch) || 1));
  return Math.min(Math.max(0, Math.floor(Number(total) || 0)), next, Math.max(1, Math.floor(Number(maximum) || 1)));
}

export function appendVisibleCount(total, current, maximum) {
  return Math.min(
    Math.max(0, Math.floor(Number(total) || 0)),
    Math.max(1, Math.floor(Number(current) || 1)) + 1,
    Math.max(1, Math.floor(Number(maximum) || 1)),
  );
}

// Leave a finished round where the host already sits. Later siblings, such as
// a steer bubble appended after the host, stay below that round.
export function commitLiveHost(host) {
  if (!host) return;
  const children = [...host.querySelectorAll(':scope > [data-event-id]')];
  if (children.length) host.replaceWith(...children);
  else host.remove();
}

// Mount a transcript widget at the tail of the post-host chain: directly
// after a connected live-turn host when it is first, or after the widgets
// already anchored there so several applied steers keep applied order. When
// the host is not connected yet, the widget stays at the stream tail so
// attachLiveHost can insert the host above it.
export function mountWidgetAfterLiveHost(stream, host, event, resolveTag, maximum) {
  const widget = upsertVisibleWidget(stream, event, resolveTag, maximum);
  if (!widget) return widget;
  widget.dataset.afterLiveHost = '1';
  if (!host?.isConnected || !host.parentNode || widget.parentNode !== host.parentNode) return widget;
  let after = host;
  for (let node = host.nextSibling; node?.dataset?.afterLiveHost; node = node.nextSibling) {
    if (node !== widget) after = node;
  }
  if (widget.previousSibling !== after) host.parentNode.insertBefore(widget, after.nextSibling);
  return widget;
}

// Insert a new live-turn host at the tail, or immediately before an applied
// steer that was mounted while this host did not exist yet.
export function attachLiveHost(stream, host, beforeWidget = null) {
  if (!stream || !host) return host;
  if (beforeWidget?.parentNode === stream && beforeWidget !== host) {
    stream.insertBefore(host, beforeWidget);
    return host;
  }
  if (!host.isConnected) stream.append(host);
  return host;
}

export function upsertVisibleWidget(stream, event, resolveTag, maximum) {
  if (!stream || !event?.id) return null;
  const escaped = globalThis.CSS?.escape ? globalThis.CSS.escape(event.id) : String(event.id).replace(/["\\]/g, '\\$&');
  let widget = stream.querySelector(`[data-event-id="${escaped}"]`);
  if (!widget) {
    widget = stream.ownerDocument.createElement(resolveTag(event.type, event));
    widget.dataset.eventId = event.id;
    stream.append(widget);
  }
  widget.event = event;

  const limit = Math.max(1, Math.floor(Number(maximum) || 1));
  const mounted = [...stream.querySelectorAll('[data-event-id]')];
  for (const stale of mounted.slice(0, Math.max(0, mounted.length - limit))) stale.remove();
  return widget;
}

// Reconcile a window without replacing widgets that are already mounted. This
// is used when paging toward older events: moving the existing custom-element
// nodes keeps their shadow roots, loaded styles, and scroll geometry intact,
// so a large tool-heavy transcript does not flash while the new head mounts.
// Only direct children are reconciled; the live turn host owns its widgets.
export function reconcileVisibleWidgets(stream, visible, resolveTag) {
  if (!stream || !Array.isArray(visible)) return [];
  const existing = new Map([...stream.querySelectorAll(':scope > [data-event-id]')]
    .map((widget) => [widget.dataset.eventId, widget]));
  const keep = new Set();
  let cursor = stream.querySelector('.load-older-events')?.nextSibling || stream.firstChild;
  const mounted = [];

  for (const event of visible) {
    if (!event?.id) continue;
    let widget = existing.get(event.id);
    if (!widget) {
      widget = stream.ownerDocument.createElement(resolveTag(event.type, event));
      widget.dataset.eventId = event.id;
    }
    widget.event = event;
    keep.add(event.id);
    if (widget !== cursor) stream.insertBefore(widget, cursor);
    cursor = widget.nextSibling;
    mounted.push(widget);
  }

  for (const [id, widget] of existing) {
    if (!keep.has(id)) widget.remove();
  }
  return mounted;
}
