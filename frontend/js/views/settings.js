// Settings workspace: native browser preferences plus the Go runtime controls.

import { autoReconnectEnabled, on, rpc, setAutoReconnect } from '../rpc.js';
import { toast, createSelect, el } from '../ui.js';
import { FONT_OPTIONS, readFontPreference, setFontPreference } from '../font-preferences.js';

let bound = false;
const state = { embeddingProviderId: '', embeddingModelId: '', visionProviderId: '', visionModelId: '', imageProviderId: '', imageModelId: '', audioProviderId: '', audioModelId: '', videoProviderId: '', videoModelId: '', videoGenProviderId: '', videoGenModelId: '', ttsProviderId: '', ttsModelId: '', webAnswerProvider: '', webAnswerModel: '', webSearchStrategy: '', compactionModel: '', reviewModel: '', delegateModel: '' };
let preferredSelect;
let embeddingSelect;
let visionSelect;
let imageSelect;
let compactionSelect;
let reviewSelect;
let delegateSelect;
let audioSelect;
let videoSelect;
let videoGenSelect;
let ttsSelect;
let webAnswerProviderSelect;
let webSearchStrategySelect;
let contractModeSelect;
let sttModelSelect;
let sttLanguageSelect;
let fontSelect;
let syncingFontPreference = false;

export async function initSettings() {
  if (!bound) {
    bound = true;
    document.getElementById('settings-save-btn').addEventListener('click', save);
    document.getElementById('settings-sidebar-compact').addEventListener('change', saveSidebarPreference);
    document.getElementById('settings-auto-reconnect').addEventListener('change', saveReconnectPreference);
    document.getElementById('settings-check-connection-btn').addEventListener('click', checkConnection);
    fontSelect = createSelect(document.getElementById('settings-font-family'), {
      data: FONT_OPTIONS.map((option) => ({
        text: `${option.label} — ${option.description}`,
        value: option.id,
      })),
      search: false,
      onChange: handleFontPreferenceChange,
    });
    syncFontPreference();
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
    imageSelect = createSelect(document.getElementById('settings-image-model'), {
      placeholder: 'Disabled — generate_image is not available',
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
    videoGenSelect = createSelect(document.getElementById('settings-video-gen-model'), {
      placeholder: 'Disabled — generate_video is not available',
      search: true,
    });
    ttsSelect = createSelect(document.getElementById('settings-tts-model'), {
      placeholder: 'Disabled — generate_speech needs the offline piper engine',
      search: true,
    });
    compactionSelect = createSelect(document.getElementById('settings-compaction-model'), {
      placeholder: 'Default — use the conversation\'s active model',
      search: true,
    });
    reviewSelect = createSelect(document.getElementById('settings-review-model'), {
      placeholder: 'Default — use the conversation\'s active model',
      search: true,
    });
    delegateSelect = createSelect(document.getElementById('settings-delegate-model'), {
      placeholder: 'Default — use the conversation\'s active model',
      search: true,
    });
    contractModeSelect = createSelect(document.getElementById('settings-plugin-contract-mode'), {
      data: [
        { text: 'Default — advisory hint on first use', value: '', placeholder: true },
        { text: 'Off — never show contract notices', value: 'off' },
        { text: 'Hint — advisory note until the contract is read', value: 'hint' },
        { text: 'Require — block MCP calls until the contract is read', value: 'require' },
      ],
    });
    sttModelSelect = createSelect(document.getElementById('settings-stt-model'), {
      placeholder: 'Auto — first installed GGML model',
      search: true,
    });
    sttLanguageSelect = createSelect(document.getElementById('settings-stt-language'), {
      placeholder: 'Auto-detect',
      search: false,
      data: [
        { text: 'Auto-detect', value: '' },
        { text: 'Bahasa Indonesia', value: 'id' },
        { text: 'English', value: 'en' },
      ],
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
    webSearchStrategySelect = createSelect(document.getElementById('settings-web-search-strategy'), {
      placeholder: 'Auto — merge all sources (default)',
      data: [
        { text: 'Auto — merge all sources (default)', value: '', placeholder: true },
        { text: 'Round robin — rotate API providers per query', value: 'round_robin' },
        { text: 'Random — pick one API provider per query', value: 'random' },
        { text: 'Brave only', value: 'brave' },
        { text: 'Serper (Google API) only', value: 'serper' },
        { text: 'Tavily only', value: 'tavily' },
        { text: 'Startpage only', value: 'startpage' },
        { text: 'Wikipedia only', value: 'wikipedia' },
        { text: 'GitHub only', value: 'github' },
      ],
    });
    window.addEventListener('hashchange', () => {
      if (location.hash === '#settings') void refresh();
    });
    bindDiskSync();
    bindTTSInstall();
    bindSTTInstall();
  }
  await refresh();
}

// Disk sync: the backend watcher reloads config/settings.json on external
// edits and announces it here. When the form has unsaved edits we never
// clobber them — the status line says the runtime moved on instead.
let diskSyncDirty = false;

function bindDiskSync() {
  const view = document.querySelector('[data-view="settings"]');
  if (view) {
    view.addEventListener('input', () => { diskSyncDirty = true; }, { capture: true });
  }
  on('settings.applied', () => {
    if (diskSyncDirty) {
      setStatus('Runtime reloaded from disk. Unsaved form edits kept — reopen this view to resync.', true);
      return;
    }
    void refresh();
    setStatus('Settings reloaded from disk.');
  });
  on('settings.rejected', (payload) => {
    setStatus(`Skipped external settings change (${payload?.reason ?? 'unknown'}) — previous values stay active.`, true);
  });
}

export async function refresh() {
  syncFontPreference();
  const [settingsResult, infoResult, modelsResult, sttStatusResult, ttsStatusResult] = await Promise.allSettled([
    rpc('settings.get'),
    rpc('app.info', {}, { timeoutMs: 4000 }),
    rpc('ai.models.list'),
    rpc('settings.stt_install_status', {}, { timeoutMs: 4000 }),
    rpc('settings.tts_install_status', {}, { timeoutMs: 4000 }),
  ]);

  if (settingsResult.status === 'fulfilled') {
    const { settings } = settingsResult.value;
    document.getElementById('settings-compaction-enabled').checked = settings.compaction_enabled !== false;
    document.getElementById('settings-prompt-caching').checked = settings.prompt_caching === true;
    document.getElementById('settings-sound-notifications').checked = settings.sound_notifications !== false;
    document.getElementById('settings-user-prompt').value = settings.user_prompt ?? '';
    document.getElementById('settings-max-tool-rounds').value = settings.max_tool_rounds ?? 8;
    document.getElementById('settings-repeated-tool-limit').value = settings.repeated_tool_limit ?? 3;
    document.getElementById('settings-max-parallel-tools').value = settings.max_parallel_tools ?? 6;
    contractModeSelect.setSelected([settings.plugin_contract_mode ?? '']);
    document.getElementById('settings-max-input-tokens').value = settings.max_input_tokens ?? 200000;
    document.getElementById('settings-compaction-threshold').value = settings.compaction_threshold ?? 0;
    document.getElementById('settings-compaction-summary-max-tokens').value = settings.compaction_summary_max_tokens ?? 0;
    document.getElementById('settings-compaction-summary-min-chars').value = settings.compaction_summary_min_chars ?? 0;
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
    state.imageProviderId = settings.image_provider_id ?? '';
    state.imageModelId = settings.image_model_id ?? '';
    state.audioProviderId = settings.audio_provider_id ?? '';
    state.audioModelId = settings.audio_model_id ?? '';
    state.videoProviderId = settings.video_provider_id ?? '';
    state.videoModelId = settings.video_model_id ?? '';
    state.videoGenProviderId = settings.video_gen_provider_id ?? '';
    state.videoGenModelId = settings.video_gen_model_id ?? '';
    state.ttsProviderId = settings.tts_provider_id ?? '';
    state.ttsModelId = settings.tts_model_id ?? '';
    state.webAnswerProvider = settings.web_answer_provider ?? '';
    state.webAnswerModel = settings.web_answer_model ?? '';
    state.webSearchStrategy = settings.web_search_strategy ?? '';
    state.compactionModel = settings.compaction_model ?? '';
    state.reviewModel = settings.review_model ?? '';
    state.delegateModel = settings.delegate_model ?? '';
    document.getElementById('settings-learner-nudge-interval').value = settings.learner_nudge_interval ?? 10;
    sttLanguageSelect.setSelected([['id', 'en'].includes(settings.stt_offline_language) ? settings.stt_offline_language : '']);
    document.getElementById('settings-project-memory-base').value = settings.project_memory_base ?? '';
    document.getElementById('settings-auto-continues').value = settings.max_auto_continues ?? 10;
    document.getElementById('settings-slow-down').value = settings.slow_down ?? 0;
    // Web answer: set provider dropdown and model field. API key is write-only.
    webAnswerProviderSelect.setSelected([state.webAnswerProvider || '']);
    document.getElementById('settings-web-answer-model').value = state.webAnswerModel;
    document.getElementById('settings-web-answer-api-key').value = '';
    // Web search: set strategy dropdown. API keys are write-only.
    webSearchStrategySelect.setSelected([state.webSearchStrategy || '']);
    document.getElementById('settings-web-search-brave-api-key').value = '';
    document.getElementById('settings-web-search-serper-api-key').value = '';
    document.getElementById('settings-web-search-tavily-api-key').value = '';
  } else {
    setStatus(`Could not load runtime settings: ${settingsResult.reason.message}`, true);
  }

  const allModels = modelsResult.status === 'fulfilled' ? modelsResult.value.models ?? [] : [];
  renderModelOptions(allModels);
  renderEmbeddingModelOptions(allModels);
  renderVisionModelOptions(allModels);
  renderImageModelOptions(allModels);
  renderAudioModelOptions(allModels);
  renderVideoModelOptions(allModels);
  renderVideoGenModelOptions(allModels);
  renderTTSModelOptions(allModels);
  renderCompactionModelOptions(allModels);
  renderReviewModelOptions(allModels);
  renderDelegateModelOptions(allModels);

  // Offline engine cards: paint the status line + populate the STT model
  // picker from the installer snapshot. The picker only lists installed
  // models, so a freshly installed whisper model appears here after the
  // install dialog closes and the view refreshes.
  if (sttStatusResult.status === 'fulfilled') {
    renderSTTCard(sttStatusResult.value);
    renderSTTModelOptions(sttStatusResult.value);
  }
  if (ttsStatusResult.status === 'fulfilled') {
    renderTTSMatchState(ttsStatusResult.value);
  }

  document.getElementById('settings-sidebar-compact').checked = localStorage.getItem('nusashell.sidebarMode') === 'icons';
  document.getElementById('settings-auto-reconnect').checked = autoReconnectEnabled();

  if (infoResult.status === 'fulfilled') {
    renderAppInfo(infoResult.value);
  } else {
    setConnectionStatus('Could not reach the local backend.', true);
  }
}

function syncFontPreference() {
  const id = readFontPreference();
  if (fontSelect) {
    syncingFontPreference = true;
    fontSelect.setSelected([id]);
    syncingFontPreference = false;
  }
  const preview = document.getElementById('settings-font-preview');
  if (preview) preview.dataset.font = id;
}

function handleFontPreferenceChange(value) {
  if (syncingFontPreference || !value) return;
  const id = setFontPreference(value);
  const option = FONT_OPTIONS.find((candidate) => candidate.id === id);
  const preview = document.getElementById('settings-font-preview');
  if (preview) preview.dataset.font = id;
  setStatus(`${option?.label ?? 'Interface font'} applied on this device.`);
}

function renderModelOptions(models) {
  // Only chat LLMs belong in the default model picker. Kind is enriched
  // from models.dev; treat unknown ("") as chat for backward compatibility.
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');
  const selected = localStorage.getItem('nusashell.model') || '';
  const data = [
    { text: 'Automatic — choose in each conversation', value: '', placeholder: true },
    ...chatModels.map((m) => {
      const label = m.id;
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
      const label = m.id;
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

function renderImageModelOptions(models) {
  const imageModels = models.filter(isImageGeneratorModel);
  const data = [
    { text: 'Disabled — generate_image is not available', value: '', placeholder: true },
    ...imageModels.map((m) => {
      const label = m.id;
      const provider = m.provider_name ? ` · ${m.provider_name}` : '';
      const badges = modelCapabilityBadges(m);
      return {
        text: `${label}${provider}`,
        html: badges ? `${label}${badges}${provider}` : undefined,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  imageSelect.setData(data);
  const selected = state.imageProviderId && state.imageModelId
    ? `${state.imageProviderId}:${state.imageModelId}`
    : '';
  if (selected) imageSelect.setSelected([selected]);
}

function isImageGeneratorModel(model) {
  if (model?.kind === 'image') return true;
  const id = String(model?.id || '').toLowerCase();
  return /gpt-image|dall-e|stable-diffusion|seedream|ideogram|recraft|imagen-|riverflow|flash-image/.test(id);
}

// modelCapabilityBadges returns an HTML string of capability badges for a
// model option in Slim Select. For image/video generation pickers, the
// key badge is "i2i"/"i2v" (vision=true = accepts image input). Models
// without modality info get no badge — they still appear in the picker
// as best-effort (upstream will reject if unsupported).
function modelCapabilityBadges(m) {
  const badges = [];
  if (m.vision) badges.push('<span class="settings-model-badge settings-badge-i2i" title="Accepts image input (image-to-image / image-to-video)">i2i</span>');
  if (m.audio) badges.push('<span class="settings-model-badge settings-badge-audio" title="Accepts audio input">audio</span>');
  if (m.video) badges.push('<span class="settings-model-badge settings-badge-video" title="Accepts video input">video</span>');
  return badges.length ? ` <span class="settings-model-badges">${badges.join('')}</span>` : '';
}

function renderAudioModelOptions(models) {
  const audioModels = models.filter((m) => m.audio === true);
  const data = [
    { text: 'Disabled — non-audio models get a text placeholder instead', value: '', placeholder: true },
    ...audioModels.map((m) => {
      const label = m.id;
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
  const videoModels = models.filter((m) => m.video === true || m.video_gen === true);
  const data = [
    { text: 'Disabled — non-video models get a text placeholder instead', value: '', placeholder: true },
    ...videoModels.map((m) => {
      const label = m.id;
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

function renderVideoGenModelOptions(models) {
  const videoGenModels = models.filter(isVideoGeneratorModel);
  const data = [
    { text: 'Disabled — generate_video is not available', value: '', placeholder: true },
    ...videoGenModels.map((m) => {
      const label = m.id;
      const provider = m.provider_name ? ` · ${m.provider_name}` : '';
      const badges = modelCapabilityBadges(m);
      return {
        text: `${label}${provider}`,
        html: badges ? `${label}${badges}${provider}` : undefined,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  videoGenSelect.setData(data);
  const selected = state.videoGenProviderId && state.videoGenModelId
    ? `${state.videoGenProviderId}:${state.videoGenModelId}`
    : '';
  if (selected) videoGenSelect.setSelected([selected]);
}

function isVideoGeneratorModel(model) {
  if (model?.kind === 'video') return true;
  const id = String(model?.id || '').toLowerCase();
  return /video|veo|wan-|grok-imagine|sora|kling|hailuo|minimax|pika|runway|luma|cogvideo|animate|vidu/.test(id);
}

function renderTTSModelOptions(models) {
  const ttsModels = models.filter((m) => m.tts === true);
  const data = [
    { text: 'Disabled — generate_speech needs the offline piper engine', value: '', placeholder: true },
    ...ttsModels.map((m) => {
      const label = m.id;
      return {
        text: m.provider_name ? `${label} · ${m.provider_name}` : label,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  ttsSelect.setData(data);
  const selected = state.ttsProviderId && state.ttsModelId
    ? `${state.ttsProviderId}:${state.ttsModelId}`
    : '';
  if (selected) ttsSelect.setSelected([selected]);
}

function renderCompactionModelOptions(models) {
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');
  const data = [
    { text: 'Default — use the conversation\'s active model', value: '', placeholder: true },
    ...chatModels.map((m) => {
      const label = m.id;
      const ctx = m.context ? ` ${Math.round(m.context / 1000)}K` : '';
      return {
        text: m.provider_name ? `${label}${ctx} · ${m.provider_name}` : `${label}${ctx}`,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  compactionSelect.setData(data);
  if (state.compactionModel) compactionSelect.setSelected([state.compactionModel]);
}

function renderReviewModelOptions(models) {
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');
  const data = [
    { text: 'Default — use the conversation\'s active model', value: '', placeholder: true },
    ...chatModels.map((m) => {
      const label = m.id;
      const ctx = m.context ? ` ${Math.round(m.context / 1000)}K` : '';
      return {
        text: m.provider_name ? `${label}${ctx} · ${m.provider_name}` : `${label}${ctx}`,
        value: `${m.provider_id}:${m.id}`,
      };
    }),
  ];
  reviewSelect.setData(data);
  if (state.reviewModel) reviewSelect.setSelected([state.reviewModel]);
}

function renderDelegateModelOptions(models) {
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');
  const data = [
    { text: 'Default — use the conversation\'s active model', value: '', placeholder: true },
    ...chatModels.map((m) => {
      const label = m.id;
      const ctx = m.context ? ' ' + Math.round(m.context / 1000) + 'K' : '';
      return {
        text: m.provider_name ? label + ctx + ' · ' + m.provider_name : label + ctx,
        value: m.provider_id + ':' + m.id,
      };
    }),
  ];
  delegateSelect.setData(data);
  if (state.delegateModel) delegateSelect.setSelected([state.delegateModel]);
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

// ---- Offline TTS one-click install ----
//
// Flow: button opens the dialog (voice picker fed by
// settings.tts_install_status), Install starts settings.tts_install_start,
// live progress arrives over WS via tts.install.* events. When the done
// event lands the dialog closes, the view refreshes, and generate_speech
// is immediately usable — no offline-mode checkbox involved.

const ttsInstallState = { voiceSelect: null, running: false, returnFocus: null };

function bindTTSInstall() {
  document.getElementById('settings-tts-install-btn')?.addEventListener('click', openTTSInstallDialog);
  document.getElementById('tts-install-close')?.addEventListener('click', closeTTSInstallDialog);
  document.getElementById('tts-install-cancel')?.addEventListener('click', closeTTSInstallDialog);
  document.getElementById('tts-install-confirm')?.addEventListener('click', confirmTTSInstall);

  on('tts.install.progress', (payload) => {
    if (!payload || !ttsInstallState.running) return;
    renderTTSProgress(payload.phase, payload.bytes_fetched ?? 0, payload.bytes_total ?? 0, payload.message);
  });
  on('tts.install.done', async (payload) => {
    if (!ttsInstallState.running) return;
    ttsInstallState.running = false;
    setTTSInstallBusy(false);
    renderTTSProgress('done', 1, 1, payload?.message ?? 'Offline TTS ready');
    closeTTSInstallDialog();
    toast(`Offline TTS installed (${payload?.voice_id ?? 'voice'})`, 'success');
    await refresh();
  });
  on('tts.install.error', (payload) => {
    if (!ttsInstallState.running && !payload?.message) return;
    ttsInstallState.running = false;
    setTTSInstallBusy(false);
    showTTSInstallError(payload?.message ?? 'Install failed.');
  });
}

async function openTTSInstallDialog() {
  ttsInstallState.returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const overlay = document.getElementById('tts-install-overlay');
  const list = document.getElementById('tts-install-voice');
  showTTSInstallError('');
  document.getElementById('tts-install-progress').hidden = true;
  document.getElementById('tts-install-confirm').disabled = false;
  overlay.hidden = false;
  overlay.setAttribute('aria-hidden', 'false');

  let status;
  try {
    status = await rpc('settings.tts_install_status');
  } catch (err) {
    showTTSInstallError(err.message);
    return;
  }
  renderTTSMatchState(status);
  const data = [
    { text: 'Select a voice…', value: '', placeholder: true },
    ...(status.voices ?? []).map((v) => ({
      text: `${v.label} · ${fmtBytes(v.size_bytes)}${v.installed ? ' ✓' : ''}`,
      value: v.id,
    })),
  ];
  if (!ttsInstallState.voiceSelect) {
    ttsInstallState.voiceSelect = createSelect(list, { search: true });
  }
  ttsInstallState.voiceSelect.setData(data);
  ttsInstallState.voiceSelect.setSelected(['']);
  updateTTSConfirmLabel(status);
  if (status.running) {
    // A previous install is still in flight in the backend — reattach UI.
    ttsInstallState.running = true;
    setTTSInstallBusy(true);
    renderTTSProgress('', 0, 0, 'An install is already running…');
  }
}

function updateTTSConfirmLabel(status) {
  const btn = document.getElementById('tts-install-confirm');
  if (!btn) return;
  const allInstalled = status.binary_installed && (status.voices ?? []).every((v) => v.installed);
  btn.textContent = allInstalled ? 'Reinstall / add another voice' : 'Install';
}

// renderTTSMatchState paints the one-liner next to the install button so the
// card shows at a glance whether offline speech is ready (no enable toggle).
function renderTTSMatchState(status) {
  const label = document.getElementById('settings-tts-install-status');
  if (!label) return;
  const voiceCount = (status?.voices ?? []).filter((v) => v.installed).length;
  if (status?.ready) {
    label.textContent = `Offline speech ready${voiceCount ? ` · ${voiceCount} voice${voiceCount > 1 ? 's' : ''} installed` : ''}`;
    label.title = 'generate_speech will use the local piper engine automatically.';
  } else if (status?.binary_installed) {
    label.textContent = 'Engine installed — pick a voice above and install it.';
    label.title = '';
  } else {
    label.textContent = 'Offline engine not installed yet.';
    label.title = '';
  }
}

async function confirmTTSInstall() {
  const voiceId = ttsInstallState.voiceSelect?.getSelected()?.[0] ?? '';
  if (!voiceId) {
    toast('Pick a voice first', 'info');
    return;
  }
  ttsInstallState.running = true;
  setTTSInstallBusy(true);
  showTTSInstallError('');
  try {
    const res = await rpc('settings.tts_install_start', { voice_id: voiceId }, { timeoutMs: 15000 });
    if (!res.started) {
      // Already-running: keep the dialog in progress mode and wait for events.
      renderTTSProgress('', 0, 0, res.message || 'An install is already running…');
      return;
    }
    renderTTSProgress('', 0, 0, 'Starting download…');
  } catch (err) {
    ttsInstallState.running = false;
    setTTSInstallBusy(false);
    showTTSInstallError(err.message);
  }
}

function setTTSInstallBusy(busy) {
  const confirmBtn = document.getElementById('tts-install-confirm');
  const cancelBtn = document.getElementById('tts-install-cancel');
  const closeBtn = document.getElementById('tts-install-close');
  if (confirmBtn) confirmBtn.disabled = busy;
  if (cancelBtn) cancelBtn.textContent = busy ? 'Hide' : 'Cancel';
  if (closeBtn) closeBtn.disabled = busy;
}

function renderTTSProgress(phase, fetched, total, message) {
  const wrap = document.getElementById('tts-install-progress');
  if (!wrap) return;
  wrap.hidden = false;
  const phaseEl = document.getElementById('tts-install-phase');
  const bar = document.getElementById('tts-install-bar');
  const track = document.getElementById('tts-install-bar-track');
  const bytesEl = document.getElementById('tts-install-bytes');
  const labels = {
    binary: 'Downloading piper engine…',
    voice: 'Downloading voice model…',
    verify: 'Verifying installation…',
    done: 'Offline TTS ready ✓',
  };
  phaseEl.textContent = message || labels[phase] || 'Working…';
  let pct = null;
  if (phase === 'binary' && total > 0) pct = Math.round((fetched / total) * 100);
  else if (phase === 'voice' && total > 0) pct = Math.round((fetched / total) * 100);
  else if (phase === 'done' || phase === 'verify') pct = phase === 'done' ? 100 : null;
  if (pct == null) {
    track.classList.add('indeterminate');
    bar.style.width = '100%';
    track.setAttribute('aria-valuenow', '0');
  } else {
    track.classList.remove('indeterminate');
    bar.style.width = `${pct}%`;
    track.setAttribute('aria-valuenow', String(pct));
  }
  bytesEl.textContent = total > 0 ? `${fmtBytes(fetched)} / ${fmtBytes(total)}${pct != null ? ` · ${pct}%` : ''}` : '';
}

function showTTSInstallError(message) {
  const banner = document.getElementById('tts-install-error');
  if (!banner) return;
  banner.textContent = message;
  banner.hidden = !message;
}

function closeTTSInstallDialog() {
  const overlay = document.getElementById('tts-install-overlay');
  overlay.hidden = true;
  overlay.setAttribute('aria-hidden', 'true');
  document.getElementById('tts-install-progress').hidden = true;
  if (!ttsInstallState.running) setTTSInstallBusy(false);
  if (ttsInstallState.returnFocus?.isConnected) ttsInstallState.returnFocus.focus();
  ttsInstallState.returnFocus = null;
}

function fmtBytes(n) {
  if (!Number.isFinite(n) || n <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = n;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

function renderAppInfo(info) {
  document.getElementById('settings-version').textContent = info.version || 'development build';
  document.getElementById('settings-data-dir').textContent = info.data_dir || '—';
}

async function save() {
  const button = document.getElementById('settings-save-btn');
  const maxToolRounds = Number(document.getElementById('settings-max-tool-rounds').value);
  const repeatedToolLimit = Number(document.getElementById('settings-repeated-tool-limit').value);
  const maxInputTokens = Number(document.getElementById('settings-max-input-tokens').value);
  const maxOutputTokens = Number(document.getElementById('settings-max-output-tokens').value);
  const maxParallelTools = Number(document.getElementById('settings-max-parallel-tools').value);
  if (!Number.isInteger(maxToolRounds) || maxToolRounds < 1 || maxToolRounds > 10000) {
    setStatus('Maximum tool rounds must be between 1 and 10,000.', true);
    return;
  }
  if (!Number.isInteger(repeatedToolLimit) || repeatedToolLimit < 0 || repeatedToolLimit > 100) {
    setStatus('Repeated tool call limit must be between 0 and 100 (0 = disabled).', true);
    return;
  }
  if (!Number.isInteger(maxParallelTools) || maxParallelTools < 1 || maxParallelTools > 64) {
    setStatus('Max parallel tools must be between 1 and 64.', true);
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
  const compactionSummaryMaxTokens = Number(document.getElementById('settings-compaction-summary-max-tokens').value);
  if (!Number.isInteger(compactionSummaryMaxTokens) || compactionSummaryMaxTokens < 0 || compactionSummaryMaxTokens > 100000) {
    setStatus('Compaction summary max tokens must be between 0 and 100,000 (0 = default).', true);
    return;
  }
  const compactionSummaryMinChars = Number(document.getElementById('settings-compaction-summary-min-chars').value);
  if (!Number.isInteger(compactionSummaryMinChars) || compactionSummaryMinChars < 0 || compactionSummaryMinChars > 100000) {
    setStatus('Compaction summary min chars must be between 0 and 100,000 (0 = default).', true);
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
    const imageValue = imageSelect.getSelected()?.[0] ?? '';
    const { providerId: imgProviderId, modelId: imgModelId } = splitProviderModel(imageValue);
    const audioValue = audioSelect.getSelected()?.[0] ?? '';
    const { providerId: audProviderId, modelId: audModelId } = splitProviderModel(audioValue);
    const videoValue = videoSelect.getSelected()?.[0] ?? '';
    const { providerId: vidProviderId, modelId: vidModelId } = splitProviderModel(videoValue);
    const videoGenValue = videoGenSelect.getSelected()?.[0] ?? '';
    const { providerId: vidGenProviderId, modelId: vidGenModelId } = splitProviderModel(videoGenValue);
    const ttsValue = ttsSelect.getSelected()?.[0] ?? '';
    const { providerId: ttsProviderId, modelId: ttsModelId } = splitProviderModel(ttsValue);
    const compactionValue = compactionSelect.getSelected()?.[0] ?? '';
    const reviewValue = reviewSelect.getSelected()?.[0] ?? '';
    const delegateValue = delegateSelect.getSelected()?.[0] ?? '';
    const learnerNudgeInterval = Number(document.getElementById('settings-learner-nudge-interval').value);
    if (!Number.isInteger(learnerNudgeInterval) || learnerNudgeInterval < 0 || learnerNudgeInterval > 100) {
      setStatus('Periodic review interval must be between 0 and 100 (0 = disabled).', true);
      return;
    }
    const maxAutoContinues = Number(document.getElementById('settings-auto-continues').value);
    if (!Number.isInteger(maxAutoContinues) || maxAutoContinues < 0 || maxAutoContinues > 10000) {
      setStatus('Max auto-continues must be between 0 and 10,000 (0 = unlimited).', true);
      return;
    }
    const slowDown = Number(document.getElementById('settings-slow-down').value);
    if (!Number.isInteger(slowDown) || slowDown < 0 || slowDown > 60) {
      setStatus('Slow Down must be between 0 and 60 seconds (0 = off).', true);
      return;
    }
    const webAnswerProvider = webAnswerProviderSelect.getSelected()?.[0] ?? '';
    const webAnswerModel = document.getElementById('settings-web-answer-model')?.value?.trim() ?? '';
    const webAnswerAPIKey = document.getElementById('settings-web-answer-api-key')?.value?.trim() ?? '';
    const webSearchStrategy = webSearchStrategySelect.getSelected()?.[0] ?? '';
    const webSearchBraveAPIKey = document.getElementById('settings-web-search-brave-api-key')?.value?.trim() ?? '';
    const webSearchSerperAPIKey = document.getElementById('settings-web-search-serper-api-key')?.value?.trim() ?? '';
    const webSearchTavilyAPIKey = document.getElementById('settings-web-search-tavily-api-key')?.value?.trim() ?? '';
    await rpc('settings.set', {
      compaction_enabled: document.getElementById('settings-compaction-enabled').checked,
      prompt_caching: document.getElementById('settings-prompt-caching').checked,
      sound_notifications: document.getElementById('settings-sound-notifications').checked,
      user_prompt: document.getElementById('settings-user-prompt').value.trim() || null,
      max_tool_rounds: maxToolRounds,
      repeated_tool_limit: repeatedToolLimit,
      max_parallel_tools: maxParallelTools,
      plugin_contract_mode: contractModeSelect.getSelected()?.[0] ?? '',
      max_input_tokens: maxInputTokens,
      compaction_threshold: compactionThreshold,
      compaction_summary_max_tokens: compactionSummaryMaxTokens || null,
      compaction_summary_min_chars: compactionSummaryMinChars || null,
      compaction_model: compactionValue || null,
      review_model: reviewValue || null,
      learner_nudge_interval: learnerNudgeInterval,
      delegate_model: delegateValue || null,
      max_output_tokens: maxOutputTokens,
      embedding_provider_id: embProviderId || null,
      embedding_model_id: embModelId || null,
      vision_provider_id: visProviderId || null,
      vision_model_id: visModelId || null,
      image_provider_id: imgProviderId || null,
      image_model_id: imgModelId || null,
      audio_provider_id: audProviderId || null,
      audio_model_id: audModelId || null,
      stt_offline_model: sttModelSelect.getSelected()?.[0] || null,
      stt_offline_language: sttLanguageSelect.getSelected()?.[0] || null,
      video_provider_id: vidProviderId || null,
      video_model_id: vidModelId || null,
      video_gen_provider_id: vidGenProviderId || null,
      video_gen_model_id: vidGenModelId || null,
      tts_provider_id: ttsProviderId || null,
      tts_model_id: ttsModelId || null,
      web_answer_provider: webAnswerProvider || null,
      web_answer_model: webAnswerModel || null,
      web_answer_api_key: webAnswerAPIKey || null,
      web_search_strategy: webSearchStrategy || null,
      web_search_brave_api_key: webSearchBraveAPIKey || null,
      web_search_serper_api_key: webSearchSerperAPIKey || null,
      web_search_tavily_api_key: webSearchTavilyAPIKey || null,
      project_memory_base: document.getElementById('settings-project-memory-base').value.trim() || null,
      max_auto_continues: maxAutoContinues,
      slow_down: slowDown,
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
    diskSyncDirty = false;
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

// ---- Offline STT one-click install ----
//
// Mirrors the TTS flow with a requirements checklist: the card button opens
// the requirements dialog (settings.stt_install_status feeds the checklist,
// the per-OS guide, and the model picker). Install kicks off
// settings.stt_install_start; progress rides stt.install.* events with a
// slow status poll as a reattach fallback. Download speed is computed from
// the event byte deltas. On success the dialog closes, the view refreshes,
// and the new model appears in both selects — read_media can use it
// immediately (degradation ladder resolves per call).

const sttInstallState = {
  running: false,
  returnFocus: null,
  pollTimer: null,
  pollCount: 0,
  lastStatus: null,
  dialogSelect: null,
  // speed probe: resets on phase change, EMA-smoothed otherwise
  phase: '', fetched: 0, at: 0, speedEma: 0,
};

function bindSTTInstall() {
  document.getElementById('settings-stt-install-btn')?.addEventListener('click', openSTTInstallDialog);
  document.getElementById('stt-requirements-close')?.addEventListener('click', closeSTTInstallDialog);
  document.getElementById('stt-requirements-cancel')?.addEventListener('click', closeSTTInstallDialog);
  document.getElementById('stt-install-stop')?.addEventListener('click', stopSTTInstall);
  document.getElementById('stt-install-confirm')?.addEventListener('click', confirmSTTInstall);
  for (const os of ['linux', 'windows', 'macos']) {
    document.getElementById(`stt-guide-tab-${os}`)?.addEventListener('click', () => setSTTGuideTab(os));
  }
  on('stt.install.progress', (payload) => {
    if (!sttInstallState.running || !payload) return;
    renderSTTProgress(payload.phase, payload.bytes_fetched ?? 0, payload.bytes_total ?? 0, payload.message);
  });
  on('stt.install.done', (payload) => {
    finishSTTInstall(true, payload?.message ?? 'Offline STT ready');
  });
  on('stt.install.error', (payload) => {
    if (!sttInstallState.running && !payload?.message) return;
    finishSTTInstall(false, payload?.message ?? 'Install failed.');
  });
}

async function openSTTInstallDialog() {
  sttInstallState.returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const overlay = document.getElementById('stt-requirements-overlay');
  showSTTDialogError('');
  document.getElementById('stt-install-progress').hidden = true;
  const confirmBtn = document.getElementById('stt-install-confirm');
  confirmBtn.disabled = false;
  overlay.hidden = false;
  overlay.setAttribute('aria-hidden', 'false');

  let status;
  try {
    status = await rpc('settings.stt_install_status');
  } catch (err) {
    showSTTDialogError(err.message);
    return;
  }
  sttInstallState.lastStatus = status;
  renderSTTChecklist(status);
  renderSTTDialogModels(status);

  // Guide shows whenever the engine is missing — supported platforms see
  // "automatic install available" plus manual fallback steps; macOS sees
  // the brew path (.experimental/offline-stt-assessment.md §2.3).
  document.getElementById('stt-guide').hidden = !!status.engine_installed;

  updateSTTConfirmLabel(status);
  if (status.running) {
    sttInstallState.running = true;
    setSTTInstallBusy(true);
    startSTTPolling();
    renderSTTProgress('', 0, 0, 'An install is already running…');
  }
}

function updateSTTConfirmLabel(status) {
  const btn = document.getElementById('stt-install-confirm');
  if (!btn) return;
  const anyMissing = !(status?.models ?? []).every((m) => m.installed);
  btn.textContent = status?.engine_installed && !anyMissing ? 'Reinstall / add another model' : 'Install';
}

// needsManualEngine: no engine anywhere AND no official release to
// auto-download (macOS today). The Install button cannot help — point at
// the guide instead of failing mid-download.
function needsManualEngine(status) {
  return !status?.engine_installed && !status?.supported;
}

async function confirmSTTInstall() {
  const status = sttInstallState.lastStatus;
  if (status && needsManualEngine(status)) {
    document.getElementById('stt-guide').hidden = false;
    setSTTGuideTab(guessOSTab());
    toast('Engine must be installed manually on this platform — follow the guide.', 'info');
    return;
  }
  const modelId = sttInstallState.dialogSelect?.getSelected()?.[0] ?? '';
  if (!modelId) {
    toast('Pick a model first', 'info');
    return;
  }
  sttInstallState.running = true;
  setSTTInstallBusy(true);
  showSTTDialogError('');
  try {
    const res = await rpc('settings.stt_install_start', { model_id: modelId }, { timeoutMs: 15000 });
    if (!res.started) {
      renderSTTProgress('', 0, 0, res.message || 'An install is already running…');
      return;
    }
    startSTTPolling();
    renderSTTProgress('binary', 0, 0, 'Starting download…');
  } catch (err) {
    sttInstallState.running = false;
    setSTTInstallBusy(false);
    showSTTDialogError(err.message);
  }
}

async function stopSTTInstall() {
  try {
    await rpc('settings.stt_install_cancel', {}, { timeoutMs: 15000 });
  } catch {
    // backend already finished or never started — either way we stop waiting
  }
  finishSTTInstall(false, 'Install cancelled.');
}

function finishSTTInstall(success, message) {
  const wasRunning = sttInstallState.running;
  sttInstallState.running = false;
  stopSTTPolling();
  setSTTInstallBusy(false);
  resetSTTSpeedProbe();
  if (success) {
    renderSTTProgressIfOpen('done', 1, 1, message);
    toast(`Offline STT installed (${message || 'ready'})`, 'success');
    closeSTTInstallDialog();
  } else {
    renderSTTProgressIfOpen('', 0, 0, message);
    const overlay = document.getElementById('stt-requirements-overlay');
    if (overlay && !overlay.hidden) showSTTDialogError(message);
    else toast(message, 'error');
  }
  void refresh();
}

function setSTTInstallBusy(busy) {
  const confirmBtn = document.getElementById('stt-install-confirm');
  const cancelBtn = document.getElementById('stt-requirements-cancel');
  const closeBtn = document.getElementById('stt-requirements-close');
  const stopBtn = document.getElementById('stt-install-stop');
  if (confirmBtn) confirmBtn.disabled = busy;
  if (cancelBtn) cancelBtn.textContent = busy ? 'Hide' : 'Cancel';
  if (closeBtn) closeBtn.disabled = busy;
  if (stopBtn) stopBtn.hidden = !busy;
}

function closeSTTInstallDialog() {
  const overlay = document.getElementById('stt-requirements-overlay');
  overlay.hidden = true;
  overlay.setAttribute('aria-hidden', 'true');
  document.getElementById('stt-install-progress').hidden = true;
  stopSTTPolling();
  if (!sttInstallState.running) setSTTInstallBusy(false);
  if (sttInstallState.returnFocus?.isConnected) sttInstallState.returnFocus.focus();
  sttInstallState.returnFocus = null;
}

function showSTTDialogError(message) {
  const banner = document.getElementById('stt-requirements-error');
  if (!banner) return;
  banner.textContent = message;
  banner.hidden = !message;
}

// ---- checklist + guide ----

function renderSTTChecklist(status) {
  const rows = [
    ['stt-req-platform', !!status?.supported, status?.supported ? 'official release' : 'manual install required'],
    ['stt-req-engine', !!status?.engine_installed, status?.engine_path ? `${status.engine_source} · ${shortPath(status.engine_path)}` : 'not found'],
    ['stt-req-model', (status?.models ?? []).some((m) => m.installed), `${(status?.models ?? []).filter((m) => m.installed).length} installed`],
    ['stt-req-disk', true, status?.disk_free_bytes > 0 ? `${fmtBytes(status.disk_free_bytes)} free` : 'unknown'],
  ];
  for (const [id, ok, detail] of rows) {
    const row = document.getElementById(id);
    if (!row) continue;
    row.classList.toggle('ok', ok);
    row.classList.toggle('missing', !ok);
    const detailEl = row.querySelector('.stt-req-detail');
    if (detailEl) detailEl.textContent = detail;
  }
}

function setSTTGuideTab(os) {
  for (const name of ['linux', 'windows', 'macos']) {
    document.getElementById(`stt-guide-tab-${name}`)?.classList.toggle('active', name === os);
    document.getElementById(`stt-guide-tab-${name}`)?.setAttribute('aria-selected', String(name === os));
    const panel = document.getElementById(`stt-guide-${name}`);
    if (panel) panel.hidden = name !== os;
  }
}

function guessOSTab() {
  const p = (navigator.platform || '').toLowerCase();
  if (p.includes('win')) return 'windows';
  if (p.includes('mac')) return 'macos';
  return 'linux';
}

function shortPath(p) {
  const parts = String(p || '').split(/[\\/]/);
  return parts.slice(-2).join('/');
}

// ---- model picker (card + dialog) ----

function renderSTTModelOptions(status) {
  if (!sttModelSelect) return;
  const models = status?.models ?? [];
  const installed = models.filter((m) => m.installed);
  const data = [
    { text: 'Auto — first installed GGML model', value: '', placeholder: true },
    ...installed.map((m) => ({
      text: `${m.label} · ${fmtBytes(m.size_bytes)}${m.default ? ' (recommended)' : ''}`,
      value: m.id,
    })),
  ];
  sttModelSelect.setData(data);
  const active = status?.active_model ?? '';
  sttModelSelect.setSelected([installed.some((m) => m.id === active) ? active : '']);
}

function renderSTTCard(status) {
  const label = document.getElementById('settings-stt-install-status');
  if (!label) return;
  if (!status) {
    label.textContent = '';
    return;
  }
  const installedCount = (status.models ?? []).filter((m) => m.installed).length;
  if (status.ready) {
    label.textContent = `Offline STT ready${status.active_model ? ` · ${status.active_model.replace(/^ggml-/, '')}` : ''}${installedCount > 1 ? ` · ${installedCount} models` : ''}`;
    label.title = 'read_media transcribes locally via whisper.cpp.';
  } else if (!status.supported) {
    label.textContent = 'No official release for this platform — see the install guide.';
    label.title = '';
  } else if (status.engine_installed) {
    label.textContent = 'whisper.cpp engine installed — install a model next.';
    label.title = '';
  } else {
    label.textContent = 'Engine not installed yet.';
    label.title = '';
  }
}

function renderSTTDialogModels(status) {
  const list = document.getElementById('stt-install-model');
  if (!list) return;
  if (!sttInstallState.dialogSelect) {
    sttInstallState.dialogSelect = createSelect(list, { search: true });
  }
  const models = status?.models ?? [];
  const activeModel = status?.active_model ?? '';
  const data = [
    { text: 'Select a model…', value: '', placeholder: true },
    ...models.map((m) => ({
      text: `${m.label} · ${fmtBytes(m.size_bytes)}${m.installed ? ' ✓' : ''}`,
      value: m.id,
    })),
  ];
  sttInstallState.dialogSelect.setData(data);
  // Preselect: the recommended default that is not yet installed, else the
  // active model, else empty.
  const defaultMissing = models.find((m) => m.default && !m.installed);
  const preselect = defaultMissing?.id ?? (models.some((m) => m.id === activeModel) ? activeModel : '');
  sttInstallState.dialogSelect.setSelected([preselect]);
}

// ---- progress rendering with download-speed estimate ----

const sttPhaseLabels = {
  binary: 'Downloading whisper.cpp engine…',
  model: 'Downloading Whisper model…',
  verify: 'Verifying installation…',
  done: 'Offline STT ready ✓',
};

function renderSTTProgress(phase, fetched, total, message) {
  const wrap = document.getElementById('stt-install-progress');
  if (!wrap) return;
  wrap.hidden = false;
  const phaseEl = document.getElementById('stt-install-phase');
  const barFill = document.getElementById('stt-install-bar');
  const track = document.getElementById('stt-install-bar-track');
  const bytesEl = document.getElementById('stt-install-bytes');

  phaseEl.textContent = message || sttPhaseLabels[phase] || 'Working…';

  let pct = null;
  if ((phase === 'binary' || phase === 'model') && total > 0) {
    pct = Math.min(100, Math.round((fetched / total) * 100));
  } else if (phase === 'done') {
    pct = 100;
  }

  if (pct == null) {
    track.classList.add('indeterminate');
    barFill.style.width = '100%';
    track.setAttribute('aria-valuenow', '0');
  } else {
    track.classList.remove('indeterminate');
    barFill.style.width = `${pct}%`;
    track.setAttribute('aria-valuenow', String(pct));
  }

  const speed = estimateSTTSpeed(phase, fetched);
  const speedTxt = speed > 0 ? ` · ${fmtBytes(speed)}/s` : '';
  bytesEl.textContent = total > 0
    ? `${fmtBytes(fetched)} / ${fmtBytes(total)}${pct != null ? ` · ${pct}%` : ''}${speedTxt}`
    : `${fmtBytes(fetched)}${speedTxt}`;
}

// renderSTTProgressIfOpen only paints when the dialog is visible.
function renderSTTProgressIfOpen(phase, fetched, total, message) {
  const overlay = document.getElementById('stt-requirements-overlay');
  if (overlay && !overlay.hidden) renderSTTProgress(phase, fetched, total, message);
}

function estimateSTTSpeed(phase, fetched) {
  const now = performance.now();
  if (phase !== sttInstallState.phase) {
    sttInstallState.phase = phase;
    sttInstallState.fetched = fetched;
    sttInstallState.at = now;
    sttInstallState.speedEma = 0;
    return 0;
  }
  const dt = (now - sttInstallState.at) / 1000;
  if (dt < 0.25) return sttInstallState.speedEma;
  const inst = Math.max(0, fetched - sttInstallState.fetched) / dt;
  sttInstallState.speedEma = sttInstallState.speedEma > 0 ? sttInstallState.speedEma * 0.6 + inst * 0.4 : inst;
  sttInstallState.fetched = fetched;
  sttInstallState.at = now;
  return Math.round(sttInstallState.speedEma);
}

function resetSTTSpeedProbe() {
  sttInstallState.phase = '';
  sttInstallState.fetched = 0;
  sttInstallState.at = 0;
  sttInstallState.speedEma = 0;
}

// ---- reattach polling (WS events remain the primary signal) ----

function startSTTPolling() {
  stopSTTPolling();
  sttInstallState.pollCount = 0;
  sttInstallState.pollTimer = setInterval(async () => {
    sttInstallState.pollCount += 1;
    try {
      const status = await rpc('settings.stt_install_status');
      sttInstallState.lastStatus = status;
      renderSTTChecklist(status);
      if (status.running && sttInstallState.pollCount === 2) {
        // Reattached to an in-flight install without seeing events yet —
        // paint an indeterminate bar so the dialog never looks dead.
        renderSTTProgress('', 0, 0, 'Downloading…');
      }
    } catch {
      // transient RPC hiccup — next tick retries
    }
  }, 1500);
}

function stopSTTPolling() {
  if (sttInstallState.pollTimer) {
    clearInterval(sttInstallState.pollTimer);
    sttInstallState.pollTimer = null;
  }
}
