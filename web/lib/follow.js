import { useEffect, useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';

// useFollow keeps the scroller in ref pinned to its bottom while its content
// grows — on every render and on any resize (late Markdown, images) — and
// lets the reader leave at once. Waiting for the scroll event to judge the
// distance from the bottom races the next delta: a small step up (trackpad,
// arrow key, a smooth wheel tick still animating) is under the threshold,
// or a pin lands before the event and cancels it. So upward intent (wheel,
// touch drag, scroll keys, the scrollbar) releases follow synchronously,
// any upward scroll not caused by a pin releases it too, and scrolling back
// down to within threshold of the bottom rejoins.
//
// key resets follow (a different thread starts at its bottom); enabled
// turns pinning off without losing the reader's choice. It answers
// { away, jump }: away is true while the reader has left the bottom, jump
// returns there and follows again.
export function useFollow(ref, { threshold = 80, key, enabled = true } = {}) {
  const s = useRef({ stick: true, top: 0, pinned: -1, key, enabled }).current;
  s.enabled = enabled;
  const [away, setAway] = useState(false);
  const setStick = (v) => {
    if (s.stick === v) return;
    s.stick = v;
    setAway(!v);
  };
  const pin = () => {
    const el = ref.current;
    if (!el || !s.enabled || !s.stick) return;
    el.scrollTop = el.scrollHeight;
    s.pinned = s.top = el.scrollTop;
  };

  useLayoutEffect(() => {
    if (s.key !== key) {
      s.key = key;
      setStick(true);
    }
    pin();
  });

  const el = ref.current;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    s.top = el.scrollTop;
    // Leaving needs somewhere to go: at the top, or with nothing to scroll,
    // an upward gesture moves nothing and must not stop follow.
    const release = () => {
      if (el.scrollTop > 0 && el.scrollHeight > el.clientHeight + 1) setStick(false);
    };
    const onScroll = () => {
      const top = el.scrollTop;
      const dist = el.scrollHeight - top - el.clientHeight;
      if (top !== s.pinned) {
        // Shrinking content clamps scrollTop upward while still at the
        // bottom; only a move that leaves the bottom counts as leaving.
        if (top < s.top && dist > 1) setStick(false);
        else if (top > s.top && dist < threshold) setStick(true);
      }
      s.pinned = -1;
      s.top = top;
    };
    const onWheel = (e) => {
      if (e.deltaY >= 0) return;
      // An inner scroller (a long tool output) takes the wheel first.
      for (let n = e.target; n && n !== el; n = n.parentElement) if (n.scrollTop > 0) return;
      release();
    };
    let touchY = null;
    const onTouchStart = (e) => { touchY = e.touches[0]?.clientY ?? null; };
    const onTouchMove = (e) => {
      const y = e.touches[0]?.clientY;
      if (touchY != null && y > touchY) release();
      touchY = y ?? null;
    };
    // Scroll keys reach the scroller from a focused descendant or, after a
    // click on its text, from the body.
    const onKey = (e) => {
      const t = e.target;
      if (t !== document.body && !el.contains(t)) return;
      if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
      if (e.key === 'ArrowUp' || e.key === 'PageUp' || e.key === 'Home' || (e.key === ' ' && e.shiftKey)) release();
    };
    // A press on the scrollbar lands on the scroller itself, right of its
    // client area.
    const onPointer = (e) => { if (e.target === el && e.offsetX >= el.clientWidth) release(); };
    const ro = new ResizeObserver(pin);
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    el.addEventListener('scroll', onScroll, { passive: true });
    el.addEventListener('wheel', onWheel, { passive: true });
    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: true });
    el.addEventListener('pointerdown', onPointer);
    window.addEventListener('keydown', onKey);
    return () => {
      ro.disconnect();
      el.removeEventListener('scroll', onScroll);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
      el.removeEventListener('pointerdown', onPointer);
      window.removeEventListener('keydown', onKey);
    };
  }, [el, threshold]);

  return {
    away: away && enabled,
    jump: () => {
      setStick(true);
      pin();
    },
  };
}
