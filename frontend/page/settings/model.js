// Pure settings form model. No DOM, no RPC, and no frontend view imports.

const IMAGE_ID = /gpt-image|dall-e|stable-diffusion|seedream|ideogram|recraft|imagen-|riverflow|flash-image|gemini-.*-image$|nano-banana/i;
const VIDEO_GEN_ID = /video|veo|wan-|grok-imagine|sora|kling|hailuo|minimax|pika|runway|luma|cogvideo|animate|vidu/i;

const NOT_SET_WHEN_EMPTY = new Set([
  'vision_model',
  'audio_model',
  'video_model',
  'image_model',
  'video_gen_model',
  'tts_model',
  'user_prompt',
  'web_answer_provider',
  'remote_access_addresses',
]);

export const SETTINGS_SECTIONS = [
  { id: 'overview', label: 'Overview' },
  { id: 'models', label: 'Models' },
  { id: 'agent', label: 'Agent' },
  { id: 'context', label: 'Context & memory' },
  { id: 'media', label: 'Media' },
  { id: 'web', label: 'Web' },
  { id: 'interface', label: 'Interface' },
  { id: 'remote', label: 'Remote access' },
];

const WORKFLOW_OPTIONS = [
  { value: 'dedicated', label: 'Dedicated' },
  { value: 'reuse', label: 'Reuse' },
];

const CONTRACT_OPTIONS = [
  { value: '', label: 'Default' },
  { value: 'off', label: 'Off' },
  { value: 'hint', label: 'Hint' },
  { value: 'require', label: 'Require' },
];

const LANGUAGE_OPTIONS = [
  { value: '', label: 'Auto' },
  { value: 'id', label: 'Indonesian' },
  { value: 'en', label: 'English' },
];

const SEARCH_OPTIONS = [
  { value: '', label: 'Auto' },
  { value: 'auto', label: 'Auto — merge all sources' },
  { value: 'round_robin', label: 'Round robin' },
  { value: 'random', label: 'Random' },
  { value: 'brave', label: 'Brave' },
  { value: 'serper', label: 'Serper' },
  { value: 'tavily', label: 'Tavily' },
  { value: 'startpage', label: 'Startpage' },
  { value: 'wikipedia', label: 'Wikipedia' },
  { value: 'github', label: 'GitHub' },
];

const ANSWER_OPTIONS = [
  { value: '', label: 'Disabled' },
  { value: 'brave', label: 'Brave' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'perplexity', label: 'Perplexity' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'xai', label: 'xAI' },
];

function numberField(spec) {
  const field = {
    key: spec.key,
    section: spec.section,
    type: 'number',
    label: spec.label,
    hint: spec.hint,
    keywords: spec.keywords,
    wire: spec.key,
    min: spec.min,
    max: spec.max,
    step: spec.step ?? 1,
    integer: true,
    default: spec.default,
  };
  if (spec.zeroMeaning) field.zeroMeaning = spec.zeroMeaning;
  if (spec.advanced) field.advanced = true;
  return field;
}

function samplingField(spec) {
  const field = {
    key: spec.key,
    section: 'agent',
    type: 'sampling',
    label: spec.label,
    hint: spec.hint,
    keywords: spec.keywords,
    wire: spec.key,
    advanced: true,
    sampling: true,
    step: spec.step,
    default: null,
  };
  if (spec.min != null) field.min = spec.min;
  if (spec.max != null) field.max = spec.max;
  if (spec.integer) field.integer = true;
  return field;
}

function singleModel(spec) {
  return {
    key: spec.key,
    section: 'models',
    type: 'model',
    label: spec.label,
    hint: spec.hint,
    keywords: spec.keywords,
    wire: { kind: 'single', name: spec.key },
    role: 'chat',
    default: '',
  };
}

function pairModel(spec) {
  return {
    key: spec.key,
    section: spec.section,
    type: 'model',
    label: spec.label,
    hint: spec.hint,
    keywords: spec.keywords,
    wire: { kind: 'pair', provider: spec.provider, model: spec.model },
    role: spec.role,
    default: '',
  };
}

function secretField(spec) {
  return {
    key: spec.key,
    section: 'web',
    type: 'secret',
    label: spec.label,
    hint: spec.hint,
    keywords: spec.keywords,
    wire: spec.key,
    secret: true,
    default: '',
  };
}

export const SETTINGS_FIELDS = [
  singleModel({
    key: 'delegate_model',
    label: 'Delegate model',
    hint: 'Model used by the internal delegate tool.',
    keywords: ['delegate', 'background', 'subagent'],
  }),
  singleModel({
    key: 'review_model',
    label: 'Review model',
    hint: 'Model used for background learning reviews.',
    keywords: ['review', 'learning', 'learner'],
  }),
  singleModel({
    key: 'compaction_model',
    label: 'Compaction model',
    hint: 'Model that writes the compaction summary.',
    keywords: ['compaction', 'summary', 'context'],
  }),
  pairModel({
    key: 'embedding_model',
    section: 'models',
    label: 'Embedding model',
    hint: 'Model used for semantic search over skills and memory.',
    keywords: ['embedding', 'semantic', 'memory'],
    role: 'embedding',
    provider: 'embedding_provider_id',
    model: 'embedding_model_id',
  }),
  {
    key: 'user_prompt',
    section: 'agent',
    type: 'textarea',
    label: 'Custom instructions',
    hint: 'Text injected into every agent turn after the base identity.',
    keywords: ['prompt', 'instructions', 'identity'],
    wire: 'user_prompt',
    default: '',
  },
  numberField({
    key: 'max_tool_rounds',
    section: 'agent',
    label: 'Maximum tool rounds',
    hint: 'Stops a turn that keeps requesting more tool work.',
    keywords: ['tools', 'rounds', 'limit'],
    min: 1,
    max: 10000,
    default: 8,
  }),
  numberField({
    key: 'repeated_tool_limit',
    section: 'agent',
    label: 'Repeated tool limit',
    hint: 'Breaks a loop of identical tool calls, or disables that guard at zero.',
    keywords: ['loop', 'repeat', 'tools'],
    min: 0,
    max: 100,
    default: 3,
    zeroMeaning: 'Disabled',
  }),
  numberField({
    key: 'max_parallel_tools',
    section: 'agent',
    label: 'Max parallel tools',
    hint: 'How many tool calls from one round run at the same time.',
    keywords: ['parallel', 'concurrency', 'tools'],
    min: 1,
    max: 64,
    default: 6,
  }),
  numberField({
    key: 'max_auto_continues',
    section: 'agent',
    label: 'Max auto-continues',
    hint: 'How many later turns may start while todos stay open.',
    keywords: ['continue', 'todos', 'turns'],
    min: 0,
    max: 10000,
    default: 10,
    zeroMeaning: 'Unlimited',
  }),
  numberField({
    key: 'slow_down',
    section: 'agent',
    label: 'Slow down',
    hint: 'Seconds to wait before every round of every conversation.',
    keywords: ['delay', 'pace', 'slow'],
    min: 0,
    max: 60,
    default: 0,
    zeroMeaning: 'Off',
  }),
  numberField({
    key: 'max_output_tokens',
    section: 'agent',
    label: 'Max output tokens',
    hint: 'Default completion token limit when a model does not advertise one.',
    keywords: ['output', 'tokens', 'completion'],
    min: 256,
    max: 1000000,
    step: 256,
    default: 65536,
  }),
  samplingField({
    key: 'temperature',
    label: 'Temperature',
    hint: 'Sampling temperature sent to the provider.',
    keywords: ['temperature', 'sampling', 'randomness'],
    min: 0,
    max: 2,
    step: 0.1,
  }),
  samplingField({
    key: 'top_p',
    label: 'Top P',
    hint: 'Nucleus sampling cutoff sent to the provider.',
    keywords: ['nucleus', 'sampling', 'top_p'],
    min: 0,
    max: 1,
    step: 0.05,
  }),
  samplingField({
    key: 'top_k',
    label: 'Top K',
    hint: 'Highest-probability token cutoff for Anthropic models.',
    keywords: ['anthropic', 'sampling', 'top_k'],
    min: 1,
    step: 1,
    integer: true,
  }),
  samplingField({
    key: 'frequency_penalty',
    label: 'Frequency penalty',
    hint: 'Penalty for tokens the model has already used.',
    keywords: ['frequency', 'penalty', 'sampling'],
    min: -2,
    max: 2,
    step: 0.1,
  }),
  samplingField({
    key: 'presence_penalty',
    label: 'Presence penalty',
    hint: 'Penalty for tokens that have appeared at least once.',
    keywords: ['presence', 'penalty', 'sampling'],
    min: -2,
    max: 2,
    step: 0.1,
  }),
  {
    key: 'plugin_contract_mode',
    section: 'agent',
    type: 'segmented',
    label: 'Plugin contracts',
    hint: 'How strictly agents must read a plugin usage contract before calling it.',
    keywords: ['plugin', 'contract', 'mcp'],
    wire: 'plugin_contract_mode',
    options: CONTRACT_OPTIONS,
    default: '',
  },
  {
    key: 'compaction_workflow',
    section: 'context',
    type: 'segmented',
    label: 'Compaction workflow',
    hint: 'Dedicated uses a summary-only request, and reuse keeps the active model prompt.',
    keywords: ['compaction', 'workflow', 'reuse'],
    wire: 'compaction_workflow',
    options: WORKFLOW_OPTIONS,
    default: 'dedicated',
  },
  numberField({
    key: 'max_input_tokens',
    section: 'context',
    label: 'Max input tokens',
    hint: 'Fallback context size when a provider does not advertise one.',
    keywords: ['input', 'context', 'tokens'],
    min: 1000,
    max: 2000000,
    step: 1000,
    default: 200000,
  }),
  numberField({
    key: 'compaction_threshold',
    section: 'context',
    label: 'Compaction threshold',
    hint: 'Token count that starts compaction, or automatic at zero.',
    keywords: ['threshold', 'compaction', 'tokens'],
    min: 0,
    max: 2000000,
    step: 1000,
    default: 0,
    zeroMeaning: 'Auto · 80% of the model window',
  }),
  numberField({
    key: 'compaction_summary_max_tokens',
    section: 'context',
    label: 'Summary max tokens',
    hint: 'Output budget for the compaction summary call.',
    keywords: ['summary', 'budget', 'compaction'],
    min: 0,
    max: 100000,
    step: 500,
    default: 0,
    advanced: true,
    zeroMeaning: 'Default',
  }),
  numberField({
    key: 'compaction_summary_min_chars',
    section: 'context',
    label: 'Summary min characters',
    hint: 'Shortest summary the quality guard accepts before retrying.',
    keywords: ['summary', 'characters', 'guard'],
    min: 0,
    max: 100000,
    step: 50,
    default: 0,
    advanced: true,
    zeroMeaning: 'Default',
  }),
  {
    key: 'prompt_caching',
    section: 'context',
    type: 'boolean',
    label: 'Prompt caching',
    hint: 'Use provider prompt caching when the active model supports it.',
    keywords: ['cache', 'prompt', 'cost'],
    wire: 'prompt_caching',
    default: false,
  },
  numberField({
    key: 'learner_nudge_interval',
    section: 'context',
    label: 'Learner interval',
    hint: 'Unreviewed turns between periodic learning jobs.',
    keywords: ['learner', 'interval', 'review'],
    min: 0,
    max: 100,
    default: 10,
    zeroMeaning: 'Disabled',
  }),
  {
    key: 'project_memory_base',
    section: 'context',
    type: 'text',
    label: 'Project memory directory',
    hint: 'Absolute directory for per-workspace memory files.',
    keywords: ['memory', 'directory', 'project'],
    wire: 'project_memory_base',
    default: '',
  },
  pairModel({
    key: 'vision_model',
    section: 'media',
    label: 'Vision model',
    hint: 'Model that describes images when the chat model cannot see them.',
    keywords: ['vision', 'images', 'fallback'],
    role: 'vision',
    provider: 'vision_provider_id',
    model: 'vision_model_id',
  }),
  pairModel({
    key: 'audio_model',
    section: 'media',
    label: 'Audio model',
    hint: 'Model that transcribes audio when the chat model cannot hear it.',
    keywords: ['audio', 'transcription', 'fallback'],
    role: 'audio',
    provider: 'audio_provider_id',
    model: 'audio_model_id',
  }),
  pairModel({
    key: 'video_model',
    section: 'media',
    label: 'Video model',
    hint: 'Model that describes video when the chat model cannot watch it.',
    keywords: ['video', 'fallback', 'understanding'],
    role: 'video',
    provider: 'video_provider_id',
    model: 'video_model_id',
  }),
  {
    key: 'stt_offline_model',
    section: 'media',
    type: 'select',
    label: 'Offline STT model',
    hint: 'Local Whisper model used for offline transcription.',
    keywords: ['stt', 'whisper', 'offline'],
    wire: 'stt_offline_model',
    default: '',
  },
  {
    key: 'stt_offline_language',
    section: 'media',
    type: 'segmented',
    label: 'Audio language',
    hint: 'Language hint for offline transcription.',
    keywords: ['language', 'stt', 'speech'],
    wire: 'stt_offline_language',
    options: LANGUAGE_OPTIONS,
    default: '',
  },
  pairModel({
    key: 'image_model',
    section: 'media',
    label: 'Image model',
    hint: 'Model used by the image generation tool.',
    keywords: ['image', 'generation', 'picture'],
    role: 'image',
    provider: 'image_provider_id',
    model: 'image_model_id',
  }),
  pairModel({
    key: 'video_gen_model',
    section: 'media',
    label: 'Video generation model',
    hint: 'Model used by the video generation tool.',
    keywords: ['video', 'generation', 'clips'],
    role: 'video_gen',
    provider: 'video_gen_provider_id',
    model: 'video_gen_model_id',
  }),
  pairModel({
    key: 'tts_model',
    section: 'media',
    label: 'Speech model',
    hint: 'Model used by the speech generation tool.',
    keywords: ['speech', 'voice', 'tts'],
    role: 'tts',
    provider: 'tts_provider_id',
    model: 'tts_model_id',
  }),
  {
    key: 'web_search_strategy',
    section: 'web',
    type: 'select',
    label: 'Search strategy',
    hint: 'How web search chooses a provider for each query.',
    keywords: ['search', 'strategy', 'provider'],
    wire: 'web_search_strategy',
    options: SEARCH_OPTIONS,
    default: '',
  },
  secretField({
    key: 'web_search_brave_api_key',
    label: 'Brave API key',
    hint: 'API key for the Brave search provider.',
    keywords: ['brave', 'search', 'key'],
  }),
  secretField({
    key: 'web_search_serper_api_key',
    label: 'Serper API key',
    hint: 'API key for the Serper search provider.',
    keywords: ['serper', 'search', 'key'],
  }),
  secretField({
    key: 'web_search_tavily_api_key',
    label: 'Tavily API key',
    hint: 'API key for the Tavily search provider.',
    keywords: ['tavily', 'search', 'key'],
  }),
  {
    key: 'web_answer_provider',
    section: 'web',
    type: 'select',
    label: 'Answer provider',
    hint: 'Vendor that serves grounded web answers.',
    keywords: ['answer', 'provider', 'vendor'],
    wire: 'web_answer_provider',
    options: ANSWER_OPTIONS,
    default: '',
  },
  secretField({
    key: 'web_answer_api_key',
    label: 'Answer API key',
    hint: 'API key for the web answer vendor.',
    keywords: ['answer', 'key', 'vendor'],
  }),
  {
    key: 'web_answer_model',
    section: 'web',
    type: 'text',
    label: 'Answer model',
    hint: 'Optional model or preset for the web answer vendor.',
    keywords: ['answer', 'preset', 'vendor'],
    wire: 'web_answer_model',
    default: '',
  },
  {
    key: 'sound_notifications',
    section: 'interface',
    type: 'boolean',
    label: 'Sound notifications',
    hint: 'Play a sound when an agent turn finishes or fails.',
    keywords: ['sound', 'notification', 'audio'],
    wire: 'sound_notifications',
    default: true,
  },
  {
    key: 'pets_auto_start',
    section: 'interface',
    type: 'boolean',
    label: 'Auto-start desktop pet',
    hint: 'Launch the desktop pet when this shell starts.',
    keywords: ['pet', 'desktop', 'startup'],
    wire: 'pets_auto_start',
    default: false,
  },
  {
    key: 'remote_access_enabled',
    section: 'remote',
    type: 'boolean',
    label: 'Remote access',
    hint: 'Allow paired devices to reach this shell from outside the machine.',
    keywords: ['remote', 'pairing', 'access'],
    wire: 'remote_access_enabled',
    default: false,
  },
  {
    key: 'remote_access_addresses',
    section: 'remote',
    type: 'addresses',
    label: 'Remote addresses',
    hint: 'Absolute HTTP or HTTPS addresses that remote devices can open.',
    keywords: ['address', 'url', 'remote'],
    wire: 'remote_access_addresses',
    default: '',
  },
];

const FIELD_BY_KEY = new Map(SETTINGS_FIELDS.map((field) => [field.key, field]));

function fieldByKey(key) {
  return FIELD_BY_KEY.get(key);
}

export function splitProviderModel(value) {
  const text = value == null ? '' : String(value);
  if (!text) return { providerId: '', modelId: '' };
  const index = text.indexOf(':');
  if (index < 0) return { providerId: '', modelId: text };
  return { providerId: text.slice(0, index), modelId: text.slice(index + 1) };
}

export function modelLabel(models, value) {
  const text = value == null ? '' : String(value);
  if (!text) return '';
  const parts = splitProviderModel(text);
  if (!Array.isArray(models)) return text;
  const match = models.find((model) => model
    && String(model.provider_id ?? '') === parts.providerId
    && String(model.id ?? '') === parts.modelId);
  if (!match) return text;
  const id = String(match.id ?? '');
  return match.provider_name ? `${id} · ${String(match.provider_name)}` : id;
}

function stringValue(value) {
  return value == null ? '' : String(value);
}

function normalizeAddresses(value) {
  const lines = Array.isArray(value) ? value : String(value ?? '').split('\n');
  const seen = new Set();
  const out = [];
  for (const line of lines) {
    const address = String(line).trim();
    if (!address || seen.has(address)) continue;
    seen.add(address);
    out.push(address);
  }
  return out;
}

function sameList(left, right) {
  return left.length === right.length && left.every((item, index) => item === right[index]);
}

function normSample(value) {
  if (value == null || value === '') return null;
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

function sameSampling(left, right) {
  const a = normSample(left);
  const b = normSample(right);
  return a === b;
}

function readFormValue(field, dto) {
  if (field.secret) return '';
  if (field.wire?.kind === 'pair') {
    const provider = String(dto[field.wire.provider] ?? '').trim();
    const model = String(dto[field.wire.model] ?? '').trim();
    return provider && model ? `${provider}:${model}` : '';
  }
  if (field.type === 'addresses') {
    const value = dto[field.key];
    return Array.isArray(value) ? value.join('\n') : '';
  }
  if (field.type === 'sampling') {
    const value = dto[field.key];
    if (value == null || value === '') return null;
    const number = Number(value);
    return Number.isFinite(number) ? number : null;
  }
  if (field.type === 'number') {
    const value = dto[field.key];
    return typeof value === 'number' && Number.isFinite(value) ? value : field.default;
  }
  if (field.type === 'boolean') {
    return typeof dto[field.key] === 'boolean' ? dto[field.key] : field.default;
  }
  if (field.wire?.kind === 'single') {
    const value = dto[field.wire.name];
    return typeof value === 'string' ? value : '';
  }
  const value = dto[field.key];
  if (typeof value !== 'string') return field.default;
  if (field.key === 'compaction_workflow' && value !== 'dedicated' && value !== 'reuse') return field.default;
  return value;
}

export function settingsFormValues(dto) {
  const source = dto && typeof dto === 'object' ? dto : {};
  const form = {};
  for (const field of SETTINGS_FIELDS) form[field.key] = readFormValue(field, source);
  return form;
}

function isAbsoluteMemoryPath(value) {
  const text = String(value ?? '').trim();
  if (!text) return true;
  if (text.startsWith('/') || text.startsWith('~')) return true;
  return /^[A-Za-z]:[\\/]/.test(text);
}

function addressError(value) {
  const lines = Array.isArray(value) ? value : String(value ?? '').split('\n');
  for (const line of lines) {
    const address = String(line).trim();
    if (!address) continue;
    if (!/^https?:\/\//i.test(address)) {
      return 'Enter an absolute http or https URL with a host and no username or password.';
    }
    let url;
    try {
      url = new URL(address);
    } catch {
      return 'Enter an absolute http or https URL with a host and no username or password.';
    }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return 'Enter an absolute http or https URL with a host and no username or password.';
    }
    if (!url.hostname || url.username || url.password) {
      return 'Enter an absolute http or https URL with a host and no username or password.';
    }
  }
  return '';
}

function numberError(field, allowEmpty) {
  const kind = field.integer ? 'an integer' : 'a number';
  if (field.max == null) {
    return allowEmpty
      ? `Leave this empty or enter ${kind} of at least ${field.min}.`
      : `Enter ${kind} of at least ${field.min}.`;
  }
  return allowEmpty
    ? `Leave this empty or enter ${kind} from ${field.min} to ${field.max}.`
    : `Enter ${kind} from ${field.min} to ${field.max}.`;
}

function validateNumber(field, value, allowEmpty) {
  if (allowEmpty && (value == null || value === '')) return '';
  if (typeof value !== 'number' || !Number.isFinite(value)) return numberError(field, allowEmpty);
  if (field.integer && !Number.isInteger(value)) return numberError(field, allowEmpty);
  if (field.min != null && value < field.min) return numberError(field, allowEmpty);
  if (field.max != null && value > field.max) return numberError(field, allowEmpty);
  return '';
}

function validateChoice(field, value) {
  if (typeof value !== 'string') return 'Choose one of the supported values.';
  if (!field.options) return '';
  return field.options.some((option) => option.value === value) ? '' : 'Choose one of the supported values.';
}

export function validateSettingsField(key, value, draft) {
  void draft;
  const field = fieldByKey(key);
  if (!field) return 'Unknown setting.';
  if (field.type === 'number') return validateNumber(field, value, false);
  if (field.type === 'sampling') return validateNumber(field, value, true);
  if (field.type === 'boolean') return typeof value === 'boolean' ? '' : 'Choose on or off.';
  if (field.type === 'addresses') return addressError(value);
  if (field.type === 'segmented' || field.type === 'select') return validateChoice(field, value);
  if (typeof value !== 'string') return 'Enter text.';
  if (field.key === 'project_memory_base' && !isAbsoluteMemoryPath(value)) {
    return 'Enter an absolute path, or leave this empty.';
  }
  return '';
}

function changed(field, saved, draft) {
  const before = saved?.[field.key];
  const after = draft?.[field.key];
  if (field.type === 'sampling') return !sameSampling(before, after);
  if (field.type === 'addresses') return !sameList(normalizeAddresses(before), normalizeAddresses(after));
  if (field.type === 'number') {
    return !(typeof before === 'number' && typeof after === 'number' && before === after);
  }
  if (field.type === 'boolean') return before !== after;
  return stringValue(before) !== stringValue(after);
}

function assignPatch(patch, field, draft) {
  if (field.wire?.kind === 'pair') {
    const parts = splitProviderModel(stringValue(draft?.[field.key]));
    patch[field.wire.provider] = parts.providerId;
    patch[field.wire.model] = parts.modelId;
    return;
  }
  if (field.wire?.kind === 'single') {
    patch[field.wire.name] = stringValue(draft?.[field.key]);
    return;
  }
  if (field.type === 'sampling') {
    patch[field.key] = normSample(draft?.[field.key]);
    return;
  }
  if (field.type === 'addresses') {
    patch[field.key] = normalizeAddresses(draft?.[field.key]);
    return;
  }
  if (field.type === 'number' || field.type === 'boolean') {
    patch[field.key] = draft?.[field.key];
    return;
  }
  patch[field.key] = stringValue(draft?.[field.key]);
}

function applyReuseWorkflow(patch, saved, draft) {
  if (stringValue(draft?.compaction_workflow) !== 'reuse') return;
  const savedModel = stringValue(saved?.compaction_model);
  const draftModel = stringValue(draft?.compaction_model);
  if (savedModel !== '' || draftModel !== '') patch.compaction_model = '';
}

export function buildSettingsPatch(saved, draft, options = {}) {
  const patch = {};
  const secrets = options?.secrets;
  for (const field of SETTINGS_FIELDS) {
    if (field.secret) {
      if (secrets && Object.prototype.hasOwnProperty.call(secrets, field.key)) {
        patch[field.key] = String(secrets[field.key] ?? '').trim();
      }
      continue;
    }
    if (!changed(field, saved, draft)) continue;
    assignPatch(patch, field, draft);
  }
  applyReuseWorkflow(patch, saved, draft);
  return patch;
}

function optionLabel(field, value) {
  const option = field.options?.find((entry) => entry.value === value);
  return option ? option.label : stringValue(value);
}

export function describeSettingValue(key, value) {
  const field = fieldByKey(key);
  if (!field) return value == null || value === '' ? 'Not set' : stringValue(value);
  if (field.type === 'boolean') return value ? 'On' : 'Off';
  if (field.type === 'number') {
    if (field.zeroMeaning && Number(value) === 0) return field.zeroMeaning;
    return stringValue(value);
  }
  if (field.type === 'sampling') {
    if (value == null || value === '') return 'Default';
    return stringValue(value);
  }
  if (field.type === 'addresses') {
    const lines = normalizeAddresses(value);
    return lines.length === 0 ? 'Not set' : lines.join(', ');
  }
  const text = stringValue(value).trim();
  if (!text) return NOT_SET_WHEN_EMPTY.has(key) ? 'Not set' : 'Default';
  if (field.options) return optionLabel(field, value);
  return stringValue(value);
}

export function diffSettings(saved, draft) {
  const changes = [];
  for (const field of SETTINGS_FIELDS) {
    if (field.secret || !changed(field, saved, draft)) continue;
    changes.push({
      key: field.key,
      section: field.section,
      label: field.label,
      before: describeSettingValue(field.key, saved?.[field.key]),
      after: describeSettingValue(field.key, draft?.[field.key]),
    });
  }
  return changes;
}

export function filterSettingsFields(query) {
  const needle = String(query ?? '').trim().toLowerCase();
  const fields = SETTINGS_FIELDS.filter((field) => field.section !== 'overview');
  if (!needle) return fields;
  return fields.filter((field) => {
    const haystack = [field.label, field.hint, field.key, ...field.keywords].join('\n').toLowerCase();
    return haystack.includes(needle);
  });
}

function matchesRole(model, role) {
  switch (role) {
    case 'chat':
      return !model.kind || model.kind === 'chat';
    case 'embedding':
      return model.kind === 'embedding';
    case 'vision':
      return model.vision === true;
    case 'audio':
      return model.audio === true;
    case 'video':
      return model.video === true || model.video_gen === true;
    case 'image':
      return model.kind === 'image' || IMAGE_ID.test(String(model.id || ''));
    case 'video_gen':
      return model.kind === 'video' || VIDEO_GEN_ID.test(String(model.id || ''));
    case 'tts':
      return model.tts === true;
    default:
      return false;
  }
}

export function modelOptionsForRole(models, role) {
  if (!Array.isArray(models)) return [];
  return models.filter((model) => model && matchesRole(model, role)).map((model) => ({
    value: `${model.provider_id}:${model.id}`,
    label: String(model.id),
    providerName: model.provider_name ? String(model.provider_name) : '',
    context: Number.isFinite(model.context) ? model.context : 0,
  }));
}

export function formatBytes(n) {
  if (!Number.isFinite(n) || n <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let value = n;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const shown = value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1);
  return `${shown} ${units[unit]}`;
}

export function observeInstallSpeed(probe, phase, fetched, nowMs) {
  if (phase !== probe.phase) {
    probe.phase = phase;
    probe.fetched = fetched;
    probe.at = nowMs;
    probe.speedEma = 0;
    return 0;
  }
  const dt = (nowMs - probe.at) / 1000;
  if (dt < 0.25) return probe.speedEma;
  const instant = Math.max(0, fetched - probe.fetched) / dt;
  probe.speedEma = probe.speedEma > 0 ? probe.speedEma * 0.6 + instant * 0.4 : instant;
  probe.fetched = fetched;
  probe.at = nowMs;
  return Math.round(probe.speedEma);
}

function modelTile(form, spec) {
  const value = stringValue(form?.[spec.field]).trim();
  if (!value) return { state: 'off', detail: 'Not set' };
  const parts = splitProviderModel(value);
  return { state: 'on', detail: parts.modelId || value };
}

function searchTile(form) {
  const strategy = stringValue(form?.web_search_strategy).trim();
  const field = fieldByKey('web_search_strategy');
  const detail = !strategy || strategy === 'auto' ? 'Auto' : optionLabel(field, strategy);
  return { state: 'on', detail };
}

function answerTile(form) {
  const provider = stringValue(form?.web_answer_provider).trim();
  if (!provider) return { state: 'off', detail: 'Not set' };
  return { state: 'on', detail: optionLabel(fieldByKey('web_answer_provider'), provider) };
}

const CAPABILITY_TILES = [
  { id: 'vision', label: 'Vision', section: 'media', field: 'vision_model', kind: 'model' },
  { id: 'audio', label: 'Audio', section: 'media', field: 'audio_model', kind: 'model' },
  { id: 'video', label: 'Video', section: 'media', field: 'video_model', kind: 'model' },
  { id: 'offline-stt', label: 'Offline speech-to-text', section: 'media', field: '', kind: 'stt' },
  { id: 'image', label: 'Image generation', section: 'media', field: 'image_model', kind: 'model' },
  { id: 'video-gen', label: 'Video generation', section: 'media', field: 'video_gen_model', kind: 'model' },
  { id: 'speech', label: 'Speech', section: 'media', field: 'tts_model', kind: 'model' },
  { id: 'offline-tts', label: 'Offline text-to-speech', section: 'media', field: '', kind: 'tts' },
  { id: 'web-search', label: 'Web search', section: 'web', field: 'web_search_strategy', kind: 'search' },
  { id: 'web-answer', label: 'Web answer', section: 'web', field: 'web_answer_provider', kind: 'answer' },
  { id: 'embeddings', label: 'Embeddings', section: 'models', field: 'embedding_model', kind: 'model' },
  { id: 'prompt-caching', label: 'Prompt caching', section: 'context', field: '', kind: 'cache' },
];

export function capabilityTiles(form, extras = {}) {
  const flags = extras && typeof extras === 'object' ? extras : {};
  return CAPABILITY_TILES.map((spec) => {
    let state = 'off';
    let detail = 'Not set';
    if (spec.kind === 'model') {
      ({ state, detail } = modelTile(form, spec));
    } else if (spec.kind === 'search') {
      ({ state, detail } = searchTile(form));
    } else if (spec.kind === 'answer') {
      ({ state, detail } = answerTile(form));
    } else if (spec.kind === 'stt') {
      if (flags.sttReady === true) {
        state = 'on';
        detail = 'Ready';
      } else {
        state = 'install';
        detail = flags.sttInstalled === true ? 'Installed' : 'Not installed';
      }
    } else if (spec.kind === 'tts') {
      if (flags.ttsReady === true) {
        state = 'on';
        detail = 'Ready';
      } else {
        state = 'install';
        detail = 'Not installed';
      }
    } else if (spec.kind === 'cache') {
      state = form?.prompt_caching ? 'on' : 'off';
      detail = form?.prompt_caching ? 'On' : 'Off';
    }
    return {
      id: spec.id,
      label: spec.label,
      section: spec.section,
      field: spec.field,
      state,
      detail,
    };
  });
}
