const PROTOTYPE_ROUTES = new Set(['home', 'agent', 'skills', 'plugins', 'learning', 'providers', 'logs', 'automation', 'telemetry', 'settings']);

export function normalizePrototypeRoute(value, fallback = 'home') {
  const route = String(value || '').trim().replace(/^#/, '').toLowerCase();
  return PROTOTYPE_ROUTES.has(route) ? route : fallback;
}
