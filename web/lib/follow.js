import { useLayoutEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';

// BACK is how close to the bottom counts as being there: a reader who comes
// back this near it is following again.
export const BACK = 12;

// The follow decision, kept out of the DOM so its rules can be read — and
// tested — on their own. It answers one question: does the pin own the
// position right now?
//
// Leaving is the reader's alone (a wheel step up, a touch drag down, a scroll
// key, the scrollbar), and so is coming back. Nothing this page does may
// re-arm the pin by itself: not a position a pin wrote, not a prepend that was
// restored, not a contact area that changed under the reader (a keyboard
// closing, a shorter window). Only the reader's own gesture, in the direction
// of the bottom, may. Without that rule a slow scroll loses: every step lands
// within BACK of the bottom, the scroll event reads that as "back at the
// bottom", and the next delta pins the line away from the hand scrolling it.
export function newFollow({ threshold = 80 } = {}) {
  return { stick: true, held: false, dir: 0, at: -1, wrote: -1, geo: -1, tick: 0, threshold };
}

const move = (f, to) => {
  if (f.stick === to) return false;
  f.stick = to;
  return true;
};

// followAway is a gesture that leaves the bottom.
export function followAway(f) {
  f.dir = -1;
  f.at = ++f.tick;
  return move(f, false);
}

// followToward is a gesture heading for the bottom; whether it arrives is the
// next scroll event's business.
export function followToward(f) {
  f.dir = 1;
  f.at = ++f.tick;
}

// followScrolled judges a scroll event by where it landed, and by who moved
// it: returning needs a reader's downward gesture newer than the last position
// this page wrote and newer than the last time the geometry moved.
export function followScrolled(f, dist) {
  if (dist > f.threshold) return move(f, false);
  if (dist <= BACK && f.dir > 0 && f.at > f.wrote && f.at > f.geo) return move(f, true);
  return false;
}

// followWrote marks a position this page set.
export function followWrote(f) {
  f.wrote = ++f.tick;
}

// followResized marks the contact area changing under the reader.
export function followResized(f) {
  f.geo = ++f.tick;
}

// followHeld marks a finger or a button down on the transcript: a pin must
// not pull the line out from under it.
export function followHeld(f, held) {
  f.held = held;
  if (held) followToward(f);
}

// followJump is the chip: the reader asked for the bottom.
export function followJump(f) {
  f.dir = 1;
  f.at = ++f.tick;
  return move(f, true);
}

// useFollow keeps the scroller in ref pinned to its bottom while its content
// grows — on every render and on any resize (late Markdown, images) — and
// lets the reader leave at once. Waiting for the scroll event to judge the
// distance from the bottom races the next delta: a small step up (trackpad,
// arrow key, a smooth wheel tick still animating) is under the threshold.
// So upward intent releases follow synchronously, and the rules above keep
// the pin from taking the position back.
//
// key resets follow (a different thread starts at its bottom); enabled
// turns pinning off without losing the reader's choice. It answers
// { away, jump }: away is true while the reader has left the bottom, jump
// returns there and follows again.
export function useFollow(ref, { threshold = 80, key, enabled = true } = {}) {
  const model = useRef(null);
  if (!model.current) model.current = newFollow({ threshold });
  const f = model.current;
  f.enabled = enabled;
  const [away, setAway] = useState(!f.stick);
  const sync = (changed) => { if (changed) setAway(!f.stick); };
  const pin = () => {
    const el = ref.current;
    if (!el || !f.enabled || !f.stick || f.held) return;
    const bottom = el.scrollHeight - el.clientHeight;
    if (el.scrollTop >= bottom - 1) return;
    followWrote(f);
    el.scrollTop = bottom;
  };

  useLayoutEffect(() => {
    if (f.key !== key) {
      f.key = key;
      sync(followJump(f));
    }
    pin();
  });

  const el = ref.current;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const dist = () => el.scrollHeight - el.scrollTop - el.clientHeight;
    // Leaving needs somewhere to go: at the top, or with nothing to scroll,
    // an upward gesture moves nothing and must not stop follow.
    const release = () => {
      if (el.scrollTop > 0 && el.scrollHeight > el.clientHeight + 1) sync(followAway(f));
    };
    // A gesture that heads for the bottom counts at once when the bottom is
    // already in reach — a wheel tick down at the very bottom moves nothing,
    // so no scroll event would come to say it.
    const toward = () => {
      followToward(f);
      sync(followScrolled(f, dist()));
    };
    const onScroll = () => sync(followScrolled(f, dist()));
    const onWheel = (e) => {
      if (e.deltaY < 0) {
        // An inner scroller (a long tool output) takes the wheel first.
        for (let n = e.target; n && n !== el; n = n.parentElement) if (n.scrollTop > 0) return;
        release();
      } else if (e.deltaY > 0) {
        toward();
      }
    };
    let touchY = null;
    const onTouchStart = (e) => { touchY = e.touches[0]?.clientY ?? null; followHeld(f, true); };
    const onTouchMove = (e) => {
      const y = e.touches[0]?.clientY;
      if (touchY != null && y != null) {
        if (y > touchY) release();
        else if (y < touchY) toward();
      }
      touchY = y ?? null;
    };
    const onTouchEnd = () => { followHeld(f, false); pin(); };
    // Scroll keys reach the scroller from a focused descendant or, after a
    // click on its text, from the body.
    const onKey = (e) => {
      const t = e.target;
      if (t !== document.body && !el.contains(t)) return;
      if (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName)) return;
      if (e.key === 'ArrowUp' || e.key === 'PageUp' || e.key === 'Home' || (e.key === ' ' && e.shiftKey)) release();
      else if (e.key === 'ArrowDown' || e.key === 'PageDown' || e.key === 'End' || e.key === ' ') toward();
    };
    const onDown = (e) => {
      followHeld(f, true);
      // A press on the scrollbar lands on the scroller itself, right of its
      // client area — and only when it has one.
      if (e.target === el && el.offsetWidth - el.clientWidth > 1 && e.offsetX >= el.clientWidth) release();
    };
    const onUp = () => { followHeld(f, false); pin(); };
    const ro = new ResizeObserver((entries) => {
      // The contact area moving under the reader (a keyboard, a shorter
      // window) is not the reader coming back to the bottom.
      if (entries.some((e) => e.target === el)) followResized(f);
      pin();
    });
    ro.observe(el);
    if (el.firstElementChild) ro.observe(el.firstElementChild);
    el.addEventListener('scroll', onScroll, { passive: true });
    el.addEventListener('wheel', onWheel, { passive: true });
    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: true });
    el.addEventListener('touchend', onTouchEnd, { passive: true });
    el.addEventListener('touchcancel', onTouchEnd, { passive: true });
    el.addEventListener('pointerdown', onDown);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
    window.addEventListener('keydown', onKey);
    return () => {
      ro.disconnect();
      el.removeEventListener('scroll', onScroll);
      el.removeEventListener('wheel', onWheel);
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
      el.removeEventListener('touchend', onTouchEnd);
      el.removeEventListener('touchcancel', onTouchEnd);
      el.removeEventListener('pointerdown', onDown);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
      window.removeEventListener('keydown', onKey);
    };
  }, [el, threshold]);

  return {
    away: away && enabled,
    jump: () => {
      sync(followJump(f));
      pin();
    },
  };
}
