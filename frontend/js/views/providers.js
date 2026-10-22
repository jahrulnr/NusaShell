// Provider preference helpers: cache TTL and reasoning-summary semantics shared
// by the Providers page and its tests.

export function cacheTTLsFor(provider = {}) {
  let ttls = [];
  if (Array.isArray(provider.cache_ttls) && provider.cache_ttls.length) {
    ttls = [...provider.cache_ttls];
  } else if (provider.kind === 'messages') ttls = ['5m', '1h'];
  else if (provider.kind === 'responses') ttls = ['30m'];
  else if (provider.kind === 'codex') ttls = ['30m'];
  else if (provider.driver === 'openrouter') ttls = ['5m', '1h'];
  else if (provider.kind === 'chat') ttls = ['30m'];
  if (!ttls.length) return ttls;
  return ttls.includes('off') ? ttls : [...ttls, 'off'];
}

export function effectiveCacheTTL(provider = {}) {
  const ttls = cacheTTLsFor(provider);
  if (provider.cache_ttl === 'off') return 'off';
  if (provider.cache_ttl && ttls.includes(provider.cache_ttl)) return provider.cache_ttl;
  return ttls.find((ttl) => ttl !== 'off') || '';
}

export function reasoningSummariesFor(provider = {}) {
  if (Array.isArray(provider.reasoning_summaries) && provider.reasoning_summaries.length) {
    return [...provider.reasoning_summaries];
  }
  return [];
}

export function effectiveReasoningSummary(provider = {}) {
  const summaries = reasoningSummariesFor(provider);
  if (provider.reasoning_summary && summaries.includes(provider.reasoning_summary)) {
    return provider.reasoning_summary;
  }
  return summaries.includes('auto') ? 'auto' : '';
}
