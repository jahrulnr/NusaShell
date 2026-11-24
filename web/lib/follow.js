import { useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';

// BACK is how close to the bottom counts as being there: a reader who stops
// this near it is following again.
const BACK = 12;

// useFollow keeps the scroller in ref pinned to its bottom while its content
// grows — on every render and on any resize (late Markdown, images) — and
// lets the reader leave at once. Waiting for the scroll event to judge the
// distance from the bottom races the next delta: a small step up (trackpad,
// arrow key, a smooth wheel tick still animating) is under the threshold,
// or a pin lands before the event and cancels it. So upward intent (wheel,
// touch drag, scroll keys, the scrollbar) releases follow synchronously, and
// scrolling back down to the bottom rejoins.
//
// A scroll nobody asked for judges by where it ended, never by how far it
// moved: a pin and the browser's own scroll anchoring both write positions
// this handler sees later and out of order, so a small upward step is not a
// reader leaving — it is layout settling under a reader who is still at the
// bottom. Only a position clearly away from the bottom releases (and the
// chip that offers to come back follows that).
//
// key resets follow (a different thread starts at its bottom); enabled
// turns pinning off without losing the reader's choice. It answers
// { away, jump }: away is true while the reader has left the bottom, jump
// returns there and follows again.
export function useFollow(ref, { threshold = 80, key, enabled = true } = {}) {
  const s = useRef({ stick: true, key, enabled }).current;
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
    // Leaving needs somewhere to go: at the top, or with nothing to scroll,
    // an upward gesture moves nothing and must not stop follow.
    const release = () => {
      if (el.scrollTop > 0 && el.scrollHeight > el.clientHeight + 1) setStick(false);
    };
    const onScroll = () => {
      const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
      if (dist <= BACK) setStick(true);
      else if (dist > threshold) setStick(false);
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
    // client area — and only when it has one.
    const onPointer = (e) => { if (e.target === el && el.offsetWidth - el.clientWidth > 1 && e.offsetX >= el.clientWidth) release(); };
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
