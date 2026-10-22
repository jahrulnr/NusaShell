import { on, rpc as defaultRPC } from '../../js/rpc.js';
import { confirmDialog, createSelect, el, toast } from '../../js/ui.js';
import { FONT_OPTIONS, readFontPreference, setFontPreference } from '../../js/font-preferences.js';
import { isPairingHostOnly, revokeAllSessions } from '../../js/pairing.js';
import {
  SETTINGS_FIELDS,
  SETTINGS_SECTIONS,
  buildSettingsPatch,
  capabilityTiles,
  describeSettingValue,
  diffSettings,
  filterSettingsFields,
  formatBytes,
  modelLabel,
  modelOptionsForRole,
  observeInstallSpeed,
  settingsFormValues,
  validateSettingsField,
} from './model.js';

const ANSWER_MODEL_PLACEHOLDER = {
  openrouter: 'openrouter/auto',
  openai: 'gpt-5.6-luna',
  perplexity: 'low',
  anthropic: 'claude-haiku-4-5',
  xai: 'grok-4.5',
};

const STT_GUIDES = {
  linux: 'git clone https://github.com/ggml-org/whisper.cpp\ncd whisper.cpp && cmake -B build && cmake --build build -j --config Release\nsudo cp build/bin/whisper-cli /usr/local/bin/',
  windows: 'Download whisper-bin-x64.zip from the whisper.cpp GitHub releases, extract it, and add the folder to PATH.',
  macos: 'brew install whisper-cpp',
};

const FIELD_BY_KEY = new Map(SETTINGS_FIELDS.map((field) => [field.key, field]));

function fieldByKey(key) {
  return FIELD_BY_KEY.get(key);
}

function addresses(value) {
  const lines = String(value ?? '').split('\n');
  const seen = new Set();
  const out = [];
  for (const line of lines) {
    const address = line.trim();
    if (!address || seen.has(address)) continue;
    seen.add(address);
    out.push(address);
  }
  return out;
}

function preferredModel() {
  try {
    return localStorage.getItem('nusashell.model') || '';
  } catch {
    return '';
  }
}

function storePreferredModel(value) {
  try {
    if (value) localStorage.setItem('nusashell.model', value);
    else localStorage.removeItem('nusashell.model');
  } catch {
    /* this browser blocked storage */
  }
}

function typingTarget(target) {
  const tag = target?.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target?.isContentEditable;
}

export function createSettingsPage(root = document, { rpcClient = defaultRPC, pairingRefresh = null } = {}) {
  let saved = settingsFormValues({});
  let draft = settingsFormValues({});
  let secrets = {};
  let ready = false;
  let destroyed = false;
  let rendering = false;
  let loaded = false;
  let sttTimer = 0;
  let sttPollGeneration = 0;
  let petStarting = false;
  let sectionObserver = null;
  let spyHold = 0;
  const selects = new Map();
  const listeners = [];
  const probes = {
    stt: { phase: '', fetched: 0, at: 0, speedEma: 0 },
    tts: { phase: '', fetched: 0, at: 0, speedEma: 0 },
    pet: { phase: '', fetched: 0, at: 0, speedEma: 0 },
  };
  const state = { models: [], info: null, stt: null, tts: null, pets: null, focusPet: false };

  function find(id) {
    return root.querySelector?.(`#${id}`) || null;
  }

  function listen(target, type, handler, options) {
    if (!target) return;
    target.addEventListener(type, handler, options);
    listeners.push(() => target.removeEventListener(type, handler, options));
  }

  function call(method, params = {}) {
    return rpcClient(method, params, { timeoutMs: 30000 });
  }

  function installerExtras() {
    return {
      sttReady: state.stt?.ready === true,
      sttInstalled: state.stt?.engine_installed === true,
      ttsReady: state.tts?.ready === true,
    };
  }

  function isDirty() {
    return diffSettings(saved, draft).length > 0 || Object.keys(secrets).length > 0;
  }

  function setSync(copy, tone) {
    const node = find('settings-sync-state');
    if (!node) return;
    node.textContent = copy;
    node.dataset.state = tone;
  }

  function showBanner(message, actions = []) {
    const banner = find('settings-disk-banner');
    if (!banner) return;
    banner.hidden = false;
    banner.replaceChildren(el('span', { text: message }), ...actions);
  }

  function hideBanner() {
    const banner = find('settings-disk-banner');
    if (!banner) return;
    banner.hidden = true;
    banner.replaceChildren();
  }

  function mountSelect(select, data, value, onChange) {
    try {
      return createSelect(select, {
        data,
        value,
        placeholder: data[0]?.text || '',
        search: data.length > 7,
        onChange: (next) => {
          if (!rendering) onChange(next);
        },
      });
    } catch {
      select.replaceChildren(...data.map((item) => el('option', { value: item.value, text: item.text })));
      select.value = value || '';
      select.addEventListener('change', () => {
        if (!rendering) onChange(select.value);
      });
      return {
        getSelected: () => [select.value],
        setSelected: (next) => { select.value = next?.[0] || ''; },
        destroy() {},
      };
    }
  }

  function rememberSelect(key, select, data, value, onChange) {
    try { selects.get(key)?.destroy?.(); } catch { /* already gone */ }
    selects.set(key, mountSelect(select, data, value, onChange));
  }

  function destroySelects() {
    for (const instance of selects.values()) {
      try { instance.destroy?.(); } catch { /* already gone */ }
      }
    selects.clear();
  }

  function choiceData(field) {
    if (field.type === 'model') {
      return [
        { text: describeSettingValue(field.key, ''), value: '' },
        ...modelOptionsForRole(state.models, field.role).map((option) => ({
          text: [option.label, option.context ? `${Math.round(option.context / 1000)}K` : '', option.providerName].filter(Boolean).join(' · '),
          value: option.value,
        })),
      ];
    }
    if (field.key === 'stt_offline_model') {
      return [
        { text: 'Auto — first installed model', value: '' },
        ...(state.stt?.models || []).filter((model) => model.installed).map((model) => ({
          text: model.label || model.id,
          value: model.id,
        })),
      ];
    }
    return (field.options || []).map((option) => ({ text: option.label, value: option.value }));
  }

  function sameDraft(field, value) {
    const current = draft[field.key];
    if (field.type === 'sampling') {
      if (current == null || value == null) return current == null && value == null;
      return Number(current) === Number(value);
    }
    if (field.type === 'number') return Number.isFinite(current) && Number.isFinite(value) && current === value;
    return current === value;
  }

  function setDraft(key, value) {
    const field = fieldByKey(key);
    if (!field || sameDraft(field, value)) return;
    draft[key] = value;
    if (key === 'compaction_workflow' && value === 'reuse' && draft.compaction_model) {
      draft.compaction_model = '';
      selects.get('compaction_model')?.setSelected?.(['']);
      paintField('compaction_model');
    }
    paintField(key);
    if (!rendering) updateChrome();
  }

  function stepField(key, delta) {
    const field = fieldByKey(key);
    if (!field) return;
    let current = draft[key];
    if (typeof current !== 'number' || !Number.isFinite(current)) current = field.min ?? 0;
    let next = current + delta;
    if (field.integer) next = Math.round(next * 1000) / 1000;
    if (field.integer && field.step && field.step >= 1) next = Math.round(next);
    if (field.min != null) next = Math.max(field.min, next);
    if (field.max != null) next = Math.min(field.max, next);
    setDraft(key, next);
  }

  function paintField(key) {
    const field = fieldByKey(key);
    if (!field) return;
    const changed = diffSettings(saved, draft).some((change) => change.key === key) || Object.hasOwn(secrets, key);
    const error = field.secret ? '' : validateSettingsField(key, draft[key], draft);
    if (key === 'remote_access_addresses') {
      const input = find('pairing-remote-address');
      const errorNode = find('pairing-address-error');
      if (input && document.activeElement !== input) input.value = draft[key] ?? '';
      if (errorNode) {
        errorNode.hidden = !(changed && error);
        errorNode.textContent = error;
      }
      return;
    }
    if (key === 'remote_access_enabled') {
      const box = find('settings-remote-access-enabled');
      if (box && document.activeElement !== box) box.checked = draft[key] === true;
      return;
    }
    const wrap = root.querySelector?.(`[data-setting-field="${key}"]`);
    if (!wrap) return;
    wrap.classList.toggle('is-dirty', changed);
    wrap.classList.toggle('is-invalid', Boolean(changed && error));
    const errorNode = wrap.querySelector('[data-error]');
    if (errorNode) {
      errorNode.hidden = !(changed && error);
      errorNode.textContent = error;
    }
    const meaning = wrap.querySelector('[data-meaning]');
    if (meaning) {
      const show = Boolean(field.zeroMeaning) && Number(draft[key]) === 0;
      meaning.hidden = !show;
      meaning.textContent = show ? field.zeroMeaning : '';
    }
    const input = wrap.querySelector('[data-setting]');
    if (input && document.activeElement !== input) {
      if (field.type === 'boolean') input.checked = draft[key] === true;
      else if (field.type === 'sampling') input.value = draft[key] == null ? '' : String(draft[key]);
      else if (field.type === 'number') input.value = String(draft[key] ?? '');
      else if (field.type === 'text' || field.type === 'textarea') input.value = draft[key] ?? '';
    }
    if (field.type === 'segmented') {
      wrap.querySelectorAll('[data-choice]').forEach((button) => {
        const on = button.dataset.value === String(draft[key] ?? '');
        button.setAttribute('aria-pressed', String(on));
        button.classList.toggle('is-selected', on);
      });
    }
    if (field.type === 'model' || field.type === 'select') {
      const instance = selects.get(key);
      const current = instance?.getSelected?.()?.[0] ?? '';
      const next = draft[key] ?? '';
      if (instance && current !== next) instance.setSelected([next]);
      if (key === 'compaction_model') {
        const select = wrap.querySelector('select');
        const disabled = draft.compaction_workflow === 'reuse';
        if (select) select.disabled = disabled;
        const note = wrap.querySelector('[data-reuse-note]');
        if (note) note.hidden = !disabled;
      }
    }
    if (key === 'user_prompt') {
      const count = wrap.querySelector('[data-count]');
      if (count) count.textContent = `${String(draft[key] ?? '').length} characters`;
      const warn = wrap.querySelector('[data-cache-warn]');
      if (warn) warn.hidden = !changed;
    }
  }

  function paintOverview() {
    const tilesHost = find('settings-overview-tiles');
    const summaryHost = find('settings-overview-summary');
    if (!tilesHost || !summaryHost) return;
    const tiles = capabilityTiles(draft, installerExtras());
    tilesHost.replaceChildren(...tiles.map((tile) => el('button', {
      type: 'button',
      class: `settings-tile settings-tile-${tile.state}`,
      dataset: { capability: tile.id, state: tile.state },
    }, el('span', { text: tile.label }), el('strong', { text: tile.detail }))));
    const info = state.info || {};
    const delegate = modelLabel(state.models, draft.delegate_model) || describeSettingValue('delegate_model', draft.delegate_model);
    summaryHost.replaceChildren(
      el('p', { class: 'settings-copy settings-overview-delegate', text: `Delegate ${delegate} · ${describeSettingValue('max_tool_rounds', draft.max_tool_rounds)} tool rounds · ${describeSettingValue('compaction_workflow', draft.compaction_workflow)} compaction` }),
      el('div', { class: 'settings-system' },
        el('div', { class: 'settings-system-item settings-system-version' },
          el('span', { class: 'settings-system-label', text: 'Version' }),
          el('strong', { class: 'settings-system-value', text: info.version || '—' }),
        ),
        el('div', { class: 'settings-system-item settings-system-directory' },
          el('span', { class: 'settings-system-label', text: 'Data directory' }),
          el('div', { class: 'settings-system-location' },
            el('span', { id: 'settings-data-dir', text: info.data_dir || '—' }),
            el('button', { type: 'button', id: 'settings-copy-data-dir', dataset: { action: 'copy-data-dir' }, text: 'Copy' }),
          ),
        ),
        el('div', { class: 'settings-system-item settings-system-remote', dataset: { state: draft.remote_access_enabled ? 'on' : 'off' } },
          el('span', { class: 'settings-system-label', text: 'Remote access' }),
          el('strong', { class: 'settings-system-value', text: draft.remote_access_enabled ? 'On' : 'Off' }),
        ),
      ),
    );
  }

  function progressNode(kind) {
    return el('div', { class: 'settings-progress', id: `settings-${kind}-progress`, hidden: true },
      el('span', { dataset: { progressMessage: kind } }),
    );
  }

  function sttCard() {
    const status = state.stt || {};
    const installed = (status.models || []).filter((model) => model.installed).length;
    const showGuide = status.supported === false && !status.engine_installed;
    const guideTabs = ['linux', 'windows', 'macos'].map((os, index) => el('button', {
      type: 'button',
      class: `settings-segment-button${index === 0 ? ' is-selected' : ''}`,
      dataset: { sttGuide: os },
      text: os,
    }));
    const guidePanels = Object.entries(STT_GUIDES).map(([os, command], index) => el('pre', {
      class: 'settings-guide',
      dataset: { sttGuidePanel: os },
      hidden: index !== 0,
      text: command,
    }));
    return el('article', { class: 'settings-card', id: 'settings-stt-card', dataset: { searchText: 'offline speech stt whisper install' } },
      el('h3', { text: 'Offline speech-to-text' }),
      el('p', { class: 'settings-copy', id: 'settings-stt-status', text: status.ready ? 'Offline speech-to-text is ready.' : 'Offline speech-to-text is not installed.' }),
      el('ul', { class: 'settings-checklist' },
        el('li', { text: `Platform · ${status.supported ? 'Official release' : 'Manual install'}` }),
        el('li', { text: `Engine · ${status.engine_installed ? 'Installed' : 'Not installed'}` }),
        el('li', { text: `Models · ${installed} installed` }),
        el('li', { text: `Disk · ${formatBytes(status.disk_free_bytes) || 'Unknown'}` }),
      ),
      el('div', { hidden: !showGuide }, ...guideTabs, ...guidePanels),
      el('select', { id: 'settings-stt-install-model', dataset: { extraSelect: 'stt-install' } }),
      el('div', { class: 'settings-inline-actions' },
        el('button', { type: 'button', dataset: { action: 'stt-install' }, text: 'Install' }),
        el('button', { type: 'button', dataset: { action: 'stt-stop' }, hidden: !status.running, text: 'Stop' }),
      ),
      progressNode('stt'),
    );
  }

  function ttsCard() {
    const status = state.tts || {};
    const voices = (status.voices || []).filter((voice) => voice.installed).length;
    const voiceLabel = `${voices} ${voices === 1 ? 'voice' : 'voices'}`;
    return el('article', { class: 'settings-card', id: 'settings-tts-card', dataset: { searchText: 'offline speech tts piper voice install' } },
      el('h3', { text: 'Offline text-to-speech' }),
      el('p', { class: 'settings-copy', id: 'settings-tts-status', text: status.ready ? `Offline speech ready · ${voiceLabel}` : 'Offline text-to-speech is not installed.' }),
      el('select', { id: 'settings-tts-install-voice', dataset: { extraSelect: 'tts-install' } }),
      el('div', { class: 'settings-inline-actions' },
        el('button', { type: 'button', dataset: { action: 'tts-install' }, text: 'Install' }),
      ),
      progressNode('tts'),
    );
  }

  function petCard() {
    const pets = state.pets || {};
    let label = 'Checking the desktop pet…';
    let action = 'Install';
    let disabled = false;
    if (pets.supported === false) {
      label = 'Desktop pet requires Linux.';
      action = 'Unavailable';
      disabled = true;
    } else if (pets.install_active) {
      label = 'Install in progress.';
      action = 'Installing…';
      disabled = true;
    } else if (pets.installed) {
      label = `${pets.running ? 'Running' : 'Installed'}${pets.version ? ` · ${pets.version}` : ''}`;
      action = pets.running ? 'Stop' : 'Launch';
    } else if (state.pets) {
      label = 'Not installed.';
    }
    return el('article', { class: 'settings-card', id: 'settings-pet-card', dataset: { searchText: 'pet desktop launch install' } },
      el('h3', { text: 'Desktop pet' }),
      el('p', { class: 'settings-copy', id: 'settings-pet-status', text: label }),
      el('button', { type: 'button', id: 'settings-pet-action', dataset: { action: 'pet' }, disabled, text: action }),
      progressNode('pet'),
    );
  }

  function renderField(field) {
    const wrap = el('div', { class: 'settings-field', dataset: { settingField: field.key } });
    if (field.key === 'web_answer_api_key') wrap.dataset.answerExtra = 'key';
    if (field.key === 'web_answer_model') wrap.dataset.answerExtra = 'model';
    wrap.append(el('span', { class: 'settings-field-label', text: field.label }));
    if (field.type === 'number' || field.type === 'sampling') {
      wrap.append(el('div', { class: 'settings-stepper' },
        el('button', { type: 'button', dataset: { stepKey: field.key, step: String(-(field.step || 1)) }, text: '−', 'aria-label': `Decrease ${field.label}` }),
        el('input', {
          type: 'number',
          id: `settings-field-${field.key}`,
          dataset: { setting: field.key },
          min: field.min ?? undefined,
          max: field.max ?? undefined,
          step: field.step ?? 1,
        }),
        el('button', { type: 'button', dataset: { stepKey: field.key, step: String(field.step || 1) }, text: '+', 'aria-label': `Increase ${field.label}` }),
        el('button', { type: 'button', class: 'settings-reset', dataset: { reset: field.key }, text: 'Reset' }),
      ));
      wrap.append(el('span', { class: 'settings-value-meaning', dataset: { meaning: field.key } }));
    } else if (field.type === 'boolean') {
      wrap.append(el('label', { class: 'settings-toggle' },
        el('input', { type: 'checkbox', id: `settings-field-${field.key}`, dataset: { setting: field.key } }),
        el('span', { class: 'settings-toggle-track', 'aria-hidden': 'true' }),
      ));
    } else if (field.type === 'segmented') {
      wrap.append(el('div', { class: 'settings-segment', role: 'group', 'aria-label': field.label },
        (field.options || []).map((option) => el('button', {
          type: 'button',
          class: 'settings-segment-button',
          dataset: { choice: field.key, value: option.value },
          text: option.label,
        })),
      ));
    } else if (field.type === 'textarea') {
      wrap.append(el('textarea', { id: `settings-field-${field.key}`, dataset: { setting: field.key }, rows: '5', spellcheck: 'false' }));
      wrap.append(el('span', { dataset: { count: field.key } }));
      if (field.key === 'user_prompt') {
        wrap.append(el('span', { class: 'settings-cache-warn', dataset: { cacheWarn: '1' }, hidden: true, text: 'Saving this changes the prompt cache for later turns.' }));
      }
    } else if (field.type === 'secret') {
      wrap.append(el('div', { class: 'settings-secret-row' },
        el('input', { type: 'password', autocomplete: 'off', dataset: { secret: field.key }, placeholder: 'Leave blank to keep the stored key' }),
        el('button', { type: 'button', dataset: { secretToggle: field.key }, text: 'Show' }),
        el('button', { type: 'button', dataset: { secretClear: field.key }, text: 'Clear stored key' }),
      ));
    } else if (field.type === 'model' || field.type === 'select') {
      wrap.append(el('select', { id: `settings-field-${field.key}`, dataset: { selectKey: field.key } }));
      if (field.key === 'compaction_model') {
        wrap.append(el('span', { class: 'settings-reuse-note', dataset: { reuseNote: '1' }, hidden: true, text: 'Reuse always uses the conversation model.' }));
      }
    } else {
      wrap.append(el('input', { type: 'text', id: `settings-field-${field.key}`, dataset: { setting: field.key }, spellcheck: 'false' }));
    }
    wrap.append(el('small', { class: 'settings-hint', text: field.hint }));
    wrap.append(el('span', { class: 'settings-field-error', dataset: { error: field.key }, hidden: true }));
    return wrap;
  }

  function renderSection(section) {
    const fields = SETTINGS_FIELDS.filter((field) => field.section === section.id && field.section !== 'remote');
    const main = fields.filter((field) => !field.advanced);
    const advanced = fields.filter((field) => field.advanced);
    const body = [el('h2', { class: 'settings-section-title', text: section.label })];
    if (section.id === 'models') {
      body.push(el('div', { class: 'settings-field', dataset: { searchText: 'default model browser preferred' } },
        el('span', { class: 'settings-field-label', text: 'Default model' }),
        el('select', { id: 'settings-preferred-model', dataset: { extraSelect: 'preferred-model' } }),
        el('small', { class: 'settings-hint', text: 'Used for new conversations in this browser.' }),
      ));
    }
    body.push(...main.map(renderField));
    if (advanced.length) {
      body.push(el('details', { class: 'settings-advanced' },
        el('summary', { text: section.id === 'agent' ? 'Sampling' : 'Advanced' }),
        ...advanced.map(renderField),
      ));
    }
    if (section.id === 'media') body.push(sttCard(), ttsCard());
    if (section.id === 'interface') {
      body.push(el('div', { class: 'settings-card', dataset: { searchText: 'font interface plex inter atkinson source' } },
        el('h3', { text: 'Interface font' }),
        el('div', { class: 'settings-segment', role: 'group', 'aria-label': 'Interface font' },
          FONT_OPTIONS.map((option) => el('button', {
            type: 'button',
            class: 'settings-segment-button',
            dataset: { fontId: option.id },
            text: option.label,
          })),
        ),
        el('p', { class: 'settings-font-specimen', id: 'settings-font-specimen', text: 'Aa 123' }),
      ),
      el('div', { class: 'settings-card', dataset: { searchText: 'sidebar compact icons collapse navigation rail' } },
        el('h3', { text: 'Sidebar' }),
        el('label', { class: 'settings-toggle' },
          el('span', { class: 'settings-toggle-copy' }, [
            el('strong', { text: 'Compact sidebar' }),
            el('small', { text: 'Show the navigation rail as icons only. Saved in this browser.' }),
          ]),
          el('input', { type: 'checkbox', id: 'settings-sidebar-compact' }),
          el('span', { class: 'settings-toggle-track', 'aria-hidden': 'true' }),
        ),
      ), petCard());
    }
    return el('section', {
      class: 'settings-section',
      id: `settings-panel-${section.id}`,
      dataset: { settingsPanel: section.id },
    }, body);
  }

  function bindSttInstallSelect() {
    const sttInstall = root.querySelector?.('[data-extra-select="stt-install"]');
    if (!sttInstall) return;
    const data = [
      { text: 'Select a model…', value: '' },
      ...(state.stt?.models || []).map((model) => ({
        text: `${model.label || model.id}${model.installed ? ' · installed' : ''}`,
        value: model.id,
      })),
    ];
    const current = selects.get('stt-install')?.getSelected?.()?.[0] || '';
    rememberSelect('stt-install', sttInstall, data, current, () => {});
  }

  function renderAll() {
    const panels = find('settings-panels');
    if (!panels) return;
    rendering = true;
    disconnectSectionSpy();
    destroySelects();
    const remote = find('settings-remote-group');
    // A paired remote client got PAIRING_UNAUTHORIZED: keep the section
    // suppressed instead of resurrecting dead controls on every render.
    if (remote) remote.hidden = isPairingHostOnly();
    panels.replaceChildren(
      el('section', {
        class: 'settings-section',
        id: 'settings-panel-overview',
        dataset: { settingsPanel: 'overview' },
      },
        el('h2', { class: 'settings-section-title', text: 'Overview' }),
        el('div', { class: 'settings-overview-grid', id: 'settings-overview-tiles' }),
        el('div', { class: 'settings-overview-summary', id: 'settings-overview-summary' }),
      ),
      ...SETTINGS_SECTIONS.filter((section) => section.id !== 'overview' && section.id !== 'remote').map(renderSection),
    );
    for (const field of SETTINGS_FIELDS) paintField(field.key);
    for (const select of panels.querySelectorAll('[data-select-key]')) {
      const key = select.dataset.selectKey;
      const field = fieldByKey(key);
      if (!field) continue;
      rememberSelect(key, select, choiceData(field), draft[key] || '', (next) => setDraft(key, next));
    }
    const preferred = panels.querySelector('[data-extra-select="preferred-model"]');
    if (preferred) {
      const data = [
        { text: 'Automatic — choose in each conversation', value: '' },
        ...modelOptionsForRole(state.models, 'chat').map((option) => ({
          text: option.providerName ? `${option.label} · ${option.providerName}` : option.label,
          value: option.value,
        })),
      ];
      rememberSelect('preferred-model', preferred, data, preferredModel(), (next) => storePreferredModel(next));
    }
    bindSttInstallSelect();
    const ttsInstall = panels.querySelector('[data-extra-select="tts-install"]');
    if (ttsInstall) {
      const data = [
        { text: 'Select a voice…', value: '' },
        ...(state.tts?.voices || []).map((voice) => ({
          text: `${voice.label || voice.id}${voice.installed ? ' · installed' : ''}`,
          value: voice.id,
        })),
      ];
      rememberSelect('tts-install', ttsInstall, data, '', () => {});
    }
    paintFont();
    paintSidebar();
    rendering = false;
    updateChrome();
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
    observeSections();
    if (state.focusPet) {
      state.focusPet = false;
      scrollToNode(find('settings-pet-card'));
    }
  }

  function paintFont() {
    const id = readFontPreference();
    root.querySelectorAll?.('[data-font-id]').forEach((button) => {
      const on = button.dataset.fontId === id;
      button.setAttribute('aria-pressed', String(on));
      button.classList.toggle('is-selected', on);
    });
    const specimen = find('settings-font-specimen');
    if (specimen) specimen.dataset.font = id;
  }

  // Compact-sidebar is a browser-local preference, like the font picker: the
  // settings view persists nusashell.sidebarMode and mirrors the shell's
  // setGlobalNavExpanded(!compact) so the rail reacts without a reload.
  function paintSidebar() {
    const input = find('settings-sidebar-compact');
    if (input) input.checked = localStorage.getItem('nusashell.sidebarMode') === 'icons';
  }

  function applySidebarCompact(compact) {
    localStorage.setItem('nusashell.sidebarMode', compact ? 'icons' : 'full');
    const expanded = !compact;
    const shell = document.querySelector('.shell');
    const nav = document.querySelector('#global-nav');
    const button = document.querySelector('#global-nav-expand');
    shell?.classList.toggle('global-nav-expanded', expanded);
    if (nav) nav.dataset.expanded = String(expanded);
    if (!button) return;
    button.setAttribute('aria-expanded', String(expanded));
    button.setAttribute('aria-label', expanded ? 'Collapse sidebar' : 'Expand sidebar');
    button.title = expanded ? 'Collapse sidebar' : 'Expand sidebar';
    const label = button.querySelector('.global-nav-expand-label');
    if (label) label.textContent = expanded ? 'Collapse sidebar' : 'Expand sidebar';
  }

  function reviewValue(key, described, raw) {
    const field = fieldByKey(key);
    const named = field?.type === 'model' || key === 'delegate_model' || key === 'review_model' || key === 'compaction_model';
    if (!named) return described;
    return modelLabel(state.models, raw) || described;
  }

  function paintReview() {
    const review = find('settings-review');
    if (!review || review.hidden) return;
    const rows = diffSettings(saved, draft).map((change) => el('div', { class: 'settings-review-row' },
      el('span', { text: `${change.label}: ${reviewValue(change.key, change.before, saved[change.key])} → ${reviewValue(change.key, change.after, draft[change.key])}` }),
      el('button', { type: 'button', class: 'settings-quiet-button', dataset: { revert: change.key }, text: 'Revert' }),
    ));
    for (const [key, value] of Object.entries(secrets)) {
      // Never render the raw secret — stored keys show as bullets in the
      // provider views.
      const shown = value === '' ? 'Stored key will be cleared' : '••••••••';
      rows.push(el('div', { class: 'settings-review-row' },
        el('span', { text: `${fieldByKey(key)?.label || key}: ${shown}` }),
        el('button', { type: 'button', class: 'settings-quiet-button', dataset: { revertSecret: key }, text: 'Revert' }),
      ));
    }
    review.replaceChildren(
      el('header', { class: 'settings-review-header' },
        el('strong', { text: 'Review changes' }),
        el('button', { type: 'button', class: 'settings-quiet-button', dataset: { reviewClose: 'true' }, text: 'Close' }),
      ),
      ...(rows.length ? rows : [el('p', { class: 'settings-copy', text: 'No staged changes.' })]),
    );
  }

  function updateChrome() {
    const changes = diffSettings(saved, draft);
    const total = changes.length + Object.keys(secrets).length;
    const invalid = changes.filter((change) => validateSettingsField(change.key, draft[change.key], draft));
    const bar = find('settings-savebar');
    if (bar) bar.hidden = total === 0;
    const count = find('settings-dirty-count');
    if (count) count.textContent = `${total} ${total === 1 ? 'change' : 'changes'}`;
    const save = find('settings-save');
    if (save) save.disabled = invalid.length > 0;
    const fix = find('settings-fix');
    if (fix) {
      fix.hidden = invalid.length === 0;
      fix.textContent = `Fix ${invalid.length} ${invalid.length === 1 ? 'field' : 'fields'}`;
      fix.dataset.fixKey = invalid[0]?.key || '';
    }
    document.querySelector?.('[data-global-route="settings"]')?.classList.toggle('has-draft', total > 0);
    const counts = {};
    for (const change of changes) counts[change.section] = (counts[change.section] || 0) + 1;
    for (const key of Object.keys(secrets)) {
      const section = fieldByKey(key)?.section;
      if (section) counts[section] = (counts[section] || 0) + 1;
    }
    root.querySelectorAll?.('[data-settings-section]').forEach((button) => {
      const amount = counts[button.dataset.settingsSection] || 0;
      let badge = button.querySelector('.settings-rail-badge');
      if (!badge) {
        badge = el('span', { class: 'settings-rail-badge' });
        button.append(badge);
      }
      badge.hidden = amount === 0;
      badge.textContent = String(amount);
    });
    const rawQuery = find('settings-search')?.value || '';
    const query = rawQuery.trim().toLowerCase();
    const searching = query !== '';
    const matches = new Set(filterSettingsFields(query).map((field) => field.key));
    root.querySelectorAll?.('[data-setting-field]').forEach((node) => {
      const key = node.dataset.settingField;
      let hide = searching && !matches.has(key);
      if (key === 'web_answer_api_key' && !draft.web_answer_provider) hide = true;
      if (key === 'web_answer_model' && (!draft.web_answer_provider || draft.web_answer_provider === 'brave')) hide = true;
      node.hidden = hide;
    });
    root.querySelectorAll?.('[data-search-text]').forEach((node) => {
      node.hidden = searching && !node.dataset.searchText.toLowerCase().includes(query);
    });
    root.querySelectorAll?.('[data-settings-panel]').forEach((panel) => {
      if (!searching) {
        panel.hidden = false;
        return;
      }
      if (panel.dataset.settingsPanel === 'overview') {
        panel.hidden = true;
        return;
      }
      panel.hidden = ![...panel.querySelectorAll('[data-setting-field], [data-search-text]')].some((node) => !node.hidden);
    });
    const remote = find('settings-remote-group');
    if (remote) remote.hidden = isPairingHostOnly() || (searching && !matches.has('remote_access_enabled') && !matches.has('remote_access_addresses'));
    const empty = find('settings-search-empty');
    if (empty) {
      const panels = [...(root.querySelectorAll?.('[data-settings-panel]') || [])];
      const none = searching && panels.length > 0 && panels.every((panel) => panel.hidden);
      empty.hidden = !none;
      empty.textContent = none ? `No settings match "${rawQuery.trim()}"` : '';
    }
    const answerModel = find('settings-field-web_answer_model');
    if (answerModel) answerModel.placeholder = ANSWER_MODEL_PLACEHOLDER[draft.web_answer_provider] || 'Vendor default';
    paintOverview();
    paintReview();
  }

  function scrollToNode(node) {
    if (!node?.getBoundingClientRect) return;
    const main = root.querySelector('.settings-main');
    const view = root.ownerDocument?.defaultView;
    const overflow = main && view?.getComputedStyle ? view.getComputedStyle(main).overflowY : 'visible';
    const mainScrolls = Boolean(main?.contains(node) && overflow !== 'visible' && overflow !== 'clip');
    const scroller = mainScrolls ? main : root;
    if (mainScrolls) root.scrollTop = 0;
    const top = node.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop - 12;
    const next = Math.max(0, top);
    if (typeof scroller.scrollTo === 'function') scroller.scrollTo({ top: next });
    else scroller.scrollTop = next;
  }

  function focusVisible(node) {
    if (!node) return;
    const trigger = node.matches?.('select') && node.nextElementSibling?.classList?.contains('ss-main')
      ? node.nextElementSibling
      : node;
    const field = trigger.closest?.('.settings-field, .settings-card') || trigger;
    scrollToNode(field);
    const focusTarget = trigger.matches?.('select') ? field : trigger;
    focusTarget.focus?.({ preventScroll: true });
  }

  function markSection(id) {
    root.querySelectorAll?.('[data-settings-section]').forEach((button) => {
      const on = button.dataset.settingsSection === id;
      button.classList.toggle('is-current', on);
      if (on) button.setAttribute('aria-current', 'location');
      else button.removeAttribute('aria-current');
    });
  }

  function showSection(id) {
    const target = id === 'remote' ? find('settings-remote-group') : find(`settings-panel-${id}`);
    holdSpy();
    scrollToNode(target);
    markSection(id);
  }

  function activeScroller() {
    const main = root.querySelector('.settings-main');
    const view = root.ownerDocument?.defaultView;
    const overflow = main && view?.getComputedStyle ? view.getComputedStyle(main).overflowY : 'visible';
    const mainScrolls = Boolean(main && overflow !== 'visible' && overflow !== 'clip');
    return mainScrolls ? main : root;
  }

  function holdSpy() {
    spyHold += 1;
    const view = root.ownerDocument?.defaultView;
    const release = () => { spyHold = Math.max(0, spyHold - 1); };
    if (typeof view?.requestAnimationFrame === 'function') view.requestAnimationFrame(() => view.requestAnimationFrame(release));
    else if (typeof view?.setTimeout === 'function') view.setTimeout(release, 0);
    else release();
  }

  function disconnectSectionSpy() {
    sectionObserver?.disconnect();
    sectionObserver = null;
  }

  function observeSections() {
    disconnectSectionSpy();
    const view = root.ownerDocument?.defaultView;
    if (typeof view?.IntersectionObserver !== 'function') return;
    const scroller = activeScroller();
    const panels = [...(root.querySelectorAll?.('[data-settings-panel]') || [])];
    if (!scroller || !panels.length) return;
    const seen = new Map();
    sectionObserver = new view.IntersectionObserver((entries) => {
      if (spyHold > 0) return;
      for (const entry of entries) seen.set(entry.target, entry);
      let best = null;
      let bestScore = Infinity;
      for (const entry of seen.values()) {
        if (!entry.isIntersecting || entry.target.hidden) continue;
        const top = entry.boundingClientRect.top - (entry.rootBounds?.top ?? 0);
        const score = top <= 48 ? Math.abs(top) : 1000 + top;
        if (score < bestScore) {
          bestScore = score;
          best = entry.target;
        }
      }
      const id = best?.dataset?.settingsPanel;
      if (id) markSection(id);
    }, { root: scroller, threshold: [0, 0.25, 0.5, 0.75, 1] });
    for (const panel of panels) sectionObserver.observe(panel);
  }

  function focusCapability(id) {
    const tile = capabilityTiles(draft, installerExtras()).find((item) => item.id === id);
    if (!tile) return;
    if (tile.id === 'offline-stt') {
      showSection('media');
      scrollToNode(find('settings-stt-card'));
      return;
    }
    if (tile.id === 'offline-tts') {
      showSection('media');
      scrollToNode(find('settings-tts-card'));
      return;
    }
    const key = tile.id === 'prompt-caching' ? 'prompt_caching' : tile.field;
    showSection(tile.section);
    if (!key) return;
    focusVisible(root.querySelector?.(`[data-setting="${key}"], #settings-field-${key}, [data-select-key="${key}"]`));
  }

  function paintProgress(kind, payload = {}) {
    const host = find(`settings-${kind}-progress`);
    if (!host) return;
    host.hidden = false;
    const fetched = Number(payload.bytes_fetched) || 0;
    const total = Number(payload.bytes_total) || 0;
    const speed = observeInstallSpeed(probes[kind], payload.phase || '', fetched, performance.now());
    const message = host.querySelector('[data-progress-message]');
    if (message) {
      message.textContent = [
        payload.message || payload.phase || 'Working…',
        total ? `${formatBytes(fetched)} / ${formatBytes(total)}` : '',
        speed ? `${formatBytes(speed)}/s` : '',
      ].filter(Boolean).join(' · ');
    }
  }

  function stopSttPoll() {
    sttPollGeneration += 1;
    if (sttTimer) clearInterval(sttTimer);
    sttTimer = 0;
  }

  function replaceSttCard() {
    const current = find('settings-stt-card');
    if (!current) return;
    const previous = find('settings-stt-progress');
    const message = previous?.querySelector('[data-progress-message]')?.textContent || '';
    const keepProgress = Boolean(previous && !previous.hidden && message);
    current.replaceWith(sttCard());
    if (keepProgress) {
      const host = find('settings-stt-progress');
      const node = host?.querySelector('[data-progress-message]');
      if (host && node) {
        host.hidden = false;
        node.textContent = message;
      }
    }
    bindSttInstallSelect();
  }

  function startSttPoll() {
    stopSttPoll();
    sttTimer = setInterval(() => {
      const requestGeneration = sttPollGeneration;
      void call('settings.stt_install_status', {}).then((status) => {
        if (destroyed || requestGeneration !== sttPollGeneration) return;
        state.stt = status;
        replaceSttCard();
        if (!status?.running) stopSttPoll();
      }).catch(() => {});
    }, 1500);
  }

  async function refreshInstaller(kind) {
    if (kind === 'stt') state.stt = await call('settings.stt_install_status', {});
    if (kind === 'tts') state.tts = await call('settings.tts_install_status', {});
    if (kind === 'pet') state.pets = await call('settings.pets_status', {});
    if (!destroyed) renderAll();
  }

  async function save() {
    const changes = diffSettings(saved, draft);
    const invalid = changes.filter((change) => validateSettingsField(change.key, draft[change.key], draft));
    if (invalid.length) {
      focusCapability(invalid[0].key === 'prompt_caching' ? 'prompt-caching' : '');
      focusVisible(root.querySelector?.(`[data-setting="${invalid[0].key}"]`) || find('pairing-remote-address'));
      return;
    }
    const patch = buildSettingsPatch(saved, draft, { secrets });
    if (!Object.keys(patch).length) {
      secrets = {};
      updateChrome();
      return;
    }
    const button = find('settings-save');
    if (button) button.disabled = true;
    try {
      const result = await call('settings.set', patch);
      const next = settingsFormValues(result?.settings || {});
      saved = next;
      draft = settingsFormValues(result?.settings || {});
      secrets = {};
      hideBanner();
      renderAll();
      toast('Settings saved', 'success');
    } catch (error) {
      const status = find('settings-save-error');
      if (status) status.textContent = error?.message || 'Could not save settings';
      toast(error?.message || 'Could not save settings', 'error');
      updateChrome();
    } finally {
      if (button && button.isConnected) button.disabled = diffSettings(saved, draft).some((change) => validateSettingsField(change.key, draft[change.key], draft));
    }
  }

  async function load({ force = false } = {}) {
    if (destroyed) return;
    if (!force && loaded && isDirty()) return;
    setSync('Syncing…', 'idle');
    const [settingsResult, modelsResult, infoResult, sttResult, ttsResult, petsResult] = await Promise.allSettled([
      call('settings.get', {}),
      call('ai.models.list', {}),
      call('app.info', {}),
      call('settings.stt_install_status', {}),
      call('settings.tts_install_status', {}),
      call('settings.pets_status', {}),
    ]);
    if (destroyed) return;
    if (settingsResult.status === 'fulfilled') {
      const next = settingsFormValues(settingsResult.value?.settings || {});
      const keepDraft = !force && loaded && isDirty();
      saved = next;
      if (!keepDraft) {
        draft = settingsFormValues(settingsResult.value?.settings || {});
        secrets = {};
      }
      loaded = true;
      setSync('Synced', 'ready');
      hideBanner();
    } else {
      setSync('Sync failed', 'error');
      showBanner(settingsResult.reason?.message || 'Could not load settings');
    }
    state.models = modelsResult.status === 'fulfilled' ? (modelsResult.value?.models || []) : state.models;
    state.info = infoResult.status === 'fulfilled' ? infoResult.value : state.info;
    state.stt = sttResult.status === 'fulfilled' ? sttResult.value : state.stt;
    state.tts = ttsResult.status === 'fulfilled' ? ttsResult.value : state.tts;
    state.pets = petsResult.status === 'fulfilled' ? petsResult.value : state.pets;
    renderAll();
    if (pairingRefresh) {
      void pairingRefresh({ enabled: draft.remote_access_enabled === true }).catch(() => {});
    }
    if (state.stt?.running) startSttPoll();
  }

  async function onRemoteToggle() {
    const box = find('settings-remote-access-enabled');
    if (!box) return;
    const desired = box.checked;
    if (desired) {
      const error = validateSettingsField('remote_access_addresses', draft.remote_access_addresses, draft);
      const errorNode = find('pairing-address-error');
      if (error) {
        box.checked = false;
        if (errorNode) {
          errorNode.hidden = false;
          errorNode.textContent = error;
        }
        return;
      }
    }
    const confirmed = await confirmDialog(
      'Restart backend',
      desired ? 'Enabling remote access restarts the backend.' : 'Disabling remote access restarts the backend.',
      'Restart',
      false,
    );
    if (!confirmed) {
      box.checked = !desired;
      // The shared dialog drops focus when it closes; return it to the toggle.
      box.focus();
      return;
    }
    const payload = { remote_access_enabled: desired };
    if (desired) payload.remote_access_addresses = addresses(draft.remote_access_addresses);
    try {
      await call('settings.set', payload);
      saved.remote_access_enabled = desired;
      draft.remote_access_enabled = desired;
      if (desired) saved.remote_access_addresses = draft.remote_access_addresses;
      toast(desired ? 'Remote access enabled' : 'Remote access disabled', 'success');
      // Do not query pairing management while the old process is still
      // shutting down — pairing.sessions.list would fail mid-restart and
      // toast a bogus error. The panel is revealed directly and refreshed with
      // populate:false so its controls are wired without network calls; the
      // next Settings activation re-runs load().
      const panel = find('pairing-panel');
      if (panel) panel.hidden = !desired;
      void pairingRefresh?.({ enabled: desired, populate: false }).catch(() => {});
      updateChrome();
    } catch (error) {
      box.checked = !desired;
      toast(error?.message || 'Could not change remote access', 'error');
    }
  }

  function onClick(event) {
    const capability = event.target.closest?.('[data-capability]');
    if (capability) {
      focusCapability(capability.dataset.capability);
      return;
    }
    const stepButton = event.target.closest?.('[data-step-key]');
    if (stepButton) {
      stepField(stepButton.dataset.stepKey, Number(stepButton.dataset.step));
      return;
    }
    const reset = event.target.closest?.('[data-reset]');
    if (reset) {
      const field = fieldByKey(reset.dataset.reset);
      if (field) setDraft(field.key, field.default);
      return;
    }
    const choice = event.target.closest?.('[data-choice]');
    if (choice) {
      setDraft(choice.dataset.choice, choice.dataset.value ?? '');
      return;
    }
    const font = event.target.closest?.('[data-font-id]');
    if (font) {
      setFontPreference(font.dataset.fontId);
      paintFont();
      return;
    }
    const secretToggle = event.target.closest?.('[data-secret-toggle]');
    if (secretToggle) {
      const input = root.querySelector?.(`[data-secret="${secretToggle.dataset.secretToggle}"]`);
      if (!input) return;
      const show = input.type === 'password';
      input.type = show ? 'text' : 'password';
      secretToggle.textContent = show ? 'Hide' : 'Show';
      return;
    }
    const secretClear = event.target.closest?.('[data-secret-clear]');
    if (secretClear) {
      const key = secretClear.dataset.secretClear;
      secrets[key] = '';
      const input = root.querySelector?.(`[data-secret="${key}"]`);
      if (input) input.value = '';
      updateChrome();
      return;
    }
    const revert = event.target.closest?.('[data-revert]');
    if (revert) {
      setDraft(revert.dataset.revert, saved[revert.dataset.revert]);
      return;
    }
    const revertSecret = event.target.closest?.('[data-revert-secret]');
    if (revertSecret) {
      delete secrets[revertSecret.dataset.revertSecret];
      const input = root.querySelector?.(`[data-secret="${revertSecret.dataset.revertSecret}"]`);
      if (input) input.value = '';
      updateChrome();
      return;
    }
    if (event.target.closest?.('[data-review-close]')) {
      const review = find('settings-review');
      if (review) review.hidden = true;
      return;
    }
    const guide = event.target.closest?.('[data-stt-guide]');
    if (guide) {
      const card = find('settings-stt-card');
      card?.querySelectorAll('[data-stt-guide]').forEach((button) => button.classList.toggle('is-selected', button === guide));
      card?.querySelectorAll('[data-stt-guide-panel]').forEach((panel) => {
        panel.hidden = panel.dataset.sttGuidePanel !== guide.dataset.sttGuide;
      });
      return;
    }
    const action = event.target.closest?.('[data-action]')?.dataset.action;
    if (action === 'copy-data-dir') {
      const value = state.info?.data_dir || '';
      void navigator.clipboard?.writeText(value).then(
        () => toast('Data directory copied', 'success'),
        () => toast('Could not copy the data directory', 'error'),
      );
    } else if (action === 'stt-install') {
      const modelId = selects.get('stt-install')?.getSelected?.()?.[0] || '';
      if (!modelId) toast('Pick a model first', 'info');
      else {
        void call('settings.stt_install_start', { model_id: modelId }).then(() => startSttPoll()).catch((error) => toast(error.message, 'error'));
      }
    } else if (action === 'stt-stop') {
      void call('settings.stt_install_cancel', {}).then(async () => {
        stopSttPoll();
        try {
          state.stt = await call('settings.stt_install_status', {});
        } catch {
          state.stt = { ...(state.stt || {}), running: false };
        }
        if (!destroyed) replaceSttCard();
      }).catch((error) => toast(error.message, 'error'));
    } else if (action === 'tts-install') {
      const voiceId = selects.get('tts-install')?.getSelected?.()?.[0] || '';
      if (!voiceId) toast('Pick a voice first', 'info');
      else void call('settings.tts_install_start', { voice_id: voiceId }).catch((error) => toast(error.message, 'error'));
    } else if (action === 'pet') {
      void petAction();
    }
  }

  async function petAction() {
    const pets = state.pets || {};
    if (pets.supported === false || pets.install_active || petStarting) return;
    try {
      if (!pets.installed) {
        petStarting = true;
        const button = find('settings-pet-action');
        if (button) {
          button.disabled = true;
          button.textContent = 'Installing…';
        }
        try {
          await call('settings.pets_install_start', {});
        } catch (error) {
          if (button?.isConnected) {
            button.disabled = false;
            button.textContent = 'Install';
          }
          throw error;
        }
        await refreshInstaller('pet');
        return;
      }
      const result = await call('settings.pets_launch', {});
      const failed = result && result.launched !== true && result.stopped !== true && result.message;
      toast(result?.message || (result?.stopped ? 'Pet stopped' : 'Pet launched'), failed ? 'error' : 'success');
      await refreshInstaller('pet');
    } catch (error) {
      toast(error.message || 'Pet action failed', 'error');
    } finally {
      petStarting = false;
    }
  }

  function onInput(event) {
    if (event.target?.id === 'settings-sidebar-compact') {
      applySidebarCompact(event.target.checked);
      return;
    }
    const secret = event.target.closest?.('[data-secret]');
    if (secret) {
      const key = secret.dataset.secret;
      if (secret.value === '') delete secrets[key];
      else secrets[key] = secret.value;
      updateChrome();
      return;
    }
    const input = event.target.closest?.('[data-setting]');
    if (!input) return;
    const field = fieldByKey(input.dataset.setting);
    if (!field) return;
    if (field.type === 'boolean') setDraft(field.key, input.checked);
    else if (field.type === 'sampling') setDraft(field.key, input.value.trim() === '' ? null : Number(input.value));
    else if (field.type === 'number') setDraft(field.key, input.value.trim() === '' ? Number.NaN : Number(input.value));
    else setDraft(field.key, input.value);
  }

  function onKey(event) {
    if (root.hidden) return;
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      void save();
      return;
    }
    if (event.key === '/' && !typingTarget(event.target)) {
      event.preventDefault();
      find('settings-search')?.focus();
      return;
    }
    if (event.key === 'Escape') {
      const review = find('settings-review');
      if (review && !review.hidden) {
        review.hidden = true;
        return;
      }
      const search = find('settings-search');
      if (search?.value) {
        search.value = '';
        updateChrome();
      }
    }
  }

  function ensureSearchEmpty() {
    if (find('settings-search-empty')) return;
    const main = root.querySelector?.('.settings-main');
    if (!main) return;
    const node = el('p', { id: 'settings-search-empty', class: 'settings-copy', hidden: true });
    const field = root.querySelector?.('.settings-search-field');
    if (field && field.parentElement === main) field.after(node);
    else find('settings-panels')?.before(node);
  }

  function init() {
    if (ready) return controller;
    ready = true;
    ensureSearchEmpty();
    const saveBar = find('settings-savebar');
    if (saveBar && !find('settings-fix')) {
      const fix = el('button', { type: 'button', id: 'settings-fix', hidden: true, text: 'Fix fields' });
      saveBar.insertBefore(fix, find('settings-save'));
    }
    if (saveBar && !find('settings-save-error')) {
      saveBar.append(el('span', { id: 'settings-save-error', class: 'settings-field-error' }));
    }
    const revoke = find('pairing-revoke-all-btn');
    if (revoke && !revoke.dataset.wired) {
      revoke.dataset.wired = '1';
      listen(revoke, 'click', () => { void revokeAllSessions(); });
    }
    listen(find('settings-rail'), 'click', (event) => {
      const button = event.target.closest?.('[data-settings-section]');
      if (button) showSection(button.dataset.settingsSection);
    });
    listen(find('settings-panels'), 'click', onClick);
    listen(find('settings-panels'), 'input', onInput);
    listen(find('settings-panels'), 'change', onInput);
    listen(find('settings-search'), 'input', () => updateChrome());
    listen(find('settings-save'), 'click', () => { void save(); });
    listen(find('settings-discard'), 'click', () => {
      draft = { ...saved };
      secrets = {};
      renderAll();
    });
    listen(find('settings-review-open'), 'click', () => {
      const review = find('settings-review');
      if (!review) return;
      review.hidden = !review.hidden;
      if (!review.hidden) paintReview();
    });
    listen(find('settings-review'), 'click', onClick);
    listen(find('settings-fix'), 'click', () => {
      const key = find('settings-fix')?.dataset.fixKey;
      focusVisible(root.querySelector?.(`[data-setting="${key}"]`) || find('pairing-remote-address'));
    });
    listen(find('settings-refresh'), 'click', () => {
      void (async () => {
        if (isDirty()) {
          const confirmed = await confirmDialog('Reload settings', 'Discard unsaved changes and reload?', 'Reload', true);
          if (!confirmed) {
            find('settings-refresh')?.focus();
            return;
          }
        }
        secrets = {};
        await load({ force: true });
      })();
    });
    listen(find('settings-remote-access-enabled'), 'change', () => { void onRemoteToggle(); });
    listen(find('pairing-remote-address'), 'input', onInput);
    listen(find('settings-disk-banner'), 'click', (event) => {
      const action = event.target.closest?.('[data-banner]')?.dataset.banner;
      if (action === 'keep') hideBanner();
      if (action === 'reload') {
        secrets = {};
        hideBanner();
        void load({ force: true });
      }
    });
    listen(document.getElementById('global-nav-pet'), 'click', () => {
      if (!root.hidden && find('settings-pet-card')) scrollToNode(find('settings-pet-card'));
      else state.focusPet = true;
    });
    listen(document, 'keydown', onKey);
    listeners.push(on('settings.applied', () => {
      if (isDirty()) {
        showBanner('Runtime reloaded from disk. Unsaved edits are still here.', [
          el('button', { type: 'button', class: 'settings-quiet-button', dataset: { banner: 'keep' }, text: 'Keep' }),
          el('button', { type: 'button', class: 'settings-quiet-button', dataset: { banner: 'reload' }, text: 'Reload' }),
        ]);
        return;
      }
      void load({ force: true });
    }));
    listeners.push(on('settings.rejected', (payload) => {
      showBanner(`Skipped an external settings change (${payload?.reason || 'unknown'}).`);
    }));
    listeners.push(on('stt.install.progress', (payload) => paintProgress('stt', payload)));
    listeners.push(on('stt.install.done', (payload) => {
      stopSttPoll();
      paintProgress('stt', { phase: 'done', message: payload?.message || 'Offline speech-to-text ready', bytes_fetched: 1, bytes_total: 1 });
      toast('Offline speech-to-text installed', 'success');
      void refreshInstaller('stt');
    }));
    listeners.push(on('stt.install.error', (payload) => {
      stopSttPoll();
      paintProgress('stt', { message: payload?.message || 'Install failed' });
      toast(payload?.message || 'Speech-to-text install failed', 'error');
    }));
    listeners.push(on('tts.install.progress', (payload) => paintProgress('tts', payload)));
    listeners.push(on('tts.install.done', (payload) => {
      paintProgress('tts', { phase: 'done', message: payload?.message || 'Offline text-to-speech ready', bytes_fetched: 1, bytes_total: 1 });
      toast('Offline text-to-speech installed', 'success');
      void refreshInstaller('tts');
    }));
    listeners.push(on('tts.install.error', (payload) => {
      paintProgress('tts', { message: payload?.message || 'Install failed' });
      toast(payload?.message || 'Text-to-speech install failed', 'error');
    }));
    listeners.push(on('pets.install.progress', (payload) => paintProgress('pet', payload)));
    listeners.push(on('pets.install.done', () => {
      toast('Desktop pet installed', 'success');
      void refreshInstaller('pet');
    }));
    listeners.push(on('pets.install.error', (payload) => {
      paintProgress('pet', { message: payload?.message || 'Install failed' });
      toast(payload?.message || 'Pet install failed', 'error');
    }));
    return controller;
  }

  function activate() {
    if (!loaded || !isDirty()) void load();
    else if (state.focusPet) scrollToNode(find('settings-pet-card'));
  }

  function deactivate() {
    stopSttPoll();
  }

  function destroy() {
    destroyed = true;
    stopSttPoll();
    disconnectSectionSpy();
    destroySelects();
    for (const unsubscribe of listeners) unsubscribe();
    listeners.length = 0;
    ready = false;
  }

  const controller = { init, activate, deactivate, destroy, state };
  return controller;
}
