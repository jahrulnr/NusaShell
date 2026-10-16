// Pure provider/ACP presentation helpers. The experimental surface consumes
// only records returned by the backend; it never invents unconfigured cards.

const DRIVER_LABELS = Object.freeze({
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  openrouter: 'OpenRouter',
  gemini: 'Gemini',
  codex: 'Codex',
});

const KIND_LABELS = Object.freeze({
  messages: 'Messages',
  responses: 'Responses',
  chat: 'Chat',
  gemini: 'Gemini',
  codex: 'Codex',
});

export const PROVIDER_PROFILES = Object.freeze([
  { value: 'automatic-chat', label: 'Automatic · Chat', driver: '', kind: 'chat', baseURL: '' },
  { value: 'automatic-responses', label: 'Automatic · Responses', driver: '', kind: 'responses', baseURL: '' },
  { value: 'automatic-messages', label: 'Automatic · Messages', driver: '', kind: 'messages', baseURL: '' },
  { value: 'anthropic', label: 'Anthropic · Messages', driver: 'anthropic', kind: 'messages', baseURL: 'https://api.anthropic.com' },
  { value: 'openai', label: 'OpenAI · Responses', driver: 'openai', kind: 'responses', baseURL: 'https://api.openai.com/v1' },
  { value: 'openrouter', label: 'OpenRouter · Chat', driver: 'openrouter', kind: 'chat', baseURL: 'https://openrouter.ai/api/v1' },
  { value: 'gemini', label: 'Gemini · native API', driver: 'gemini', kind: 'gemini', baseURL: 'https://generativelanguage.googleapis.com' },
  { value: 'codex', label: 'Codex · ChatGPT OAuth', driver: 'codex', kind: 'codex', baseURL: 'https://chatgpt.com/backend-api/codex' },
  { value: 'custom-chat', label: 'Custom gateway · Chat', driver: 'openrouter', kind: 'chat', baseURL: 'https://gateway.example/v1' },
  { value: 'custom-responses', label: 'Custom gateway · Responses', driver: 'openrouter', kind: 'responses', baseURL: 'https://gateway.example/v1' },
  { value: 'custom-messages', label: 'Custom gateway · Messages', driver: 'openrouter', kind: 'messages', baseURL: 'https://gateway.example' },
]);

export function providerDriverLabel(provider) {
  const driver = String(provider?.driver || '').trim().toLowerCase();
  return DRIVER_LABELS[driver] || (driver ? driver : 'Automatic');
}

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

export function providerProfile(provider) {
  const driver = String(provider?.driver || '').trim().toLowerCase();
  const kind = String(provider?.kind || '').trim().toLowerCase();
  if (!driver && !kind) return PROVIDER_PROFILES.find((profile) => profile.value === 'openrouter') || PROVIDER_PROFILES[0];
  const exact = PROVIDER_PROFILES.find((profile) => profile.driver === driver && profile.kind === kind);
  if (exact && exact.value !== 'custom-chat' && exact.value !== 'custom-responses' && exact.value !== 'custom-messages') return exact;
  return PROVIDER_PROFILES.find((profile) => profile.value === `custom-${kind}`) || PROVIDER_PROFILES[0];
}

export function providerProfileByValue(value) {
  return PROVIDER_PROFILES.find((profile) => profile.value === value) || PROVIDER_PROFILES[0];
}

export function filterProviders(providers = [], query = '', filter = 'all') {
  const normalizedQuery = String(query || '').trim().toLocaleLowerCase();
  const normalizedFilter = String(filter || 'all').trim().toLocaleLowerCase();
  return (Array.isArray(providers) ? providers : []).filter((provider) => {
    const tone = providerStatusTone(provider);
    if (normalizedFilter !== 'all' && normalizedFilter !== tone) return false;
    if (!normalizedQuery) return true;
    return [
      provider?.id, provider?.name, provider?.driver, provider?.kind,
      provider?.base_url, providerKindLabel(provider), providerDriverLabel(provider),
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
