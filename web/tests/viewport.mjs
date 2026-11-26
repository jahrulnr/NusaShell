// inLayout runs a popover test in one screen layout: the narrow-screen query
// decides between the bottom sheet and the anchored side, and innerHeight is
// the window the cap is taken from. Every test that lays a popover out goes
// through here, so the two layouts are one definition. The geometry the test
// needs is its own; this only sets what the window answers, and puts it back.
export async function inLayout(layout, fn) {
  const hadHeight = 'innerHeight' in globalThis;
  const prevHeight = globalThis.innerHeight;
  const prevMatch = globalThis.matchMedia;
  globalThis.innerHeight = 800;
  globalThis.matchMedia = (query) => ({
    matches: layout === 'sheet' && query.includes('max-width'),
    addEventListener() {},
    removeEventListener() {},
  });
  try {
    return await fn();
  } finally {
    globalThis.matchMedia = prevMatch;
    if (hadHeight) globalThis.innerHeight = prevHeight;
    else delete globalThis.innerHeight;
  }
}
