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
  const byProvider = new Map();
  for (const model of models) {
    if (!byProvider.has(model.provider_name)) byProvider.set(model.provider_name, []);
    byProvider.get(model.provider_name).push(model);
  }
  if (!models.length) list.append(el('div', { class: 'agent-model-empty', text: 'No models yet. Configure a provider and import its models first.' }));
  for (const [provider, providerModels] of byProvider) {
    const section = el('div', { class: 'agent-model-section' }, el('div', { class: 'agent-model-section-title', text: provider }));
    for (const model of providerModels) {
      const isSelected = model.id === selectedModel;
      const row = el('div', { class: `agent-model-row${isSelected ? ' is-selected' : ''}` },
        el('button', { class: 'agent-model-choice', type: 'button' },
          el('span', { class: 'agent-model-name', text: model.id }),
          el('span', { class: 'agent-model-meta' }, el('span', { class: 'agent-model-provider', text: model.provider_name })),
        ),
      );
      row.querySelector('button').addEventListener('click', () => {
        selectModel(model.id);
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
        autoBtn.addEventListener('click', (e) => { e.stopPropagation(); selectModel(model.id); selectEffort('auto'); closeMenu(); });
        effortBar.append(autoBtn);
        for (const effort of model.supported_efforts) {
          const chip = el('button', { class: `agent-effort-chip${isSelected && selectedEffort === effort ? ' is-active' : ''}`, type: 'button', text: effort });
          chip.addEventListener('click', (e) => { e.stopPropagation(); selectModel(model.id); selectEffort(effort); closeMenu(); });
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
        const id = row.querySelector('.agent-model-name').textContent;
        row.hidden = !(id.toLowerCase().includes(query) || provider.toLowerCase().includes(query));
      }
    }
  }, 120));
  menu.append(list);
}
