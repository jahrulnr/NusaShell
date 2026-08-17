// Settings workspace: native browser preferences plus the Go runtime controls.

import { autoReconnectEnabled, rpc, setAutoReconnect } from '../rpc.js';
import { toast, createSelect } from '../ui.js';

let bound = false;
const state = { embeddingProviderId: '', embeddingModelId: '', visionProviderId: '', visionModelId: '', audioProviderId: '', audioModelId: '', videoProviderId: '', videoModelId: '', webAnswerProvider: '', webAnswerModel: '' };
let preferredSelect;
let embeddingSelect;
let visionSelect;
let audioSelect;
let videoSelect;
let webAnswerProviderSelect;

export async function initSettings() {
  if (!bound) {
    bound = true;
    document.getElementById('settings-save-btn').addEventListener('click', save);
    document.getElementById('settings-sidebar-compact').addEventListener('change', saveSidebarPreference);
    document.getElementById('settings-auto-reconnect').addEventListener('change', saveReconnectPreference);
    document.getElementById('settings-check-connection-btn').addEventListener('click', checkConnection);
    preferredSelect = createSelect(document.getElementById('settings-preferred-model'), {
      placeholder: 'Automatic — choose in each conversation',
      search: true,
    });
    embeddingSelect = createSelect(document.getElementById('settings-embedding-model'), {
      placeholder: 'Automatic — use first available embedding model',
      search: true,
    });
    visionSelect = createSelect(document.getElementById('settings-vision-model'), {
      placeholder: 'Disabled — non-vision models get a text placeholder instead',
      search: true,
    });
    audioSelect = createSelect(document.getElementById('settings-audio-model'), {
      placeholder: 'Disabled — non-audio models get a text placeholder instead',
      search: true,
    });
    videoSelect = createSelect(document.getElementById('settings-video-model'), {
      placeholder: 'Disabled — non-video models get a text placeholder instead',
      search: true,
    });
    webAnswerProviderSelect = createSelect(document.getElementById('settings-web-answer-provider'), {
      placeholder: 'Disabled — web_answer tool is not available',
      data: [
        { text: 'Disabled — web_answer tool is not available', value: '', placeholder: true },
        { text: 'Brave (Answers API)', value: 'brave' },
        { text: 'OpenRouter (web_search server tool)', value: 'openrouter' },
        { text: 'OpenAI (Responses API web_search)', value: 'openai' },
        { text: 'Perplexity (Agent API)', value: 'perplexity' },
        { text: 'Anthropic (Messages API web_search)', value: 'anthropic' },
        { text: 'xAI (Responses API web_search)', value: 'xai' },
      ],
    });
    window.addEventListener('hashchange', () => {
      if (location.hash === '#settings') void refresh();
    });
  }
  await refresh();
}

export async function refresh() {
  const [settingsResult, infoResult, modelsResult] = await Promise.allSettled([
    rpc('settings.get'),
    rpc('app.info', {}, { timeoutMs: 4000 }),
    rpc('ai.models.list'),
  ]);

  if (settingsResult.status === 'fulfilled') {
    const { settings } = settingsResult.value;
    document.getElementById('settings-compaction-enabled').checked = settings.compaction_enabled !== false;
    document.getElementById('settings-prompt-caching').checked = settings.prompt_caching === true;
    document.getElementById('settings-max-tool-rounds').value = settings.max_tool_rounds ?? 8;
    document.getElementById('settings-max-input-tokens').value = settings.max_input_tokens ?? 200000;
    document.getElementById('settings-compaction-threshold').value = settings.compaction_threshold ?? 0;
    document.getElementById('settings-max-output-tokens').value = settings.max_output_tokens ?? 65536;
    setOptionalNumber('settings-temperature', settings.temperature);
    setOptionalNumber('settings-top-p', settings.top_p);
    setOptionalNumber('settings-top-k', settings.top_k);
    setOptionalNumber('settings-frequency-penalty', settings.frequency_penalty);
    setOptionalNumber('settings-presence-penalty', settings.presence_penalty);
    state.embeddingProviderId = settings.embedding_provider_id ?? '';
    state.embeddingModelId = settings.embedding_model_id ?? '';
    state.visionProviderId = settings.vision_provider_id ?? '';
    state.visionModelId = settings.vision_model_id ?? '';
    state.audioProviderId = settings.audio_provider_id ?? '';
    state.audioModelId = settings.audio_model_id ?? '';
    state.videoProviderId = settings.video_provider_id ?? '';
    state.videoModelId = settings.video_model_id ?? '';
    state.webAnswerProvider = settings.web_answer_provider ?? '';
    state.webAnswerModel = settings.web_answer_model ?? '';
    document.getElementById('settings-learning-threshold').value = settings.learning_review_threshold ?? 50;
    document.getElementById('settings-auto-continues').value = settings.max_auto_continues ?? 10;
    // Web answer: set provider dropdown and model field. API key is write-only.
    webAnswerProviderSelect.setSelected([state.webAnswerProvider || '']);
    document.getElementById('settings-web-answer-model').value = state.webAnswerModel;
    document.getElementById('settings-web-answer-api-key').value = '';
  } else {
    setStatus(`Could not load runtime settings: ${settingsResult.reason.message}`, true);
  }

  const allModels = modelsResult.status === 'fulfilled' ? modelsResult.value.models ?? [] : [];
  renderModelOptions(allModels);
  renderEmbeddingModelOptions(allModels);
  renderVisionModelOptions(allModels);
  renderAudioModelOptions(allModels);
  renderVideoModelOptions(allModels);
  document.getElementById('settings-sidebar-compact').checked = localStorage.getItem('nusashell.sidebarMode') === 'icons';
  document.getElementById('settings-auto-reconnect').checked = autoReconnectEnabled();

  if (infoResult.status === 'fulfilled') {
    renderAppInfo(infoResult.value);
  } else {
    setConnectionStatus('Could not reach the local backend.', true);
  }
}

function renderModelOptions(models) {
  // Only chat LLMs belong in the default model picker. Kind is enriched
  // from models.dev; treat unknown ("") as chat for backward compatibility.
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');
  const selected = localStorage.getItem('nusashell.model') || '';
  const data = [
    { text: 'Automatic — choose in each conversation', value: '', placeholder: true },
    ...chatModels.map((m) => {
      const label = m.display_name || m.id;
      const ctx = m.context ? ` ${Math.round(m.context / 1000)}K` : '';
      return {
        text: m.provider_name ? `${label}${ctx} · ${m.provider_name}` : `${label}${ctx}`,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  preferredSelect.setData(data);
  if (selected) preferredSelect.setSelected([selected]);
}

function renderEmbeddingModelOptions(models) {
  const embeddingModels = models.filter((m) => m.kind === 'embedding');
  const data = [
    { text: 'Automatic — use first available embedding model', value: '', placeholder: true },
    ...embeddingModels.map((m) => ({
      text: m.provider_name ? `${m.id} · ${m.provider_name}` : m.id,
      value: `${m.provider_id}:${m.id}`,
    })),
  ];
  embeddingSelect.setData(data);
  const selected = state.embeddingProviderId && state.embeddingModelId
    ? `${state.embeddingProviderId}:${state.embeddingModelId}`
    : '';
  if (selected) embeddingSelect.setSelected([selected]);
}

function renderVisionModelOptions(models) {
  const visionModels = models.filter((m) => m.vision === true);
  const data = [
    { text: 'Disabled — non-vision models get a text placeholder instead', value: '', placeholder: true },
    ...visionModels.map((m) => {
      const label = m.display_name || m.id;
      const ctx = m.context ? ` ${Math.round(m.context / 1000)}K` : '';
      return {
        text: m.provider_name ? `${label}${ctx} · ${m.provider_name}` : `${label}${ctx}`,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  visionSelect.setData(data);
  const selected = state.visionProviderId && state.visionModelId
    ? `${state.visionProviderId}:${state.visionModelId}`
    : '';
  if (selected) visionSelect.setSelected([selected]);
}

function renderAudioModelOptions(models) {
  const audioModels = models.filter((m) => m.audio === true);
  const data = [
    { text: 'Disabled — non-audio models get a text placeholder instead', value: '', placeholder: true },
    ...audioModels.map((m) => {
      const label = m.display_name || m.id;
      const ctx = m.context ? ` ${Math.round(m.context / 1000)}K` : '';
      return {
        text: m.provider_name ? `${label}${ctx} · ${m.provider_name}` : `${label}${ctx}`,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  audioSelect.setData(data);
  const selected = state.audioProviderId && state.audioModelId
    ? `${state.audioProviderId}:${state.audioModelId}`
    : '';
  if (selected) audioSelect.setSelected([selected]);
}

function renderVideoModelOptions(models) {
  const videoModels = models.filter((m) => m.video === true);
  const data = [
    { text: 'Disabled — non-video models get a text placeholder instead', value: '', placeholder: true },
    ...videoModels.map((m) => {
      const label = m.display_name || m.id;
      const ctx = m.context ? ` ${Math.round(m.context / 1000)}K` : '';
      return {
        text: m.provider_name ? `${label}${ctx} · ${m.provider_name}` : `${label}${ctx}`,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  videoSelect.setData(data);
  const selected = state.videoProviderId && state.videoModelId
    ? `${state.videoProviderId}:${state.videoModelId}`
    : '';
  if (selected) videoSelect.setSelected([selected]);
}

// splitProviderModel splits a "providerId:modelId" select value on the first
// colon only, so model IDs that contain colons (e.g. Ollama's
// "nomic-embed-text:latest") are preserved intact.
function splitProviderModel(value) {
  const idx = value.indexOf(':');
  if (idx < 0) return { providerId: '', modelId: '' };
  return { providerId: value.slice(0, idx), modelId: value.slice(idx + 1) };
}

function setOptionalNumber(id, value) {
  const input = document.getElementById(id);
  if (!input) return;
  input.value = value == null ? '' : String(value);
}

// optionalNumber reads an input and returns null when blank or invalid so
// the backend treats it as "use provider default".
function optionalNumber(id) {
  const raw = document.getElementById(id)?.value?.trim();
  if (raw === '') return null;
  const n = Number(raw);
  return Number.isFinite(n) ? n : null;
}

function renderAppInfo(info) {
  document.getElementById('settings-version').textContent = info.version || 'development build';
  document.getElementById('settings-data-dir').textContent = info.data_dir || '—';
  document.getElementById('settings-transports').textContent = (info.transports || []).join(' · ') || '—';
}

async function save() {
  const button = document.getElementById('settings-save-btn');
  const maxToolRounds = Number(document.getElementById('settings-max-tool-rounds').value);
  const maxInputTokens = Number(document.getElementById('settings-max-input-tokens').value);
  const maxOutputTokens = Number(document.getElementById('settings-max-output-tokens').value);
  if (!Number.isInteger(maxToolRounds) || maxToolRounds < 1 || maxToolRounds > 10000) {
    setStatus('Maximum tool rounds must be between 1 and 10,000.', true);
    return;
  }
  if (!Number.isInteger(maxInputTokens) || maxInputTokens < 1000 || maxInputTokens > 2000000) {
    setStatus('Max input tokens must be between 1,000 and 2,000,000.', true);
    return;
  }
  const compactionThreshold = Number(document.getElementById('settings-compaction-threshold').value);
  if (!Number.isInteger(compactionThreshold) || compactionThreshold < 0 || compactionThreshold > 2000000) {
    setStatus('Compaction threshold must be between 0 and 2,000,000 (0 = auto).', true);
    return;
  }
  if (!Number.isInteger(maxOutputTokens) || maxOutputTokens < 256 || maxOutputTokens > 1000000) {
    setStatus('Max output tokens must be between 256 and 1,000,000.', true);
    return;
  }
  button.disabled = true;
  try {
    const embeddingValue = embeddingSelect.getSelected()?.[0] ?? '';
    const { providerId: embProviderId, modelId: embModelId } = splitProviderModel(embeddingValue);
    const visionValue = visionSelect.getSelected()?.[0] ?? '';
    const { providerId: visProviderId, modelId: visModelId } = splitProviderModel(visionValue);
    const audioValue = audioSelect.getSelected()?.[0] ?? '';
    const { providerId: audProviderId, modelId: audModelId } = splitProviderModel(audioValue);
    const videoValue = videoSelect.getSelected()?.[0] ?? '';
    const { providerId: vidProviderId, modelId: vidModelId } = splitProviderModel(videoValue);
    const learningThreshold = Number(document.getElementById('settings-learning-threshold').value);
    if (!Number.isInteger(learningThreshold) || learningThreshold < 0 || learningThreshold > 1000) {
      setStatus('Learning review threshold must be between 0 and 1,000.', true);
      return;
    }
    const maxAutoContinues = Number(document.getElementById('settings-auto-continues').value);
    if (!Number.isInteger(maxAutoContinues) || maxAutoContinues < 0 || maxAutoContinues > 10000) {
      setStatus('Max auto-continues must be between 0 and 10,000 (0 = unlimited).', true);
      return;
    }
    const webAnswerProvider = webAnswerProviderSelect.getSelected()?.[0] ?? '';
    const webAnswerModel = document.getElementById('settings-web-answer-model')?.value?.trim() ?? '';
    const webAnswerAPIKey = document.getElementById('settings-web-answer-api-key')?.value?.trim() ?? '';
    await rpc('settings.set', {
      compaction_enabled: document.getElementById('settings-compaction-enabled').checked,
      prompt_caching: document.getElementById('settings-prompt-caching').checked,
      max_tool_rounds: maxToolRounds,
      max_input_tokens: maxInputTokens,
      compaction_threshold: compactionThreshold,
      max_output_tokens: maxOutputTokens,
      embedding_provider_id: embProviderId || null,
      embedding_model_id: embModelId || null,
      vision_provider_id: visProviderId || null,
      vision_model_id: visModelId || null,
      audio_provider_id: audProviderId || null,
      audio_model_id: audModelId || null,
      video_provider_id: vidProviderId || null,
      video_model_id: vidModelId || null,
      web_answer_provider: webAnswerProvider || null,
      web_answer_model: webAnswerModel || null,
      web_answer_api_key: webAnswerAPIKey || null,
      learning_review_threshold: learningThreshold,
      max_auto_continues: maxAutoContinues,
      temperature: optionalNumber('settings-temperature'),
      top_p: optionalNumber('settings-top-p'),
      top_k: optionalNumber('settings-top-k'),
      frequency_penalty: optionalNumber('settings-frequency-penalty'),
      presence_penalty: optionalNumber('settings-presence-penalty'),
    });
    const model = preferredSelect.getSelected()?.[0] ?? '';
    localStorage.setItem('nusashell.model', model);
    window.dispatchEvent(new CustomEvent('nusashell:preferred-model', { detail: { model } }));
    setStatus('Saved on this device.');
    toast('Settings saved', 'success');
  } catch (err) {
    setStatus(err.message, true);
    toast(err.message, 'error');
  } finally {
    button.disabled = false;
  }
}

function saveSidebarPreference(event) {
  window.nusashell?.setSidebarCompact(event.currentTarget.checked);
}

function saveReconnectPreference(event) {
  setAutoReconnect(event.currentTarget.checked);
  setConnectionStatus(event.currentTarget.checked ? 'Automatic reconnect is on.' : 'Automatic reconnect is off.');
}

async function checkConnection() {
  const button = document.getElementById('settings-check-connection-btn');
  button.disabled = true;
  try {
    await rpc('app.info', {}, { timeoutMs: 4000 });
    setConnectionStatus('Your local agent responded.');
  } catch {
    setConnectionStatus('Sorry, it looks like your agent is offline.', true);
  } finally {
    button.disabled = false;
  }
}

function setStatus(message, isError = false) {
  const status = document.getElementById('settings-save-status');
  status.textContent = message;
  status.style.color = isError ? 'var(--red)' : '';
}

function setConnectionStatus(message, isError = false) {
  const status = document.getElementById('settings-connection-status');
  status.textContent = message;
  status.style.color = isError ? 'var(--red)' : '';
}
