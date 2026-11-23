// Which side of its button a popover opens on. A popover opens on the side its
// caller prefers, which is where it normally has the most room (above the
// composer, below a menu button). Only when the content does not fit there, and
// the other side is clearly better, does it open on the other — a small menu
// that fits stays where the caller put it, and a tall panel is not squeezed
// into the smaller gap.
const WORTH_FLIPPING = 40; // how much more room the other side must offer, in px

// chooseSide answers 'top' or 'bottom'. above and below are the free room on
// each side of the button and need is the content's natural height, all in px.
export function chooseSide(prefer, above, below, need) {
  const room = { top: above, bottom: below };
  const other = prefer === 'top' ? 'bottom' : 'top';
  if (room[prefer] >= need) return prefer;
  return room[other] >= need || room[other] >= room[prefer] + WORTH_FLIPPING ? other : prefer;
}
