// Stick-to-bottom follow for a growing transcript scroller.
//
// Live tool/reason/activity bursts change the last message's size faster
// than a 1px flex sentinel and IntersectionObserver can stay visible. The
// contract is two bits (pinned / followDetached) plus one coalesced rAF
// loop driven by ResizeObserver on the thread and its children.

export function stickThreadToBottom(thread) {
  if (!thread) return 0;
  thread.scrollTop = thread.scrollHeight;
  return thread.scrollTop;
}

function viewConstructor(thread, name, fallback) {
  if (typeof fallback === 'function') return fallback;
  return thread?.ownerDocument?.defaultView?.[name] || globalThis[name];
}

export function createThreadFollow(options = {}) {
  const getThread = options.getThread || (() => null);
  const isFollowing = options.isFollowing || (() => false);
  const onStick = options.onStick;
  const schedule = options.scheduleFrame;
  const ResizeObserverOption = options.ResizeObserver;
  const MutationObserverOption = options.MutationObserver;

  let frame = null;
  let generation = 0;
  let watching = null;
  let resizeObserver = null;
  let mutationObserver = null;

  function cancel() {
    generation += 1;
    frame?.cancel?.();
    frame = null;
  }

  function stick() {
    const thread = getThread();
    if (!thread) return;
    stickThreadToBottom(thread);
    onStick?.(thread);
  }

  function tick(expected) {
    frame = null;
    if (expected !== generation) return;
    if (!isFollowing()) return;
    const thread = getThread();
    if (!thread || (watching && thread !== watching)) return;
    stick();
  }

  function queueFrame(thread, expected) {
    const run = () => tick(expected);
    if (typeof schedule === 'function') {
      frame = schedule(thread, run);
      return;
    }
    const view = thread?.ownerDocument?.defaultView || globalThis;
    const raf = view.requestAnimationFrame;
    if (typeof raf === 'function') {
      const id = raf.call(view, run);
      frame = {
        cancel() {
          const cancelRaf = view.cancelAnimationFrame || globalThis.cancelAnimationFrame;
          if (typeof cancelRaf === 'function') cancelRaf.call(view, id);
        },
      };
      return;
    }
    const id = setTimeout(run, 0);
    frame = { cancel: () => clearTimeout(id) };
  }

  function nudge() {
    if (!isFollowing()) return;
    const thread = getThread();
    if (!thread) return;
    if (watching && thread !== watching) attach(thread);
    if (frame) return;
    queueFrame(thread, generation);
  }

  function observeContent(thread) {
    if (!resizeObserver || !thread) return;
    resizeObserver.disconnect();
    resizeObserver.observe(thread);
    for (const child of thread.children) resizeObserver.observe(child);
  }

  function attach(thread) {
    if (!thread) {
      detach();
      return;
    }
    if (watching === thread && resizeObserver) {
      observeContent(thread);
      nudge();
      return;
    }
    detach();
    watching = thread;
    const ResizeObserverCtor = viewConstructor(thread, 'ResizeObserver', ResizeObserverOption);
    const MutationObserverCtor = viewConstructor(thread, 'MutationObserver', MutationObserverOption);
    if (typeof ResizeObserverCtor === 'function') {
      resizeObserver = new ResizeObserverCtor(() => nudge());
      observeContent(thread);
    }
    if (typeof MutationObserverCtor === 'function') {
      mutationObserver = new MutationObserverCtor(() => {
        if (getThread() !== thread) return;
        observeContent(thread);
        nudge();
      });
      mutationObserver.observe(thread, { childList: true });
    }
    nudge();
  }

  function detach() {
    cancel();
    resizeObserver?.disconnect?.();
    mutationObserver?.disconnect?.();
    resizeObserver = null;
    mutationObserver = null;
    watching = null;
  }

  return { attach, detach, cancel, nudge, stick };
}
