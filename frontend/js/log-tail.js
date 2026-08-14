// Pure log-tail helpers shared by the Logs view and its unit tests.

export function entriesAfter(visible, lastRenderedId) {
  if (!lastRenderedId) return visible;
  const start = visible.findIndex((entry) => entry.id === lastRenderedId);
  if (start < 0) return visible;
  return visible.slice(start + 1);
}
