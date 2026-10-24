// Pure provider/ACP presentation helpers. The experimental surface consumes
// only records returned by the backend; it never invents unconfigured cards.

const KIND_LABELS = Object.freeze({
  messages: 'Messages',
  responses: 'Responses',
  chat: 'Chat',
  gemini: 'Gemini',
  codex: 'Codex',
});

export const PROVIDER_PROTOCOLS = Object.freeze([
  { value: 'chat', label: 'Chat', kind: 'chat', baseURL: 'https://openrouter.ai/api/v1' },
  { value: 'responses', label: 'Responses', kind: 'responses', baseURL: 'https://api.openai.com/v1' },
  { value: 'messages', label: 'Messages', kind: 'messages', baseURL: 'https://api.anthropic.com' },
  { value: 'gemini', label: 'Gemini', kind: 'gemini', baseURL: 'https://generativelanguage.googleapis.com' },
  { value: 'codex', label: 'Codex · ChatGPT OAuth', kind: 'codex', baseURL: 'https://chatgpt.com/backend-api/codex' },
]);

export function providerKindLabel(provider) {
  const kind = String(provider?.kind || '').trim().toLowerCase();
  return KIND_LABELS[kind] || (kind ? kind : 'Unknown');
}

export function providerStatusTone(provider, probe = '') {
  const normalizedProbe = String(probe || '').trim().toLowerCase();
  if (normalizedProbe === 'testing') return 'testing';
  if (normalizedProbe === 'error' || provider?.error) return 'error';
  if (provider?.enabled === false) return 'disabled';
  if (provider?.configured === false) return 'setup';
  return 'ready';
}

export function providerStatusLabel(provider, probe = '') {
  const labels = { ready: 'Ready', setup: 'Needs setup', disabled: 'Disabled', testing: 'Testing…', error: 'Unavailable' };
  return labels[providerStatusTone(provider, probe)] || 'Ready';
}

export function providerModelsCount(provider) {
  return Array.isArray(provider?.models) ? provider.models.length : 0;
}

export function providerCredentialLabel(provider) {
  if (String(provider?.kind || '').toLowerCase() === 'codex') return provider?.has_api_key ? 'ChatGPT account linked' : 'Sign-in required';
  return provider?.has_api_key ? 'API key stored' : provider?.configured ? 'No key required' : 'API key not set';
}

export function providerProtocol(provider) {
  const kind = String(provider?.kind || '').trim().toLowerCase();
  return PROVIDER_PROTOCOLS.find((protocol) => protocol.kind === kind) || PROVIDER_PROTOCOLS[0];
}

export function providerProtocolByValue(value) {
  return PROVIDER_PROTOCOLS.find((protocol) => protocol.value === value) || PROVIDER_PROTOCOLS[0];
}

export function filterProviders(providers = [], query = '', filter = 'all') {
  const normalizedQuery = String(query || '').trim().toLocaleLowerCase();
  const normalizedFilter = String(filter || 'all').trim().toLocaleLowerCase();
  return (Array.isArray(providers) ? providers : []).filter((provider) => {
    const tone = providerStatusTone(provider);
    if (normalizedFilter !== 'all' && normalizedFilter !== tone) return false;
    if (!normalizedQuery) return true;
    return [
      provider?.id, provider?.name, provider?.kind,
      provider?.base_url, providerKindLabel(provider),
    ].filter(Boolean).join(' ').toLocaleLowerCase().includes(normalizedQuery);
  });
}

export function formatModelContext(value) {
  const context = Number(value);
  if (!Number.isFinite(context) || context <= 0) return '';
  if (context >= 1_000_000) return `${(context / 1_000_000).toFixed(context % 1_000_000 ? 1 : 0)}M ctx`;
  if (context >= 1_000) return `${Math.round(context / 1_000)}k ctx`;
  return `${context} ctx`;
}

export function filterModels(models = [], query = '') {
  const normalized = String(query || '').trim().toLocaleLowerCase();
  if (!normalized) return Array.isArray(models) ? models : [];
  return (Array.isArray(models) ? models : []).filter((model) => [model?.id, model?.display_name, model?.description, model?.kind]
    .filter(Boolean).join(' ').toLocaleLowerCase().includes(normalized));
}

export function acpAgentStatusTone(agent) {
  if (agent?.enabled === false) return 'disabled';
  if ((agent?.cached_modes || agent?.modes || []).length || (agent?.cached_models || agent?.models || []).length) return 'ready';
  return 'unprobed';
}

export function acpAgentStatusLabel(agent) {
  const labels = { ready: 'Ready', disabled: 'Disabled', unprobed: 'Not probed' };
  return labels[acpAgentStatusTone(agent)] || 'Not probed';
}

export function acpAgentModelCount(agent) {
  return (agent?.cached_models || agent?.models || []).length;
}

export function acpAgentModeCount(agent) {
  return (agent?.cached_modes || agent?.modes || []).length;
}

export function filterAcpAgents(agents = [], query = '') {
  const normalized = String(query || '').trim().toLocaleLowerCase();
  if (!normalized) return Array.isArray(agents) ? agents : [];
  return (Array.isArray(agents) ? agents : []).filter((agent) => [agent?.id, agent?.name, agent?.command, agent?.transport, agent?.preferred_model_id, agent?.preferred_mode_id]
    .filter(Boolean).join(' ').toLocaleLowerCase().includes(normalized));
}
