// Home / launcher view. This mirrors the Electron launcher surface while
// keeping the Go port's HTTP plugin endpoint and browser-window handoff.

let plugins = [];
let launcherSearchQuery = '';
let launcherCategory = 'All';
let pluginLoadError = false;

export async function initHome() {
  const searchInput = document.getElementById('search-input');
  const searchClear = document.getElementById('search-clear');
  const retryBtn = document.getElementById('plugin-load-retry');

  searchInput?.addEventListener('input', () => {
    launcherSearchQuery = searchInput.value;
    searchClear.hidden = !launcherSearchQuery;
    renderAppGrid();
  });
  searchInput?.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && launcherSearchQuery) {
      searchInput.value = '';
      searchInput.dispatchEvent(new Event('input'));
    }
  });
  searchClear?.addEventListener('click', () => {
    searchInput.value = '';
    searchInput.dispatchEvent(new Event('input'));
    searchInput.focus();
  });
  retryBtn?.addEventListener('click', () => {
    setPluginLoadError(false);
    void refresh();
  });

  await refresh();
}

export async function refresh() {
  try {
    const response = await fetch('/plugins');
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const data = await response.json();
    plugins = Array.isArray(data.plugins) ? data.plugins : [];
    setPluginLoadError(false);
  } catch (error) {
    plugins = [];
    setPluginLoadError(true, error);
  }
  renderAppGrid();
}

function setPluginLoadError(visible, error) {
  pluginLoadError = visible;
  const banner = document.getElementById('plugin-load-error');
  if (!banner) return;
  banner.hidden = !visible;
  const text = document.getElementById('plugin-load-error-text');
  if (text && error != null) {
    text.textContent = `Could not load plugins: ${String(error).replace(/^Error: /, '')}`;
  }
}

function pluginId(plugin) {
  return plugin?.id || plugin?.pluginId || '';
}

function pluginManifest(plugin) {
  return plugin?.manifest || plugin || {};
}

function hasPluginUI(plugin) {
  const manifest = pluginManifest(plugin);
  return Boolean(plugin?.hasUI || plugin?.ui?.entry || manifest.ui?.entry);
}

function pluginDescription(plugin) {
  const manifest = pluginManifest(plugin);
  return plugin?.description || manifest.description || '';
}

export function filterLauncherPlugins(items, query) {
  const normalized = String(query || '').trim().toLocaleLowerCase();
  if (!normalized) return items;
  return items.filter((plugin) => {
    const text = `${plugin.name || ''} ${pluginId(plugin)} ${pluginDescription(plugin)}`;
    return text.toLocaleLowerCase().includes(normalized);
  });
}

function renderAppGrid() {
  const grid = document.getElementById('app-grid');
  if (!grid) return;
  grid.replaceChildren();

  const uiPlugins = plugins.filter(hasPluginUI);
  if (plugins.length === 0) {
    appendEmptyState(
      grid,
      pluginLoadError
        ? 'Could not load plugins. Use Retry above.'
        : 'No plugins installed. Add a plugin folder to plugins/.',
    );
    return;
  }
  if (uiPlugins.length === 0) {
    appendEmptyState(grid, 'No apps to launch. Installed plugins are MCP-only — manage them from the Plugins view.');
    return;
  }

  renderLauncherTabs(uiPlugins);
  const categoryFiltered = launcherCategory === 'All'
    ? uiPlugins
    : uiPlugins.filter((plugin) => (plugin.category || 'Uncategorized') === launcherCategory);
  const visiblePlugins = filterLauncherPlugins(categoryFiltered, launcherSearchQuery);

  if (visiblePlugins.length === 0) {
    appendEmptyState(grid, `No plugins match “${launcherSearchQuery}”.`);
    return;
  }

  visiblePlugins.forEach((plugin) => {
    const cell = document.createElement('button');
    cell.type = 'button';
    cell.className = 'app-cell';
    cell.dataset.pluginId = pluginId(plugin);

    const icon = document.createElement('div');
    icon.className = 'app-icon';
    setPluginIcon(icon, plugin.icon || '🧩', 60);

    const name = document.createElement('div');
    name.className = 'app-name';
    name.textContent = plugin.name || pluginId(plugin);

    const status = document.createElement('div');
    renderPluginStatus(status, plugin.state);

    cell.append(icon, name, status);
    cell.addEventListener('click', () => openPluginWindow(plugin));
    grid.appendChild(cell);
  });
}

function renderLauncherTabs(uiPlugins) {
  const tabsContainer = document.getElementById('launcher-tabs');
  if (!tabsContainer) return;
  const categories = ['All', ...new Set(uiPlugins.map((plugin) => plugin.category || 'Uncategorized'))];
  if (!categories.includes(launcherCategory)) launcherCategory = 'All';

  tabsContainer.replaceChildren();
  categories.forEach((category) => {
    const tab = document.createElement('button');
    tab.type = 'button';
    tab.className = `tab${category === launcherCategory ? ' active' : ''}`;
    tab.textContent = category;
    tab.dataset.category = category;
    tab.addEventListener('click', () => {
      launcherCategory = category;
      renderAppGrid();
    });
    tabsContainer.appendChild(tab);
  });
}

function setPluginIcon(container, icon, size) {
  const value = String(icon || '🧩').trim() || '🧩';
  const isImage = /^(?:https?:\/\/|data:image\/)/i.test(value);
  container.replaceChildren();
  container.classList.toggle('has-image', isImage);
  container.classList.add('bg-blue');

  if (isImage) {
    const image = document.createElement('img');
    image.className = 'plugin-icon-image';
    image.alt = '';
    image.width = size;
    image.height = size;
    image.addEventListener('error', () => setPluginIcon(container, '🧩', Math.min(size, 28)), { once: true });
    image.src = value;
    container.appendChild(image);
    return;
  }

  const glyph = document.createElement('span');
  glyph.className = 'plugin-icon-glyph';
  glyph.style.fontSize = `${Math.round(size * 0.55)}px`;
  glyph.textContent = value;
  container.appendChild(glyph);
}

function renderPluginStatus(container, state) {
  const normalizedState = String(state || '');
  container.className = `app-status${normalizedState ? ` ${normalizedState}` : ''}`;
  if (!['running', 'starting', 'stopping', 'crashed'].includes(normalizedState)) return;
  const dot = document.createElement('span');
  dot.className = 'status-dot';
  container.append(dot, document.createTextNode(normalizedState[0].toUpperCase() + normalizedState.slice(1)));
}

function appendEmptyState(grid, text) {
  const empty = document.createElement('div');
  empty.className = 'app-grid-empty';
  empty.textContent = text;
  grid.appendChild(empty);
}

function openPluginWindow(plugin) {
  const id = pluginId(plugin);
  const manifest = pluginManifest(plugin);
  const win = manifest.ui?.window || {};
  const width = win.defaultSize?.width || 1024;
  const height = win.defaultSize?.height || 720;
  const features = [
    `width=${width}`,
    `height=${height}`,
    'menubar=no',
    'toolbar=no',
    'location=no',
    'status=no',
    `resizable=${win.resizable !== false ? 'yes' : 'no'}`,
  ].join(',');
  window.open(`/plugins/${id}/`, id, features);
}
