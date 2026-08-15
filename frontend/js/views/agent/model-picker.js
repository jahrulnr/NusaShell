import { debounce, el } from '../../ui.js';

export function bindModelPicker({ getModels, getSelectedModel, getSelectedEffort, selectModel, selectEffort, refreshModels }) {
  const trigger = document.getElementById('model-trigger');
  const menu = document.getElementById('model-menu');
  const closeMenu = () => {
    menu.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
  };
  const openMenu = () => {
    try {
      renderModelMenu(menu, getModels(), getSelectedModel(), getSelectedEffort(), selectModel, selectEffort, closeMenu);
      menu.hidden = false;
      positionMenu(menu, trigger);
      trigger.setAttribute('aria-expanded', 'true');
      menu.querySelector('input')?.focus();
    } catch (error) {
      console.error('model picker:', error);
    }
  };

  trigger.addEventListener('click', () => {
    if (!menu.hidden) {
      closeMenu();
      return;
    }
    openMenu();
    refreshModels().then(() => {
      if (!menu.hidden) openMenu();
    }).catch((error) => console.error('model refresh:', error));
  });
  window.addEventListener('hashchange', () => {
    if (location.hash === '' || location.hash === '#agent') refreshModels();
  });
  document.addEventListener('mousedown', (event) => {
    if (!menu.hidden && !menu.contains(event.target) && event.target !== trigger) closeMenu();
  });
}

function positionMenu(menu, trigger) {
  const rect = trigger.getBoundingClientRect();
  const menuHeight = menu.offsetHeight || 320;
  const spaceBelow = window.innerHeight - rect.bottom;
  const spaceAbove = rect.top;
  const menuWidth = menu.offsetWidth || 560;
  menu.style.left = `${Math.max(8, Math.min(rect.left, window.innerWidth - menuWidth - 8))}px`;
  menu.style.top = `${spaceBelow < menuHeight + 6 && spaceAbove > spaceBelow
    ? Math.max(8, rect.top - menuHeight - 6)
    : rect.bottom + 6}px`;
}

function renderModelMenu(menu, models, selectedModel, selectedEffort, selectModel, selectEffort, closeMenu) {
  menu.innerHTML = '';
  menu.append(el('div', { class: 'agent-model-search' }, el('input', { type: 'text', placeholder: 'Search models…', autocomplete: 'off' })));
  const list = el('div', { class: 'agent-model-list' });
  // Filter out non-chat models — only chat LLMs can do chat completions.
  // Kind is enriched from models.dev catalog; treat unknown ("") as chat
  // for backward compatibility with providers not in the catalog.
  const chatModels = models.filter((m) => !m.kind || m.kind === 'chat');
  const byProvider = new Map();
  for (const model of chatModels) {
    if (!byProvider.has(model.provider_name)) byProvider.set(model.provider_name, []);
    byProvider.get(model.provider_name).push(model);
  }
  if (!chatModels.length) list.append(el('div', { class: 'agent-model-empty', text: 'No models yet. Configure a provider and import its models first.' }));
  for (const [provider, providerModels] of byProvider) {
    const section = el('div', { class: 'agent-model-section' }, el('div', { class: 'agent-model-section-title', text: provider }));
    for (const model of providerModels) {
      const isSelected = `${model.provider_id}:${model.id}` === selectedModel || (selectedModel && !selectedModel.includes(':') && model.id === selectedModel);
      const name = model.display_name || model.id;
      const badges = [];
      if (model.reasoning) badges.push(el('span', { class: 'agent-model-badge agent-badge-reasoning', text: 'reasoning', title: 'Supports reasoning/thinking mode' }));
      if (model.tool_call) badges.push(el('span', { class: 'agent-model-badge agent-badge-tool', text: 'tools', title: 'Supports function/tool calling' }));
      if (model.vision) badges.push(el('span', { class: 'agent-model-badge agent-badge-vision', text: 'vision', title: 'Supports image input' }));
      if (model.structured_output) badges.push(el('span', { class: 'agent-model-badge agent-badge-structured', text: 'JSON', title: 'Supports structured output' }));
      const metaChildren = [el('span', { class: 'agent-model-provider', text: model.provider_name })];
      if (model.context) metaChildren.push(el('span', { class: 'agent-model-context', text: formatContext(model.context) }));
      if (model.input_cost || model.output_cost) metaChildren.push(el('span', { class: 'agent-model-cost', text: formatCost(model.input_cost, model.output_cost) }));
      const row = el('div', { class: `agent-model-row${isSelected ? ' is-selected' : ''}` },
        el('button', { class: 'agent-model-choice', type: 'button' },
          el('span', { class: 'agent-model-name' },
            el('span', { text: name }),
            badges.length ? el('span', { class: 'agent-model-badges' }, badges) : null,
          ),
          el('span', { class: 'agent-model-meta' }, metaChildren),
        ),
      );
      row.querySelector('button').addEventListener('click', () => {
        selectModel(`${model.provider_id}:${model.id}`);
        closeMenu();
      });
      // Effort selector: show for any model that advertises supported efforts.
      // The active chip reflects the current effort selection (only meaningful
      // for the selected model, but visible for all so the user can see what
      // each model supports before switching).
      if (model.supported_efforts && model.supported_efforts.length) {
        const effortBar = el('div', { class: 'agent-model-effort-bar' });
        effortBar.append(el('span', { class: 'agent-model-effort-label', text: 'Reasoning:' }));
        const autoBtn = el('button', { class: `agent-effort-chip${isSelected && selectedEffort === 'auto' ? ' is-active' : ''}`, type: 'button', text: 'default' });
        autoBtn.addEventListener('click', (e) => { e.stopPropagation(); selectModel(`${model.provider_id}:${model.id}`); selectEffort('auto'); closeMenu(); });
        effortBar.append(autoBtn);
        for (const effort of model.supported_efforts) {
          const chip = el('button', { class: `agent-effort-chip${isSelected && selectedEffort === effort ? ' is-active' : ''}`, type: 'button', text: effort });
          chip.addEventListener('click', (e) => { e.stopPropagation(); selectModel(`${model.provider_id}:${model.id}`); selectEffort(effort); closeMenu(); });
          effortBar.append(chip);
        }
        row.append(effortBar);
      }
      section.append(row);
    }
    list.append(section);
  }
  const search = menu.querySelector('input');
  search.addEventListener('input', debounce(() => {
    const query = search.value.toLowerCase();
    for (const section of list.querySelectorAll('.agent-model-section')) {
      const provider = section.querySelector('.agent-model-section-title').textContent;
      for (const row of section.querySelectorAll('.agent-model-row')) {
        const nameEl = row.querySelector('.agent-model-name > span');
        const name = nameEl ? nameEl.textContent : '';
        row.hidden = !(name.toLowerCase().includes(query) || provider.toLowerCase().includes(query));
      }
    }
  }, 120));
  menu.append(list);
}

function formatContext(tokens) {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M ctx`;
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K ctx`;
  return `${tokens} ctx`;
}

function formatCost(input, output) {
  const parts = [];
  if (input) parts.push(`$${input}/M in`);
  if (output) parts.push(`$${output}/M out`);
  return parts.join(' · ');
}
