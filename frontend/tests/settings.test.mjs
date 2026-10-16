import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

import {
  SETTINGS_FIELDS,
  SETTINGS_SECTIONS,
  buildSettingsPatch,
  capabilityTiles,
  describeSettingValue,
  diffSettings,
  filterSettingsFields,
  formatBytes,
  modelOptionsForRole,
  observeInstallSpeed,
  settingsFormValues,
  modelLabel,
  splitProviderModel,
  validateSettingsField,
} from '../page/settings/model.js';

const SERVER_WIRE_KEYS = [
  'compaction_model',
  'compaction_summary_max_tokens',
  'compaction_summary_min_chars',
  'compaction_threshold',
  'compaction_workflow',
  'delegate_model',
  'embedding_model_id',
  'embedding_provider_id',
  'audio_model_id',
  'audio_provider_id',
  'frequency_penalty',
  'image_model_id',
  'image_provider_id',
  'learner_nudge_interval',
  'max_auto_continues',
  'max_input_tokens',
  'max_output_tokens',
  'max_parallel_tools',
  'max_tool_rounds',
  'pets_auto_start',
  'plugin_contract_mode',
  'presence_penalty',
  'project_memory_base',
  'prompt_caching',
  'remote_access_addresses',
  'remote_access_enabled',
  'repeated_tool_limit',
  'review_model',
  'slow_down',
  'sound_notifications',
  'stt_offline_language',
  'stt_offline_model',
  'temperature',
  'top_k',
  'top_p',
  'tts_model_id',
  'tts_provider_id',
  'user_prompt',
  'video_gen_model_id',
  'video_gen_provider_id',
  'video_model_id',
  'video_provider_id',
  'vision_model_id',
  'vision_provider_id',
  'web_answer_api_key',
  'web_answer_model',
  'web_answer_provider',
  'web_search_brave_api_key',
  'web_search_serper_api_key',
  'web_search_strategy',
  'web_search_tavily_api_key',
];

function field(key) {
  const found = SETTINGS_FIELDS.find((item) => item.key === key);
  assert.ok(found, `missing field ${key}`);
  return found;
}

function keysIn(section) {
  return SETTINGS_FIELDS.filter((item) => item.section === section).map((item) => item.key);
}

function wireNames(item) {
  if (typeof item.wire === 'string') return [item.wire];
  if (item.wire?.kind === 'pair') return [item.wire.provider, item.wire.model];
  return [item.wire.name];
}

test('settings sections are the eight prototype sections in order', () => {
  assert.deepEqual(SETTINGS_SECTIONS, [
    { id: 'overview', label: 'Overview' },
    { id: 'models', label: 'Models' },
    { id: 'agent', label: 'Agent' },
    { id: 'context', label: 'Context & memory' },
    { id: 'media', label: 'Media' },
    { id: 'web', label: 'Web' },
    { id: 'interface', label: 'Interface' },
    { id: 'remote', label: 'Remote access' },
  ]);
});

test('settings fields cover every server key with placement, wire, and search text', () => {
  const types = new Set(['number', 'sampling', 'textarea', 'text', 'boolean', 'segmented', 'select', 'model', 'secret', 'addresses']);
  const sectionIds = new Set(SETTINGS_SECTIONS.map((section) => section.id));
  assert.deepEqual(keysIn('models'), ['delegate_model', 'review_model', 'compaction_model', 'embedding_model']);
  assert.deepEqual(keysIn('agent'), [
    'user_prompt',
    'max_tool_rounds',
    'repeated_tool_limit',
    'max_parallel_tools',
    'max_auto_continues',
    'slow_down',
    'max_output_tokens',
    'temperature',
    'top_p',
    'top_k',
    'frequency_penalty',
    'presence_penalty',
    'plugin_contract_mode',
  ]);
  assert.deepEqual(keysIn('context'), [
    'compaction_workflow',
    'max_input_tokens',
    'compaction_threshold',
    'compaction_summary_max_tokens',
    'compaction_summary_min_chars',
    'prompt_caching',
    'learner_nudge_interval',
    'project_memory_base',
  ]);
  assert.deepEqual(keysIn('media'), [
    'vision_model',
    'audio_model',
    'video_model',
    'stt_offline_model',
    'stt_offline_language',
    'image_model',
    'video_gen_model',
    'tts_model',
  ]);
  assert.deepEqual(keysIn('web'), [
    'web_search_strategy',
    'web_search_brave_api_key',
    'web_search_serper_api_key',
    'web_search_tavily_api_key',
    'web_answer_provider',
    'web_answer_api_key',
    'web_answer_model',
  ]);
  assert.deepEqual(keysIn('interface'), ['sound_notifications', 'pets_auto_start']);
  assert.deepEqual(keysIn('remote'), ['remote_access_enabled', 'remote_access_addresses']);
  assert.deepEqual(keysIn('overview'), []);

  for (const item of SETTINGS_FIELDS) {
    assert.equal(typeof item.key, 'string');
    assert.ok(sectionIds.has(item.section), item.key);
    assert.ok(types.has(item.type), item.key);
    assert.equal(typeof item.label, 'string');
    assert.ok(item.label.length > 0, item.key);
    assert.equal(typeof item.hint, 'string');
    assert.match(item.hint, /\.$/, item.key);
    assert.ok(Array.isArray(item.keywords), item.key);
    assert.ok(item.keywords.length >= 2 && item.keywords.length <= 4, item.key);
    assert.ok(item.wire, item.key);
    assert.equal(Object.hasOwn(item, 'default'), true, item.key);
  }

  assert.equal(field('delegate_model').role, 'chat');
  assert.equal(field('review_model').role, 'chat');
  assert.equal(field('compaction_model').role, 'chat');
  assert.equal(field('embedding_model').role, 'embedding');
  assert.equal(field('vision_model').role, 'vision');
  assert.equal(field('audio_model').role, 'audio');
  assert.equal(field('video_model').role, 'video');
  assert.equal(field('image_model').role, 'image');
  assert.equal(field('video_gen_model').role, 'video_gen');
  assert.equal(field('tts_model').role, 'tts');
  assert.deepEqual(field('embedding_model').wire, { kind: 'pair', provider: 'embedding_provider_id', model: 'embedding_model_id' });
  assert.deepEqual(field('vision_model').wire, { kind: 'pair', provider: 'vision_provider_id', model: 'vision_model_id' });
  assert.deepEqual(field('audio_model').wire, { kind: 'pair', provider: 'audio_provider_id', model: 'audio_model_id' });
  assert.deepEqual(field('video_model').wire, { kind: 'pair', provider: 'video_provider_id', model: 'video_model_id' });
  assert.deepEqual(field('image_model').wire, { kind: 'pair', provider: 'image_provider_id', model: 'image_model_id' });
  assert.deepEqual(field('video_gen_model').wire, { kind: 'pair', provider: 'video_gen_provider_id', model: 'video_gen_model_id' });
  assert.deepEqual(field('tts_model').wire, { kind: 'pair', provider: 'tts_provider_id', model: 'tts_model_id' });
  assert.deepEqual(field('compaction_model').wire, { kind: 'single', name: 'compaction_model' });
  assert.deepEqual(field('review_model').wire, { kind: 'single', name: 'review_model' });
  assert.deepEqual(field('delegate_model').wire, { kind: 'single', name: 'delegate_model' });
  assert.equal(field('max_tool_rounds').wire, 'max_tool_rounds');
  assert.equal(field('temperature').type, 'sampling');
  assert.equal(field('temperature').sampling, true);
  assert.equal(field('temperature').advanced, true);
  assert.equal(field('top_k').integer, true);
  assert.equal(field('compaction_summary_max_tokens').advanced, true);
  assert.equal(field('compaction_summary_min_chars').advanced, true);
  assert.equal(field('max_tool_rounds').advanced, undefined);
  assert.equal(field('repeated_tool_limit').zeroMeaning, 'Disabled');
  assert.equal(field('max_auto_continues').zeroMeaning, 'Unlimited');
  assert.equal(field('slow_down').zeroMeaning, 'Off');
  assert.equal(field('compaction_threshold').zeroMeaning, 'Auto · 80% of the model window');
  assert.equal(field('compaction_summary_max_tokens').zeroMeaning, 'Default');
  assert.equal(field('compaction_summary_min_chars').zeroMeaning, 'Default');
  assert.equal(field('learner_nudge_interval').zeroMeaning, 'Disabled');
  assert.equal(field('plugin_contract_mode').type, 'segmented');
  assert.deepEqual(field('plugin_contract_mode').options.map((option) => option.label), ['Default', 'Off', 'Hint', 'Require']);
  assert.deepEqual(field('plugin_contract_mode').options.map((option) => option.value), ['', 'off', 'hint', 'require']);
  const searchOptions = field('web_search_strategy').options;
  assert.deepEqual(searchOptions.map((option) => option.value), ['', 'auto', 'round_robin', 'random', 'brave', 'serper', 'tavily', 'startpage', 'wikipedia', 'github']);
  assert.deepEqual(searchOptions.filter((option) => option.value === '' || option.value === 'auto'), [
    { value: '', label: 'Auto' },
    { value: 'auto', label: 'Auto — merge all sources' },
  ]);
  assert.equal(new Set(searchOptions.map((option) => option.label)).size, searchOptions.length);
  assert.equal(field('web_answer_api_key').secret, true);
  assert.equal(field('web_search_brave_api_key').type, 'secret');
  assert.equal(field('sound_notifications').default, true);
  assert.equal(field('prompt_caching').default, false);
  assert.equal(field('max_tool_rounds').default, 8);
  assert.equal(field('max_auto_continues').default, 10);
  assert.equal(field('temperature').default, null);

  const names = SETTINGS_FIELDS.flatMap(wireNames).sort();
  assert.deepEqual(names, [...SERVER_WIRE_KEYS].sort());
});

test('settingsFormValues flattens pairs, defaults omitted integers, and hides secrets', () => {
  const empty = settingsFormValues();
  assert.equal(empty.max_tool_rounds, 8);
  assert.equal(empty.repeated_tool_limit, 3);
  assert.equal(empty.max_parallel_tools, 6);
  assert.equal(empty.max_auto_continues, 10);
  assert.equal(empty.learner_nudge_interval, 10);
  assert.equal(empty.compaction_summary_max_tokens, 0);
  assert.equal(empty.compaction_summary_min_chars, 0);
  assert.equal(empty.max_input_tokens, 200000);
  assert.equal(empty.max_output_tokens, 65536);
  assert.equal(empty.compaction_threshold, 0);
  assert.equal(empty.slow_down, 0);
  assert.equal(empty.compaction_workflow, 'dedicated');
  assert.equal(empty.prompt_caching, false);
  assert.equal(empty.sound_notifications, true);
  assert.equal(empty.pets_auto_start, false);
  assert.equal(empty.remote_access_enabled, false);
  assert.equal(empty.user_prompt, '');
  assert.equal(empty.plugin_contract_mode, '');
  assert.equal(empty.web_search_strategy, '');
  assert.equal(empty.web_answer_provider, '');
  assert.equal(empty.web_answer_model, '');
  assert.equal(empty.temperature, null);
  assert.equal(empty.top_p, null);
  assert.equal(empty.top_k, null);
  assert.equal(empty.remote_access_addresses, '');
  assert.equal(empty.web_answer_api_key, '');
  assert.equal(empty.embedding_model, '');
  assert.equal(empty.delegate_model, '');

  const roundTrip = settingsFormValues({
    embedding_provider_id: 'prov',
    embedding_model_id: 'nomic-embed-text:latest',
    vision_provider_id: 'openai',
    vision_model_id: '',
    compaction_model: 'openai:gpt-4.1',
    review_model: 'openai:review',
    delegate_model: 'openai:delegate',
    remote_access_addresses: ['https://a.example', 'https://b.example'],
    temperature: 0,
    top_p: 0.9,
    repeated_tool_limit: 0,
    prompt_caching: true,
    sound_notifications: false,
    web_answer_api_key: 'should-not-leak',
    max_tool_rounds: 12,
  });
  assert.equal(roundTrip.embedding_model, 'prov:nomic-embed-text:latest');
  assert.equal(roundTrip.vision_model, '');
  assert.equal(roundTrip.compaction_model, 'openai:gpt-4.1');
  assert.equal(roundTrip.review_model, 'openai:review');
  assert.equal(roundTrip.delegate_model, 'openai:delegate');
  assert.equal(roundTrip.remote_access_addresses, 'https://a.example\nhttps://b.example');
  assert.equal(roundTrip.temperature, 0);
  assert.equal(roundTrip.top_p, 0.9);
  assert.equal(roundTrip.repeated_tool_limit, 0);
  assert.equal(roundTrip.prompt_caching, true);
  assert.equal(roundTrip.sound_notifications, false);
  assert.equal(roundTrip.web_answer_api_key, '');
  assert.equal(roundTrip.max_tool_rounds, 12);
  assert.equal(settingsFormValues({ embedding_provider_id: 'prov' }).embedding_model, '');
  assert.equal(settingsFormValues({ embedding_model_id: 'nomic-embed-text:latest' }).embedding_model, '');
});

test('patch contains only dirty keys', () => {
  const saved = settingsFormValues({
    max_tool_rounds: 8,
    prompt_caching: true,
    embedding_provider_id: 'p',
    embedding_model_id: 'm',
    delegate_model: 'p:chat',
    temperature: 0.2,
  });
  const draft = { ...saved, max_tool_rounds: 12 };
  assert.deepEqual(buildSettingsPatch(saved, draft, {}), { max_tool_rounds: 12 });
});

test('clearing embedding_model sends both ids as empty strings', () => {
  const saved = settingsFormValues({
    embedding_provider_id: 'p',
    embedding_model_id: 'm',
  });
  const draft = { ...saved, embedding_model: '' };
  const patch = buildSettingsPatch(saved, draft, {});
  assert.deepEqual(patch, { embedding_provider_id: '', embedding_model_id: '' });
  assert.equal(JSON.stringify(patch).includes('null'), false);
});

test('clearing temperature sends null and unchanged null sampling is omitted', () => {
  const saved = settingsFormValues({ temperature: 0.7 });
  const cleared = { ...saved, temperature: null };
  assert.deepEqual(buildSettingsPatch(saved, cleared, {}), { temperature: null });

  const untouched = settingsFormValues({});
  const draft = { ...untouched, max_tool_rounds: 9 };
  const patch = buildSettingsPatch(untouched, draft, {});
  assert.equal(patch.max_tool_rounds, 9);
  for (const key of ['temperature', 'top_p', 'top_k', 'frequency_penalty', 'presence_penalty']) {
    assert.equal(Object.hasOwn(patch, key), false, key);
  }
  assert.deepEqual(buildSettingsPatch(saved, { ...saved, temperature: 0.7 }, {}), {});
});

test('secrets are write-only and an empty string deletes the stored key', () => {
  const saved = settingsFormValues({});
  const draft = { ...saved, web_answer_api_key: 'from-draft', web_search_brave_api_key: 'also-draft' };
  assert.deepEqual(buildSettingsPatch(saved, draft, {}), {});
  assert.deepEqual(buildSettingsPatch(saved, draft, { secrets: {} }), {});
  assert.deepEqual(
    buildSettingsPatch(saved, draft, { secrets: { web_answer_api_key: '' } }),
    { web_answer_api_key: '' },
  );
  assert.deepEqual(
    buildSettingsPatch(saved, draft, { secrets: { web_search_brave_api_key: '  brave-key  ' } }),
    { web_search_brave_api_key: 'brave-key' },
  );
  const mixed = buildSettingsPatch(
    saved,
    { ...draft, max_tool_rounds: 11 },
    { secrets: { web_search_tavily_api_key: 'tv' } },
  );
  assert.equal(mixed.max_tool_rounds, 11);
  assert.equal(mixed.web_search_tavily_api_key, 'tv');
  assert.equal(Object.hasOwn(mixed, 'web_answer_api_key'), false);
  assert.equal(Object.hasOwn(mixed, 'web_search_serper_api_key'), false);
});

test('splitProviderModel keeps colons inside the model id', () => {
  assert.deepEqual(splitProviderModel(''), { providerId: '', modelId: '' });
  assert.deepEqual(splitProviderModel('prov:nomic-embed-text:latest'), {
    providerId: 'prov',
    modelId: 'nomic-embed-text:latest',
  });
  assert.deepEqual(splitProviderModel('bare-model'), { providerId: '', modelId: 'bare-model' });
});

test('range validation matches the settings handler bounds', () => {
  const draft = settingsFormValues({});
  assert.notEqual(validateSettingsField('max_tool_rounds', 0, draft), '');
  assert.notEqual(validateSettingsField('max_tool_rounds', 10001, draft), '');
  assert.equal(validateSettingsField('max_tool_rounds', 8, draft), '');
  assert.equal(validateSettingsField('max_tool_rounds', 1, draft), '');
  assert.equal(validateSettingsField('max_tool_rounds', 10000, draft), '');
  assert.notEqual(validateSettingsField('max_tool_rounds', 8.5, draft), '');

  const cases = [
    ['repeated_tool_limit', 0, ''],
    ['repeated_tool_limit', 100, ''],
    ['repeated_tool_limit', -1, 'no'],
    ['repeated_tool_limit', 101, 'no'],
    ['max_parallel_tools', 1, ''],
    ['max_parallel_tools', 64, ''],
    ['max_parallel_tools', 0, 'no'],
    ['max_parallel_tools', 65, 'no'],
    ['max_auto_continues', 0, ''],
    ['max_auto_continues', 10000, ''],
    ['max_auto_continues', -1, 'no'],
    ['max_auto_continues', 10001, 'no'],
    ['slow_down', 0, ''],
    ['slow_down', 60, ''],
    ['slow_down', 61, 'no'],
    ['max_output_tokens', 256, ''],
    ['max_output_tokens', 1000000, ''],
    ['max_output_tokens', 255, 'no'],
    ['max_output_tokens', 1000001, 'no'],
    ['max_input_tokens', 1000, ''],
    ['max_input_tokens', 2000000, ''],
    ['max_input_tokens', 999, 'no'],
    ['max_input_tokens', 2000001, 'no'],
    ['compaction_threshold', 0, ''],
    ['compaction_threshold', 2000000, ''],
    ['compaction_threshold', -1, 'no'],
    ['compaction_threshold', 2000001, 'no'],
    ['compaction_summary_max_tokens', 0, ''],
    ['compaction_summary_max_tokens', 100000, ''],
    ['compaction_summary_max_tokens', 100001, 'no'],
    ['compaction_summary_min_chars', 0, ''],
    ['compaction_summary_min_chars', 100000, ''],
    ['compaction_summary_min_chars', -1, 'no'],
    ['learner_nudge_interval', 0, ''],
    ['learner_nudge_interval', 100, ''],
    ['learner_nudge_interval', 101, 'no'],
    ['temperature', null, ''],
    ['temperature', 0, ''],
    ['temperature', 2, ''],
    ['temperature', -0.1, 'no'],
    ['temperature', 2.1, 'no'],
    ['top_p', null, ''],
    ['top_p', 0, ''],
    ['top_p', 1, ''],
    ['top_p', 1.01, 'no'],
    ['top_k', null, ''],
    ['top_k', 1, ''],
    ['top_k', 40, ''],
    ['top_k', 0, 'no'],
    ['top_k', 1.5, 'no'],
    ['frequency_penalty', null, ''],
    ['frequency_penalty', -2, ''],
    ['frequency_penalty', 2, ''],
    ['frequency_penalty', -2.1, 'no'],
    ['presence_penalty', null, ''],
    ['presence_penalty', 2, ''],
    ['presence_penalty', 2.5, 'no'],
    ['compaction_workflow', 'dedicated', ''],
    ['compaction_workflow', 'reuse', ''],
    ['compaction_workflow', 'other', 'no'],
    ['plugin_contract_mode', '', ''],
    ['plugin_contract_mode', 'off', ''],
    ['plugin_contract_mode', 'hint', ''],
    ['plugin_contract_mode', 'require', ''],
    ['plugin_contract_mode', 'strict', 'no'],
    ['stt_offline_language', '', ''],
    ['stt_offline_language', 'id', ''],
    ['stt_offline_language', 'en', ''],
    ['stt_offline_language', 'fr', 'no'],
    ['web_answer_provider', '', ''],
    ['web_answer_provider', 'xai', ''],
    ['web_answer_provider', 'google', 'no'],
    ['web_search_strategy', '', ''],
    ['web_search_strategy', 'auto', ''],
    ['web_search_strategy', 'github', ''],
    ['web_search_strategy', 'bing', 'no'],
    ['project_memory_base', '', ''],
    ['project_memory_base', '/var/memory', ''],
    ['project_memory_base', '~/memory', ''],
    ['project_memory_base', 'C:\\memory', ''],
    ['project_memory_base', 'C:/memory', ''],
    ['project_memory_base', '  /tmp/memory  ', ''],
    ['project_memory_base', 'memory', 'no'],
    ['project_memory_base', '$HOME/memory', 'no'],
    ['remote_access_addresses', '', ''],
    ['remote_access_addresses', 'https://example.com:10994', ''],
    ['remote_access_addresses', '\nhttps://example.com:10994\n', ''],
    ['remote_access_addresses', 'javascript:alert(1)', 'no'],
    ['remote_access_addresses', 'https://user:pass@example.com', 'no'],
    ['remote_access_addresses', 'https://ok.example\njavascript:alert(1)', 'no'],
    ['user_prompt', 'Hello', ''],
    ['prompt_caching', true, ''],
    ['prompt_caching', false, ''],
  ];
  for (const [key, value, want] of cases) {
    const error = validateSettingsField(key, value, draft);
    if (want === '') assert.equal(error, '', `${key} ${String(value)} should be valid, got ${error}`);
    else assert.notEqual(error, '', `${key} ${String(value)} should be rejected`);
  }
});

test('reuse workflow forces an empty compaction model when a model was set', () => {
  const saved = settingsFormValues({
    compaction_workflow: 'dedicated',
    compaction_model: 'p:sum',
  });
  const draft = { ...saved, compaction_workflow: 'reuse', compaction_model: 'p:sum' };
  assert.deepEqual(buildSettingsPatch(saved, draft, {}), {
    compaction_workflow: 'reuse',
    compaction_model: '',
  });

  const already = settingsFormValues({ compaction_workflow: 'reuse', compaction_model: '' });
  assert.deepEqual(buildSettingsPatch(already, { ...already }, {}), {});

  const typedWhileReuse = { ...already, compaction_model: 'p:sum' };
  assert.deepEqual(buildSettingsPatch(already, typedWhileReuse, {}), { compaction_model: '' });

  const emptyModel = settingsFormValues({ compaction_workflow: 'dedicated', compaction_model: '' });
  assert.deepEqual(
    buildSettingsPatch(emptyModel, { ...emptyModel, compaction_workflow: 'reuse' }, {}),
    { compaction_workflow: 'reuse' },
  );

  const cleared = settingsFormValues({ delegate_model: 'p:chat' });
  assert.deepEqual(
    buildSettingsPatch(cleared, { ...cleared, delegate_model: '' }, {}),
    { delegate_model: '' },
  );
  const prompt = settingsFormValues({ user_prompt: 'Keep it short' });
  assert.deepEqual(
    buildSettingsPatch(prompt, { ...prompt, user_prompt: '' }, {}),
    { user_prompt: '' },
  );
});

test('remote address patch trims, drops blanks and duplicates, and preserves order', () => {
  const saved = settingsFormValues({ remote_access_addresses: ['https://old.example'] });
  const draft = {
    ...saved,
    remote_access_addresses: ' https://a.example \n\nhttps://b.example\nhttps://a.example\n https://c.example ',
  };
  assert.deepEqual(buildSettingsPatch(saved, draft, {}), {
    remote_access_addresses: ['https://a.example', 'https://b.example', 'https://c.example'],
  });
  const same = settingsFormValues({ remote_access_addresses: ['https://a.example', 'https://b.example'] });
  assert.deepEqual(
    buildSettingsPatch(same, { ...same, remote_access_addresses: 'https://a.example\n\nhttps://b.example' }, {}),
    {},
  );
  assert.deepEqual(
    buildSettingsPatch(same, { ...same, remote_access_addresses: '' }, {}),
    { remote_access_addresses: [] },
  );
});

test('filterSettingsFields matches label, hint, keywords, and key', () => {
  assert.equal(filterSettingsFields('').length, SETTINGS_FIELDS.length);
  assert.equal(filterSettingsFields('   ').length, SETTINGS_FIELDS.length);
  const embedding = filterSettingsFields('embedding');
  assert.deepEqual(embedding.map((item) => item.key), ['embedding_model']);
  assert.deepEqual(filterSettingsFields('EmBeDdInG').map((item) => item.key), ['embedding_model']);
  assert.ok(filterSettingsFields('slow_down').some((item) => item.key === 'slow_down'));
  assert.ok(filterSettingsFields(field('user_prompt').hint.slice(0, 12)).some((item) => item.key === 'user_prompt'));
});

test('modelOptionsForRole image includes a gpt-image id and excludes a plain chat model', () => {
  const models = [
    { provider_id: 'openai', id: 'gpt-4.1', kind: 'chat', provider_name: 'OpenAI', context: 128000 },
    { provider_id: 'openai', id: 'gpt-image-1', kind: 'chat', provider_name: 'OpenAI', context: 4096 },
    { provider_id: 'openrouter', id: 'flux-custom', kind: 'image', provider_name: 'OpenRouter' },
    { provider_id: 'google', id: 'gemini-2.5-flash', kind: 'chat', provider_name: 'Google' },
    { provider_id: 'google', id: 'gemini-2.5-flash-image', kind: 'chat', provider_name: 'Google' },
  ];
  const options = modelOptionsForRole(models, 'image');
  assert.deepEqual(options.map((option) => option.value), [
    'openai:gpt-image-1',
    'openrouter:flux-custom',
    'google:gemini-2.5-flash-image',
  ]);
  assert.equal(options[0].label, 'gpt-image-1');
  assert.equal(options[0].providerName, 'OpenAI');
  assert.equal(options[0].context, 4096);
  assert.equal(options[1].context, 0);
  assert.equal(options.some((option) => option.value === 'openai:gpt-4.1'), false);
});

test('modelOptionsForRole filters chat, embedding, media, and generator roles', () => {
  const models = [
    { provider_id: 'a', id: 'chat-plain', provider_name: 'A', context: 1000 },
    { provider_id: 'a', id: 'chat-kind', kind: 'chat', provider_name: 'A' },
    { provider_id: 'a', id: 'embed', kind: 'embedding', provider_name: 'A' },
    { provider_id: 'a', id: 'see', kind: 'chat', vision: true, provider_name: 'A' },
    { provider_id: 'a', id: 'hear', kind: 'chat', audio: true, provider_name: 'A' },
    { provider_id: 'a', id: 'watch', kind: 'chat', video: true, provider_name: 'A' },
    { provider_id: 'a', id: 'watch-flag', kind: 'chat', video_gen: true, provider_name: 'A' },
    { provider_id: 'a', id: 'veo-3', kind: 'chat', provider_name: 'A' },
    { provider_id: 'a', id: 'clip', kind: 'video', provider_name: 'A' },
    { provider_id: 'a', id: 'tts-1', kind: 'chat', tts: true, provider_name: 'A' },
    { provider_id: 'a', id: 'other', kind: 'rerank', provider_name: 'A' },
  ];
  const ids = (role) => modelOptionsForRole(models, role).map((option) => option.value);
  assert.deepEqual(ids('chat'), ['a:chat-plain', 'a:chat-kind', 'a:see', 'a:hear', 'a:watch', 'a:watch-flag', 'a:veo-3', 'a:tts-1']);
  assert.deepEqual(ids('embedding'), ['a:embed']);
  assert.deepEqual(ids('vision'), ['a:see']);
  assert.deepEqual(ids('audio'), ['a:hear']);
  assert.deepEqual(ids('video'), ['a:watch', 'a:watch-flag']);
  assert.deepEqual(ids('video_gen'), ['a:veo-3', 'a:clip']);
  assert.deepEqual(ids('tts'), ['a:tts-1']);
  assert.deepEqual(ids('unknown'), []);
});

test('describeSettingValue and diffSettings review non-secret changes', () => {
  assert.equal(describeSettingValue('repeated_tool_limit', 0), 'Disabled');
  assert.equal(describeSettingValue('repeated_tool_limit', 3), '3');
  assert.equal(describeSettingValue('max_auto_continues', 0), 'Unlimited');
  assert.equal(describeSettingValue('slow_down', 0), 'Off');
  assert.equal(describeSettingValue('slow_down', 2), '2');
  assert.equal(describeSettingValue('compaction_threshold', 0), 'Auto · 80% of the model window');
  assert.equal(describeSettingValue('compaction_summary_max_tokens', 0), 'Default');
  assert.equal(describeSettingValue('compaction_summary_min_chars', 0), 'Default');
  assert.equal(describeSettingValue('learner_nudge_interval', 0), 'Disabled');
  assert.equal(describeSettingValue('prompt_caching', true), 'On');
  assert.equal(describeSettingValue('prompt_caching', false), 'Off');
  assert.equal(describeSettingValue('compaction_model', ''), 'Default');
  assert.equal(describeSettingValue('delegate_model', ''), 'Default');
  assert.equal(describeSettingValue('review_model', ''), 'Default');
  assert.equal(describeSettingValue('embedding_model', ''), 'Default');
  assert.equal(describeSettingValue('vision_model', ''), 'Not set');
  assert.equal(describeSettingValue('audio_model', ''), 'Not set');
  assert.equal(describeSettingValue('video_model', ''), 'Not set');
  assert.equal(describeSettingValue('image_model', ''), 'Not set');
  assert.equal(describeSettingValue('video_gen_model', ''), 'Not set');
  assert.equal(describeSettingValue('tts_model', ''), 'Not set');
  assert.equal(describeSettingValue('user_prompt', ''), 'Not set');
  assert.equal(describeSettingValue('web_answer_provider', ''), 'Not set');
  assert.equal(describeSettingValue('remote_access_addresses', ''), 'Not set');
  assert.equal(describeSettingValue('stt_offline_model', ''), 'Default');
  assert.equal(describeSettingValue('stt_offline_language', ''), 'Default');
  assert.equal(describeSettingValue('plugin_contract_mode', ''), 'Default');
  assert.equal(describeSettingValue('web_search_strategy', ''), 'Default');
  assert.equal(describeSettingValue('web_search_strategy', 'auto'), 'Auto — merge all sources');
  assert.equal(describeSettingValue('web_answer_model', ''), 'Default');
  assert.equal(describeSettingValue('project_memory_base', ''), 'Default');
  assert.equal(describeSettingValue('temperature', null), 'Default');
  assert.equal(describeSettingValue('temperature', 0.7), '0.7');
  assert.equal(describeSettingValue('web_search_strategy', 'brave'), 'Brave');
  assert.equal(describeSettingValue('compaction_workflow', 'reuse'), 'Reuse');
  assert.equal(describeSettingValue('plugin_contract_mode', 'hint'), 'Hint');
  assert.equal(describeSettingValue('stt_offline_language', 'en'), 'English');
  assert.equal(describeSettingValue('delegate_model', 'p:gpt'), 'p:gpt');
  assert.equal(describeSettingValue('remote_access_addresses', 'https://a.example\nhttps://b.example'), 'https://a.example, https://b.example');

  const saved = settingsFormValues({ max_tool_rounds: 8, repeated_tool_limit: 3, prompt_caching: false });
  const draft = { ...saved, max_tool_rounds: 9, repeated_tool_limit: 0, web_answer_api_key: 'new-secret' };
  assert.deepEqual(diffSettings(saved, draft), [
    {
      key: 'max_tool_rounds',
      section: 'agent',
      label: 'Maximum tool rounds',
      before: '8',
      after: '9',
    },
    {
      key: 'repeated_tool_limit',
      section: 'agent',
      label: 'Repeated tool limit',
      before: '3',
      after: 'Disabled',
    },
  ]);
});

test('formatBytes uses B KB MB GB', () => {
  assert.equal(formatBytes(0), '');
  assert.equal(formatBytes(Number.NaN), '');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(1536), '1.5 KB');
  assert.equal(formatBytes(10 * 1024), '10 KB');
  assert.equal(formatBytes(1024 * 1024), '1.0 MB');
  assert.equal(formatBytes(5 * 1024 * 1024 * 1024), '5.0 GB');
});

test('observeInstallSpeed resets on phase change and smooths bytes per second', () => {
  const probe = { phase: 'binary', fetched: 10, at: 0, speedEma: 50 };
  assert.equal(observeInstallSpeed(probe, 'model', 20, 500), 0);
  assert.equal(probe.phase, 'model');
  assert.equal(probe.fetched, 20);
  assert.equal(probe.at, 500);
  assert.equal(probe.speedEma, 0);

  assert.equal(observeInstallSpeed(probe, 'model', 1020, 1500), 1000);
  assert.equal(probe.fetched, 1020);
  assert.equal(probe.at, 1500);
  assert.equal(probe.speedEma, 1000);

  assert.equal(observeInstallSpeed(probe, 'model', 9000, 1600), 1000);
  assert.equal(probe.fetched, 1020);
  assert.equal(probe.at, 1500);

  const rounded = { phase: 'model', fetched: 0, at: 0, speedEma: 1 };
  assert.equal(observeInstallSpeed(rounded, 'model', 100, 1000), 41);
  assert.ok(Math.abs(rounded.speedEma - 40.6) < 1e-9);
});

test('capabilityTiles report configured media, always-on search, and offline install state', () => {
  const base = settingsFormValues({});
  const tiles = capabilityTiles(base, {});
  assert.deepEqual(tiles.map((tile) => tile.id), [
    'vision',
    'audio',
    'video',
    'offline-stt',
    'image',
    'video-gen',
    'speech',
    'offline-tts',
    'web-search',
    'web-answer',
    'embeddings',
    'prompt-caching',
  ]);

  const vision = tiles.find((tile) => tile.id === 'vision');
  assert.equal(vision.state, 'off');
  assert.equal(vision.detail, 'Not set');
  assert.equal(vision.field, 'vision_model');
  assert.equal(vision.section, 'media');
  assert.equal(vision.label, 'Vision');

  const enabled = capabilityTiles(settingsFormValues({
    vision_provider_id: 'openai',
    vision_model_id: 'gpt-4o',
    web_answer_provider: 'brave',
    embedding_provider_id: 'ollama',
    embedding_model_id: 'nomic-embed-text',
    prompt_caching: true,
  }), { sttReady: true, ttsReady: false, sttInstalled: true });
  const visionOn = enabled.find((tile) => tile.id === 'vision');
  assert.equal(visionOn.state, 'on');
  assert.equal(visionOn.detail, 'gpt-4o');

  const search = tiles.find((tile) => tile.id === 'web-search');
  assert.equal(search.state, 'on');
  assert.equal(search.detail, 'Auto');
  assert.equal(search.field, 'web_search_strategy');
  const pinned = capabilityTiles({ ...base, web_search_strategy: 'round_robin' }, {});
  assert.equal(pinned.find((tile) => tile.id === 'web-search').state, 'on');
  assert.equal(pinned.find((tile) => tile.id === 'web-search').detail, 'Round robin');
  const namedAuto = capabilityTiles({ ...base, web_search_strategy: 'auto' }, {});
  assert.equal(namedAuto.find((tile) => tile.id === 'web-search').detail, 'Auto');

  const answer = tiles.find((tile) => tile.id === 'web-answer');
  assert.equal(answer.state, 'off');
  assert.equal(answer.detail, 'Not set');
  assert.equal(enabled.find((tile) => tile.id === 'web-answer').state, 'on');
  assert.equal(enabled.find((tile) => tile.id === 'web-answer').detail, 'Brave');

  const embeddings = tiles.find((tile) => tile.id === 'embeddings');
  assert.equal(embeddings.state, 'off');
  assert.equal(embeddings.section, 'models');
  assert.equal(embeddings.field, 'embedding_model');
  assert.equal(enabled.find((tile) => tile.id === 'embeddings').state, 'on');
  assert.equal(enabled.find((tile) => tile.id === 'embeddings').detail, 'nomic-embed-text');

  const caching = tiles.find((tile) => tile.id === 'prompt-caching');
  assert.equal(caching.field, '');
  assert.equal(caching.section, 'context');
  assert.equal(caching.state, 'off');
  assert.equal(caching.detail, 'Off');
  assert.equal(enabled.find((tile) => tile.id === 'prompt-caching').state, 'on');
  assert.equal(enabled.find((tile) => tile.id === 'prompt-caching').detail, 'On');

  const stt = tiles.find((tile) => tile.id === 'offline-stt');
  assert.equal(stt.state, 'install');
  assert.equal(stt.detail, 'Not installed');
  assert.equal(stt.field, '');
  assert.equal(enabled.find((tile) => tile.id === 'offline-stt').state, 'on');
  assert.equal(enabled.find((tile) => tile.id === 'offline-stt').detail, 'Ready');
  const installedOnly = capabilityTiles(base, { sttInstalled: true, sttReady: false });
  assert.equal(installedOnly.find((tile) => tile.id === 'offline-stt').state, 'install');
  assert.equal(installedOnly.find((tile) => tile.id === 'offline-stt').detail, 'Installed');

  const tts = tiles.find((tile) => tile.id === 'offline-tts');
  assert.equal(tts.state, 'install');
  assert.equal(tts.detail, 'Not installed');
  assert.equal(tts.field, '');
  const ttsReady = capabilityTiles(base, { ttsReady: true });
  assert.equal(ttsReady.find((tile) => tile.id === 'offline-tts').state, 'on');
  assert.equal(ttsReady.find((tile) => tile.id === 'offline-tts').detail, 'Ready');

  const speech = tiles.find((tile) => tile.id === 'speech');
  assert.equal(speech.state, 'off');
  assert.equal(speech.field, 'tts_model');
  const speaking = capabilityTiles(settingsFormValues({
    tts_provider_id: 'openai',
    tts_model_id: 'tts-1',
  }), {});
  assert.equal(speaking.find((tile) => tile.id === 'speech').state, 'on');
  assert.equal(speaking.find((tile) => tile.id === 'speech').detail, 'tts-1');
});

test('modelLabel resolves catalogue ids and leaves unknown values raw', () => {
  const models = [
    { provider_id: 'openai', id: 'gpt-4o', provider_name: 'OpenAI' },
    { provider_id: 'ollama', id: 'nomic-embed-text:latest', provider_name: '' },
    { provider_id: 'local', id: 'bare-model' },
  ];
  assert.equal(modelLabel(models, ''), '');
  assert.equal(modelLabel(models, null), '');
  assert.equal(modelLabel(models, 'openai:gpt-4o'), 'gpt-4o · OpenAI');
  assert.equal(modelLabel(models, 'ollama:nomic-embed-text:latest'), 'nomic-embed-text:latest');
  assert.equal(modelLabel(models, 'local:bare-model'), 'bare-model');
  assert.equal(modelLabel(models, 'missing:gpt'), 'missing:gpt');
  assert.equal(modelLabel(models, 'gpt-4o'), 'gpt-4o');
  assert.equal(modelLabel(null, 'openai:gpt-4o'), 'openai:gpt-4o');
  assert.equal(modelLabel([], 'openai:gpt-4o'), 'openai:gpt-4o');
});

test('the settings model stays a pure module', async () => {
  const source = await readFile(new URL('../page/settings/model.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /frontend\/js\/views\/settings/);
  assert.doesNotMatch(source, /\bfrom\s+['"]/);
  assert.doesNotMatch(source, /\bdocument\b/);
  assert.doesNotMatch(source, /\bwindow\./);
});
