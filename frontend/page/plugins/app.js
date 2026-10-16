import { rpc as defaultRPC } from '../../js/rpc.js';
import {
  bindTablistKeyboard,
  confirmDialog,
  debounce,
  dialog,
  el,
  registerOverlayDismiss,
  toast,
} from '../../js/ui.js';
import { openPluginWindow as launchPluginWindow } from '../../js/plugin-window.js?v=1';
import { homePluginIconGlyph, homePluginIconSource } from '../home/mcp.js';
import {
  canAutoUpdate,
  filterPlugins,
  hasContract,
  hasPluginUI,
  hasUpdate,
  pluginFilterLabel,
  pluginIconFallback,
  pluginStatusLabel,
  pluginStatusTone,
  pluginToolCount,
  pluginTransport,
  pluginTypeLabel,
} from './model.js';

const TRANSPORT_OPTIONS = [
  { value: 'stdio', label: 'stdio — local child process' },
  { value: 'sse', label: 'SSE — remote endpoint' },
  { value: 'http', label: 'HTTP — Streamable HTTP endpoint' },
];

function iconIsImage(value) {
  return /^(?:data:image\/|https?:\/\/)/i.test(String(value || '').trim());
}

function pluginIcon(plugin) {
  const value = String(plugin?.icon || '').trim();
  const source = iconIsImage(value) ? value : homePluginIconSource(plugin);
  if (source) {
    const image = el('img', { class: 'plugins-icon-image', alt: '', loading: 'lazy' });
    image.addEventListener('error', () => image.replaceWith(el('span', { text: pluginIconFallback(plugin) })), { once: true });
    image.src = source;
    return image;
  }
  return el('span', { text: homePluginIconGlyph(plugin) || pluginIconFallback(plugin) });
}

function nextFrame(callback) {
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(callback);
  else setTimeout(callback, 0);
}

function parseKeyValueLines(text, separator) {
  const out = {};
  for (const line of String(text || '').split('\n')) {
    const index = line.indexOf(separator);
    if (index > 0) out[line.slice(0, index).trim()] = line.slice(index + 1).trim();
  }
  return out;
}

function updateTransportFields(select, values) {
  const isStdio = (select.value || 'stdio') === 'stdio';
  for (const [name, input] of Object.entries(values)) {
    if (name === 'name' || name === 'transport') continue;
    const wrapper = input.closest('label');
    if (!wrapper) continue;
    const isRemote = name === 'url' || name === 'headers';
    wrapper.hidden = isStdio === isRemote;
  }
}

export function createPluginsPage(root = document, { rpcClient = defaultRPC } = {}) {
  const state = {
    plugins: [],
    selectedId: null,
    query: '',
    filter: 'all',
    loading: false,
    error: null,
    catalog: [],
    installTab: 'catalog',
    installing: false,
  };
  let initialized = false;
  let refreshToken = 0;
  let actionToken = 0;
  const startingPluginIDs = new Set();
  let installReturnFocus = null;
  let unregisterInstallDismiss = null;
  let installKeyHandler = null;

  const find = (id) => root.querySelector(`#${id}`);
  const pageNode = () => root.nodeType === 1 ? root : root.querySelector('#plugins-view');
  const isMobile = () => Number(window.innerWidth) <= 720;
  const selectedPlugin = () => state.plugins.find((plugin) => plugin.id === state.selectedId) || null;

  // Mobile list -> detail -> Back navigation lives on the page root because the
  // library toolbar and filter row are siblings of .plugins-workspace and must
  // hide while a detail is open. The attribute is inert on desktop widths.
  function setMobilePane(pane) {
    const page = pageNode();
    if (!page || page.nodeType !== 1) return;
    const previous = page.dataset.mobilePane || 'list';
    if (pane === 'detail' && previous === 'list') {
      page.dataset.mobileListScroll = String(page.scrollTop || 0);
    }
    page.dataset.mobilePane = pane;
    if (pane === 'detail' && previous !== 'detail') {
      page.scrollTop = 0;
      return;
    }
    if (pane === 'list' && previous !== 'list') {
      const top = Number(page.dataset.mobileListScroll) || 0;
      nextFrame(() => {
        if (page.dataset.mobilePane === 'list') page.scrollTop = top;
      });
    }
  }

  function focusMobileControl(selector) {
    if (!isMobile()) return;
    nextFrame(() => root.querySelector(selector)?.focus?.({ preventScroll: true }));
  }

  // The shared index.html belongs to another workstream, so the Back control
  // is mounted through this controller instead of new root markup.
  function ensureMobileBack() {
    const header = root.querySelector('.plugins-detail-header');
    if (!header || find('plugins-mobile-back')) return;
    const back = el('button', {
      class: 'plugins-mobile-back',
      id: 'plugins-mobile-back',
      type: 'button',
      'aria-label': 'Back to plugins',
    }, [
      el('i', { dataset: { lucide: 'arrow-left' }, 'aria-hidden': 'true' }),
      el('span', { text: 'Plugins' }),
    ]);
    back.addEventListener('click', () => {
      setMobilePane('list');
      focusMobileControl('.plugins-list-row.is-selected');
    });
    header.prepend(back);
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  }

  function setSyncState(copy, tone = '') {
    const node = find('plugins-sync-state');
    if (!node) return;
    node.textContent = copy;
    node.dataset.state = tone;
  }

  function visiblePlugins() {
    return filterPlugins(state.plugins, state.query, state.filter);
  }

  function reconcileSelectedPlugin() {
    if (state.selectedId && !visiblePlugins().some((plugin) => plugin.id === state.selectedId)) state.selectedId = null;
  }

  function renderToolbar() {
    const visible = visiblePlugins();
    const total = state.plugins.length;
    const count = find('plugins-count');
    const indexCount = find('plugins-index-count');
    const filterCount = find('plugins-filter-count');
    if (count) count.textContent = `${total} installed`;
    if (indexCount) indexCount.textContent = String(total);
    if (filterCount) filterCount.textContent = state.query || state.filter !== 'all'
      ? `${visible.length} of ${total} shown`
      : pluginFilterLabel(state.filter, total);
  }

  function renderFilters() {
    root.querySelectorAll('[data-plugin-filter]').forEach((button) => {
      const active = button.dataset.pluginFilter === state.filter;
      button.classList.toggle('is-active', active);
      button.setAttribute('aria-pressed', String(active));
    });
  }

  function renderRowMeta(plugin) {
    return [
      el('span', { text: plugin.category || pluginTransport(plugin) }),
      pluginToolCount(plugin) ? el('span', { dataset: { pluginRuntime: 'tools' }, text: `${pluginToolCount(plugin)} tools` }) : null,
      hasContract(plugin) ? el('span', { class: 'plugins-row-contract', text: 'contract', title: plugin.contractEntry }) : null,
      hasUpdate(plugin) ? el('span', { class: 'plugins-row-update', text: `Update · v${plugin.updateAvailable}` }) : null,
    ];
  }

  function createPluginListItem(plugin) {
    const item = el('div', { class: 'plugins-list-item', role: 'listitem', dataset: { pluginId: plugin.id } });
    const tone = pluginStatusTone(plugin.status);
    const row = el('button', {
      class: `plugins-list-row${plugin.id === state.selectedId ? ' is-selected' : ''}`,
      type: 'button',
      dataset: { pluginId: plugin.id },
      'aria-label': `Open ${plugin.name || plugin.id} details`,
      'aria-current': plugin.id === state.selectedId ? 'true' : undefined,
    });
    const name = el('div', { class: 'plugins-row-name' }, [
      el('strong', { text: plugin.name || plugin.id || 'Unnamed integration' }),
      el('span', { class: 'plugins-row-type', text: pluginTypeLabel(plugin) }),
    ]);
    const copy = el('span', { class: 'plugins-row-copy' }, [
      name,
      el('span', { class: 'plugins-row-id', text: plugin.id || '—' }),
      el('div', { class: 'plugins-row-meta' }, renderRowMeta(plugin)),
    ]);
    const status = el('span', { class: `plugins-row-status plugins-row-status-${tone}`, dataset: { pluginRuntime: 'status' } }, [
      el('i', { 'aria-hidden': 'true' }),
      el('span', { text: pluginStatusLabel(plugin.status) }),
    ]);
    row.append(el('span', { class: `plugins-row-icon${plugin.plugin ? ' is-plugin' : ''}`, 'aria-hidden': 'true' }, pluginIcon(plugin)), copy, status);
    row.addEventListener('click', () => {
      setMobilePane('detail');
      selectPlugin(plugin.id);
      focusMobileControl('#plugins-mobile-back');
    });
    item.append(row);
    return item;
  }

  function renderList() {
    const list = find('plugins-list');
    if (!list) return;
    const visible = visiblePlugins();
    list.setAttribute('aria-busy', String(state.loading));
    list.replaceChildren();
    renderToolbar();
    renderFilters();

    if (state.loading && !state.plugins.length) {
      list.append(el('p', { class: 'plugins-empty-list', role: 'status' }, [
        el('strong', { text: 'Loading plugins…' }),
        el('span', { text: 'Reading the local MCP catalog.' }),
      ]));
      return;
    }
    if (state.error && !state.plugins.length) {
      const retry = el('button', { class: 'plugins-inline-retry', type: 'button', text: 'Retry' });
      retry.addEventListener('click', () => { void refresh(); });
      list.append(el('div', { class: 'plugins-empty-list plugins-empty-list-error', role: 'status' }, [
        el('strong', { text: 'Plugins unavailable' }),
        el('span', { text: state.error.message || 'Could not read installed integrations.' }),
        retry,
      ]));
      return;
    }
    if (!visible.length) {
      list.append(el('p', { class: 'plugins-empty-list', role: 'status' }, [
        el('strong', { text: state.query || state.filter !== 'all' ? 'No matching integrations' : 'No integrations yet' }),
        el('span', { text: state.query || state.filter !== 'all' ? 'Try another search or filter.' : 'Add an MCP server or install a plugin to give the agent more tools.' }),
      ]));
      return;
    }

    for (const plugin of visible) list.append(createPluginListItem(plugin));
  }

  function renderConnection(plugin) {
    const list = find('plugins-connection-list');
    if (!list) return;
    const mcp = plugin.manifest?.mcp || {};
    const remote = mcp.transport === 'sse' || mcp.transport === 'http';
    const rows = [
      ['Type', pluginTypeLabel(plugin)],
      ['Transport', pluginTransport(plugin)],
    ];
    if (remote) rows.push(['Endpoint', mcp.url || 'Not configured']);
    else {
      rows.push(['Command', mcp.command || 'Not configured']);
      if (mcp.args?.length) rows.push(['Arguments', mcp.args.join(' ')]);
    }
    if (plugin.plugin && plugin.version) rows.push(['Version', `v${plugin.version}`]);
    if (plugin.category) rows.push(['Category', plugin.category]);
    if (hasContract(plugin)) rows.push(['Agent contract', plugin.contractEntry]);
    for (const [label, value] of rows) {
      list.append(el('dt', { text: label }), el('dd', { text: value }));
    }
  }

  function renderTools(plugin) {
    const list = find('plugins-tools-list');
    const count = find('plugins-tools-heading-count');
    if (!list) return;
    const tools = Array.isArray(plugin.tools) ? plugin.tools : [];
    if (count) count.textContent = String(tools.length);
    list.replaceChildren();
    if (!tools.length) {
      list.append(el('p', { class: 'plugins-tools-empty', role: 'status', text: pluginStatusTone(plugin.status) === 'connected' ? 'Connected, but no tools were advertised.' : 'Start the server to discover its tools.' }));
      return;
    }
    for (const tool of tools) {
      list.append(el('div', { class: 'plugins-tool-item', role: 'listitem' }, [
        el('strong', { text: tool.name || 'Unnamed tool' }),
        tool.description ? el('span', { text: tool.description }) : null,
      ]));
    }
  }

  function toolsAreEqual(left, right) {
    const a = Array.isArray(left) ? left : [];
    const b = Array.isArray(right) ? right : [];
    if (a.length !== b.length) return false;
    return a.every((tool, index) => JSON.stringify(tool) === JSON.stringify(b[index]));
  }

  function findListItem(id) {
    const list = find('plugins-list');
    if (!list) return null;
    return [...list.querySelectorAll('.plugins-list-item')]
      .find((item) => item.dataset.pluginId === id) || null;
  }

  function syncRowStatus(row, plugin) {
    const status = row?.querySelector('[data-plugin-runtime="status"]');
    if (!status) return;
    const tone = pluginStatusTone(plugin.status);
    status.className = `plugins-row-status plugins-row-status-${tone}`;
    status.replaceChildren(
      el('i', { 'aria-hidden': 'true' }),
      el('span', { text: pluginStatusLabel(plugin.status) }),
    );
  }

  function syncRowToolCount(row, plugin) {
    const meta = row?.querySelector('.plugins-row-meta');
    if (!meta) return;
    const count = pluginToolCount(plugin);
    const current = meta.querySelector('[data-plugin-runtime="tools"]');
    if (!count) {
      current?.remove();
      return;
    }
    if (current) {
      current.textContent = `${count} tools`;
      return;
    }
    const tools = el('span', { dataset: { pluginRuntime: 'tools' }, text: `${count} tools` });
    const anchor = meta.querySelector('.plugins-row-contract, .plugins-row-update');
    meta.insertBefore(tools, anchor || null);
  }

  function syncPluginRow(item, plugin, { statusChanged = false, toolsChanged = false } = {}) {
    const row = item?.querySelector('.plugins-list-row');
    if (!row) return;
    if (statusChanged) syncRowStatus(row, plugin);
    if (toolsChanged) syncRowToolCount(row, plugin);
  }

  function appendListEmptyState(list) {
    const filtered = Boolean(state.query || state.filter !== 'all');
    list.append(el('p', { class: 'plugins-empty-list', role: 'status' }, [
      el('strong', { text: filtered ? 'No matching integrations' : 'No integrations yet' }),
      el('span', { text: filtered ? 'Try another search or filter.' : 'Add an MCP server or install a plugin to give the agent more tools.' }),
    ]));
  }

  function syncListMembership(previous, plugin, changes) {
    const list = find('plugins-list');
    if (!list) return false;
    const wasVisible = filterPlugins([previous], state.query, state.filter).length > 0;
    const isVisible = filterPlugins([plugin], state.query, state.filter).length > 0;
    const selectionCleared = state.selectedId === plugin.id && !isVisible;
    if (selectionCleared) state.selectedId = null;
    const current = findListItem(plugin.id);
    if (isVisible && current) {
      syncPluginRow(current, plugin, changes);
      return false;
    }
    if (isVisible && !current) {
      list.querySelector('.plugins-empty-list')?.remove();
      const item = createPluginListItem(plugin);
      const visible = visiblePlugins();
      const targetIndex = visible.findIndex((candidate) => candidate.id === plugin.id);
      const before = [...list.querySelectorAll('.plugins-list-item')].find((candidate) => {
        const candidateIndex = visible.findIndex((entry) => entry.id === candidate.dataset.pluginId);
        return candidateIndex > targetIndex;
      });
      list.insertBefore(item, before || null);
      return false;
    }
    if (wasVisible && current) {
      current.remove();
      if (!list.querySelector('.plugins-list-item')) appendListEmptyState(list);
    }
    return selectionCleared;
  }

  function syncInspectorRuntime(plugin, { toolsChanged = true } = {}) {
    const tone = pluginStatusTone(plugin.status);
    const status = find('plugins-detail-status');
    if (status) {
      status.className = `plugins-status-badge plugins-status-${tone}`;
      status.textContent = pluginStatusLabel(plugin.status);
    }
    const toolCount = find('plugins-detail-tools-count');
    if (toolCount) toolCount.textContent = `${pluginToolCount(plugin)} tool${pluginToolCount(plugin) === 1 ? '' : 's'}`;

    const openUI = find('plugins-detail-open-ui');
    const start = find('plugins-detail-start');
    const stop = find('plugins-detail-stop');
    const restart = find('plugins-detail-restart');
    const update = find('plugins-detail-update');
    const edit = find('plugins-detail-edit');
    const remove = find('plugins-detail-remove');
    if (openUI) {
      openUI.hidden = !hasPluginUI(plugin);
      openUI.disabled = tone === 'connecting';
    }
    if (start) {
      start.hidden = tone === 'connected';
      start.disabled = tone === 'connecting';
      start.textContent = tone === 'connecting' ? 'Starting…' : tone === 'error' ? 'Retry' : 'Start';
    }
    if (stop) stop.hidden = tone !== 'connected';
    if (restart) restart.hidden = tone !== 'connected';
    if (update) {
      update.hidden = !hasUpdate(plugin);
      update.textContent = hasUpdate(plugin) ? `Update to v${plugin.updateAvailable}` : 'Update';
    }
    if (edit) edit.hidden = plugin.plugin === true;
    if (remove) {
      remove.hidden = false;
      remove.textContent = plugin.plugin === true ? 'Uninstall' : 'Delete';
    }
    if (toolsChanged) renderTools(plugin);
  }

  function patchPluginRuntime(id, nextRuntime) {
    const index = state.plugins.findIndex((plugin) => plugin.id === id);
    if (index < 0) return null;
    const previous = state.plugins[index];
    const next = { ...previous };
    let statusChanged = false;
    let toolsChanged = false;
    if (nextRuntime.status !== undefined && previous.status !== nextRuntime.status) {
      next.status = nextRuntime.status;
      statusChanged = true;
    }
    if (nextRuntime.tools !== undefined && !toolsAreEqual(previous.tools, nextRuntime.tools)) {
      next.tools = nextRuntime.tools;
      toolsChanged = true;
    }
    if (!statusChanged && !toolsChanged) return previous;
    state.plugins[index] = next;
    const selectionCleared = syncListMembership(previous, next, { statusChanged, toolsChanged });
    if (selectionCleared) renderInspector();
    else if (state.selectedId === id) syncInspectorRuntime(next, { toolsChanged });
    renderToolbar();
    return next;
  }

  function setPreference(method, plugin, checkbox) {
    const before = checkbox.checked;
    checkbox.disabled = true;
    void rpcClient(method, { id: plugin.id, enabled: before })
      .then(() => {
        const current = state.plugins.find((candidate) => candidate.id === plugin.id);
        if (current) {
          if (method === 'plugin.set_autostart') current.autostart = before;
          else current.autoUpdate = before;
        }
        toast(before ? 'Preference enabled' : 'Preference disabled', 'success');
      })
      .catch((error) => {
        checkbox.checked = !before;
        toast(error.message || 'Could not save preference', 'error');
      })
      .finally(() => {
        checkbox.disabled = false;
        renderInspector();
      });
  }

  function renderPreferences(plugin) {
    const autostart = find('plugins-detail-autostart');
    const autoupdate = find('plugins-detail-autoupdate');
    const updateRow = find('plugins-autoupdate-row');
    if (autostart) {
      autostart.checked = Boolean(plugin.autostart);
      autostart.onchange = () => setPreference('plugin.set_autostart', plugin, autostart);
    }
    if (updateRow) updateRow.hidden = !canAutoUpdate(plugin);
    if (autoupdate) {
      autoupdate.checked = Boolean(plugin.autoUpdate);
      autoupdate.onchange = () => setPreference('plugin.set_autoupdate', plugin, autoupdate);
    }
  }

  function renderInspector() {
    const empty = find('plugins-empty');
    const detail = find('plugins-detail');
    const plugin = selectedPlugin();
    if (!empty || !detail) return;
    if (!plugin) setMobilePane('list');
    empty.hidden = Boolean(plugin);
    detail.hidden = !plugin;
    if (!plugin) return;

    const icon = find('plugins-detail-icon');
    if (icon) icon.replaceChildren(pluginIcon(plugin));
    const type = find('plugins-detail-type');
    const title = find('plugins-detail-title');
    const id = find('plugins-detail-id');
    if (type) type.textContent = pluginTypeLabel(plugin);
    if (title) title.textContent = plugin.name || plugin.id || 'Unnamed integration';
    if (id) id.textContent = plugin.id || '—';

    const transport = find('plugins-detail-transport');
    if (transport) transport.textContent = pluginTransport(plugin);
    syncInspectorRuntime(plugin);
    const connection = find('plugins-connection-list');
    if (connection) {
      connection.replaceChildren();
      renderConnection(plugin);
    }
    const note = find('plugins-detail-note');
    if (note) note.textContent = plugin.installPath ? `Installed at ${plugin.installPath}` : '';
    renderPreferences(plugin);
  }

  function selectPlugin(id, { focus = false } = {}) {
    if (!state.plugins.some((plugin) => plugin.id === id)) return;
    state.selectedId = id;
    renderList();
    renderInspector();
    if (focus) root.querySelector(`[data-plugin-id="${CSS.escape(id)}"]`)?.focus();
  }

  async function refresh() {
    const token = ++refreshToken;
    state.loading = true;
    state.error = null;
    setSyncState('Syncing…');
    renderList();
    try {
      const result = await rpcClient('plugin.list');
      if (token !== refreshToken) return;
      state.plugins = Array.isArray(result?.plugins) ? result.plugins : [];
      state.loading = false;
      if (!state.plugins.some((plugin) => plugin.id === state.selectedId)) state.selectedId = state.plugins[0]?.id || null;
      reconcileSelectedPlugin();
      setSyncState('Synced', 'ready');
      renderList();
      renderInspector();
    } catch (error) {
      if (token !== refreshToken) return;
      state.loading = false;
      state.error = error;
      setSyncState('Sync failed', 'error');
      renderList();
      renderInspector();
    }
  }

  async function checkUpdates() {
    const button = find('plugins-check-updates');
    if (button) {
      button.disabled = true;
      button.textContent = 'Checking…';
    }
    try {
      const result = await rpcClient('plugin.check_updates');
      const updates = new Map((Array.isArray(result?.plugins) ? result.plugins : []).map((entry) => [entry.pluginId || entry.id, entry.version]));
      state.plugins = state.plugins.map((plugin) => ({ ...plugin, updateAvailable: updates.get(plugin.id) || '' }));
      renderList();
      renderInspector();
      toast(updates.size ? `${updates.size} update${updates.size === 1 ? '' : 's'} available` : 'All plugins are up to date', 'success');
    } catch (error) {
      toast(error.message || 'Could not check for updates', 'error');
    } finally {
      if (button) {
        button.disabled = false;
        button.textContent = 'Check updates';
      }
    }
  }

  async function runSelectedAction(buttonID, operation, successCopy) {
    const plugin = selectedPlugin();
    const button = find(buttonID);
    if (!plugin || !button) return;
    const token = ++actionToken;
    button.disabled = true;
    const previous = button.textContent;
    button.textContent = 'Working…';
    try {
      await operation(plugin);
      toast(successCopy(plugin), 'success');
    } catch (error) {
      toast(error.message || 'Plugin action failed', 'error');
    } finally {
      if (token === actionToken) {
        button.disabled = false;
        button.textContent = previous;
      }
      await refresh();
    }
  }

  async function connectPlugin(plugin, buttonID = null) {
    if (!plugin || startingPluginIDs.has(plugin.id)) return;
    startingPluginIDs.add(plugin.id);
    const button = buttonID ? find(buttonID) : null;
    if (button) button.disabled = true;
    patchPluginRuntime(plugin.id, { status: 'connecting' });
    try {
      const result = await rpcClient('plugin.test', { id: plugin.id });
      patchPluginRuntime(plugin.id, {
        status: 'connected',
        tools: Array.isArray(result?.tools) ? result.tools : [],
      });
      toast(`${plugin.name || plugin.id} started`, 'success');
    } catch (error) {
      patchPluginRuntime(plugin.id, { status: 'error' });
      toast(error.message || `${plugin.name || plugin.id} could not start`, 'error');
    } finally {
      startingPluginIDs.delete(plugin.id);
      const current = selectedPlugin();
      if (current?.id === plugin.id) syncInspectorRuntime(current, { toolsChanged: false });
    }
  }

  async function runRuntimeAction(buttonID, operation, successCopy, runtime) {
    const plugin = selectedPlugin();
    const button = find(buttonID);
    if (!plugin || !button) return;
    const token = ++actionToken;
    button.disabled = true;
    const previous = button.textContent;
    button.textContent = 'Working…';
    try {
      const result = await operation(plugin);
      patchPluginRuntime(plugin.id, runtime(result));
      toast(successCopy(plugin), 'success');
    } catch (error) {
      toast(error.message || 'Plugin action failed', 'error');
    } finally {
      if (token === actionToken) {
        button.disabled = false;
        button.textContent = previous;
      }
    }
  }

  async function startSelected() {
    await connectPlugin(selectedPlugin(), 'plugins-detail-start');
  }

  async function stopSelected() {
    await runRuntimeAction(
      'plugins-detail-stop',
      (plugin) => rpcClient('plugin.stop', { id: plugin.id }),
      (plugin) => `${plugin.name} stopped`,
      () => ({ status: 'idle', tools: [] }),
    );
  }

  async function restartSelected() {
    await runRuntimeAction('plugins-detail-restart', async (plugin) => {
      await rpcClient('plugin.stop', { id: plugin.id });
      return rpcClient('plugin.test', { id: plugin.id });
    }, (plugin) => `${plugin.name} restarted`, (result) => ({
      status: 'connected',
      tools: Array.isArray(result?.tools) ? result.tools : [],
    }));
  }

  async function updateSelected() {
    await runSelectedAction('plugins-detail-update', (plugin) => rpcClient('plugin.update', { id: plugin.id }, { timeoutMs: 300000 }), (plugin) => `${plugin.name} updated`);
  }

  async function removeSelected() {
    const plugin = selectedPlugin();
    if (!plugin) return;
    const action = plugin.plugin === true ? 'Uninstall plugin' : 'Delete MCP server';
    const message = plugin.plugin === true
      ? `${plugin.name || plugin.id} and its installed files will be removed.`
      : `${plugin.name || plugin.id} will be removed from NusaShell.`;
    if (!await confirmDialog(action, message, plugin.plugin === true ? 'Uninstall' : 'Delete')) return;
    try {
      await rpcClient(plugin.plugin === true ? 'plugin.uninstall' : 'plugin.delete', { id: plugin.id });
      state.selectedId = null;
      toast(plugin.plugin === true ? 'Plugin uninstalled' : 'MCP server deleted', 'success');
      await refresh();
    } catch (error) {
      toast(error.message || 'Could not remove integration', 'error');
    }
  }

  function openSelectedUI() {
    const plugin = selectedPlugin();
    if (!plugin || !hasPluginUI(plugin)) return;
    const opened = launchPluginWindow({ id: plugin.id.replace(/^plugin:/, ''), name: plugin.name, ui: plugin.manifest?.ui });
    if (!opened) toast(`Allow pop-ups to open “${plugin.name}”.`, 'error');
    if (!opened || pluginStatusTone(plugin.status) === 'connected') return;
    // Keep window.open inside the click handler so browsers do not classify it
    // as a blocked popup. Start the MCP connection in the background and patch
    // only this plugin's row/inspector as the authoritative RPC settles.
    void connectPlugin(plugin);
  }

  async function editMCP(server = null) {
    const mcp = server?.manifest?.mcp || {};
    const result = await dialog({
      title: server ? 'Edit MCP server' : 'Add MCP server',
      message: 'stdio starts a local process; SSE/HTTP connect to a remote endpoint.',
      fields: [
        { name: 'name', label: 'Name', value: server?.name || '', placeholder: 'e.g. filesystem' },
        { name: 'transport', label: 'Transport', tag: 'select', value: mcp.transport || 'stdio', options: TRANSPORT_OPTIONS, onChange: updateTransportFields },
        { name: 'command', label: 'Command (stdio)', value: mcp.command || '', placeholder: 'e.g. npx' },
        { name: 'args', label: 'Arguments (stdio)', value: (mcp.args || []).join(' '), placeholder: '-y server-filesystem /path' },
        { name: 'url', label: 'URL (SSE/HTTP)', value: mcp.url || '', placeholder: 'https://example.com/mcp' },
        { name: 'headers', label: 'Headers (SSE/HTTP)', tag: 'textarea', value: Object.entries(mcp.headers || {}).map(([key, value]) => `${key}: ${value}`).join('\n'), placeholder: 'Authorization: Bearer <token>' },
        { name: 'env', label: 'Environment (stdio)', tag: 'textarea', value: Object.entries(mcp.env || {}).map(([key, value]) => `${key}=${value}`).join('\n'), placeholder: 'KEY=VALUE' },
      ],
      actions: [{ label: 'Cancel', value: null }, { label: 'Save', value: 'save', primary: true }],
    });
    if (result.value !== 'save') return;
    const fields = result.fields;
    const transport = fields.transport || 'stdio';
    if (!fields.name?.trim()) return toast('Name is required', 'error');
    if (transport === 'stdio' && !fields.command?.trim()) return toast('Command is required for stdio servers', 'error');
    if (transport !== 'stdio' && !/^https?:\/\//i.test(fields.url?.trim() || '')) return toast('An http(s) URL is required for remote servers', 'error');
    try {
      await rpcClient('plugin.save', {
        id: server?.id || undefined,
        name: fields.name.trim(),
        transport,
        command: transport === 'stdio' ? fields.command.trim() : undefined,
        args: transport === 'stdio' ? String(fields.args || '').split(/\s+/).filter(Boolean) : undefined,
        env: transport === 'stdio' ? parseKeyValueLines(fields.env, '=') : undefined,
        url: transport !== 'stdio' ? fields.url.trim() : undefined,
        headers: transport !== 'stdio' ? parseKeyValueLines(fields.headers, ':') : undefined,
        autostart: Boolean(server?.autostart),
      });
      toast('MCP server saved', 'success');
      await refresh();
    } catch (error) {
      toast(error.message || 'Could not save MCP server', 'error');
    }
  }

  function showInstallError(message) {
    const node = find('plugins-install-error');
    if (!node) return;
    node.textContent = message;
    node.hidden = false;
  }

  function clearInstallError() {
    const node = find('plugins-install-error');
    if (node) node.hidden = true;
  }

  function setInstallTab(tab) {
    state.installTab = tab;
    clearInstallError();
    const footer = find('plugins-install-footer');
    const confirm = find('plugins-install-confirm');
    if (footer) footer.hidden = tab === 'catalog';
    if (confirm) confirm.hidden = tab === 'catalog';
    root.querySelectorAll('[data-plugin-install-tab]').forEach((button) => {
      const active = button.dataset.pluginInstallTab === tab;
      button.classList.toggle('is-active', active);
      button.setAttribute('aria-selected', String(active));
    });
    root.querySelectorAll('[data-plugin-install-panel]').forEach((panel) => {
      panel.hidden = panel.dataset.pluginInstallPanel !== tab;
      panel.classList.toggle('is-active', panel.dataset.pluginInstallPanel === tab);
    });
  }

  function renderCatalog() {
    const list = find('plugins-catalog-list');
    if (!list) return;
    const query = String(find('plugins-catalog-search')?.value || '').trim().toLocaleLowerCase();
    const filtered = state.catalog.filter((entry) => [entry.name, entry.id, entry.pluginId, entry.description].filter(Boolean).join(' ').toLocaleLowerCase().includes(query));
    list.replaceChildren();
    if (!filtered.length) {
      list.append(el('p', { class: 'plugins-catalog-empty', role: 'status', text: query ? 'No catalog plugin matches that search.' : 'No catalog plugins are available.' }));
      return;
    }
    for (const entry of filtered) {
      const installed = state.plugins.find((plugin) => plugin.id === entry.pluginId);
      const sameVersion = Boolean(installed && installed.version === entry.version);
      const action = !installed ? 'Install' : sameVersion ? 'Installed' : 'Update';
      const button = el('button', { class: 'plugins-catalog-action', type: 'button', text: action, disabled: sameVersion });
      button.addEventListener('click', () => { void installCatalogEntry(entry); });
      const nameLine = el('div', { class: 'plugins-catalog-name-line' }, [
        el('strong', { text: entry.name || entry.pluginId || entry.id }),
        el('span', {
          class: 'plugins-catalog-version',
          text: `v${entry.version || '—'}`,
        }),
      ]);
      list.append(el('article', { class: 'plugins-catalog-item', role: 'listitem' }, [
        el('span', { class: 'plugins-catalog-icon', 'aria-hidden': 'true' }, pluginIcon(entry)),
        el('div', { class: 'plugins-catalog-copy' }, [
          nameLine,
          el('p', { text: entry.description || entry.pluginId || 'Catalog plugin' }),
        ]),
        button,
      ]));
    }
  }

  async function loadCatalog() {
    const result = await rpcClient('plugin.catalog');
    state.catalog = Array.isArray(result?.plugins) ? result.plugins : [];
    renderCatalog();
  }

  async function installPayload(payload, successCopy = 'Plugin installed') {
    if (state.installing) return;
    state.installing = true;
    const confirm = find('plugins-install-confirm');
    if (confirm) {
      confirm.disabled = true;
      confirm.textContent = 'Installing…';
    }
    try {
      await rpcClient('plugin.install', payload, { timeoutMs: 300000 });
      closeInstall();
      toast(successCopy, 'success');
      await refresh();
    } catch (error) {
      showInstallError(error.message || 'Plugin installation failed');
    } finally {
      state.installing = false;
      if (confirm) {
        confirm.disabled = false;
        confirm.textContent = 'Install';
      }
    }
  }

  async function installCatalogEntry(entry) {
    await installPayload({ source: 'catalog', id: entry.id }, `${entry.name || 'Plugin'} installed`);
  }

  function confirmInstall() {
    if (state.installTab === 'catalog') return toast('Choose a plugin from the catalog', 'info');
    if (state.installTab === 'github') {
      const url = find('plugins-github-url')?.value.trim();
      if (!url) return showInstallError('Repository URL is required');
      return void installPayload({ source: 'github', url, subdir: find('plugins-github-subdir')?.value.trim(), ref: find('plugins-github-ref')?.value.trim() });
    }
    const file = find('plugins-zip-file')?.files?.[0];
    if (!file) return showInstallError('Choose a .zip file first');
    const reader = new FileReader();
    reader.onload = (event) => {
      const encoded = String(event.target?.result || '').split(',')[1];
      if (!encoded) return showInstallError('Could not read the selected archive');
      void installPayload({ source: 'zip', data: encoded });
    };
    reader.onerror = () => showInstallError('Could not read the selected archive');
    reader.readAsDataURL(file);
  }

  function focusableInstallNodes() {
    const dialogNode = root.querySelector('.plugins-install-dialog');
    return [...(dialogNode?.querySelectorAll('button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])') || [])]
      .filter((node) => !node.hidden && node.getAttribute('aria-hidden') !== 'true');
  }

  async function openInstall() {
    const overlay = find('plugins-install-overlay');
    if (!overlay) return;
    installReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    state.installTab = 'catalog';
    state.catalog = [];
    state.installing = false;
    clearInstallError();
    if (find('plugins-catalog-search')) find('plugins-catalog-search').value = '';
    if (find('plugins-github-url')) find('plugins-github-url').value = '';
    if (find('plugins-github-subdir')) find('plugins-github-subdir').value = '';
    if (find('plugins-github-ref')) find('plugins-github-ref').value = '';
    if (find('plugins-zip-file')) find('plugins-zip-file').value = '';
    if (find('plugins-zip-name')) find('plugins-zip-name').textContent = 'No file selected';
    find('plugins-catalog-list')?.replaceChildren(el('p', { class: 'plugins-catalog-empty', role: 'status', text: 'Loading catalog…' }));
    setInstallTab('catalog');
    overlay.hidden = false;
    overlay.setAttribute('aria-hidden', 'false');
    find('plugins-install-tab-catalog')?.focus();
    try {
      await loadCatalog();
    } catch (error) {
      showInstallError(error.message || 'Catalog unavailable');
      find('plugins-catalog-list')?.replaceChildren(el('p', { class: 'plugins-catalog-empty', role: 'status', text: 'Catalog unavailable.' }));
    }
  }

  function closeInstall() {
    const overlay = find('plugins-install-overlay');
    if (!overlay) return;
    overlay.hidden = true;
    overlay.setAttribute('aria-hidden', 'true');
    state.installing = false;
    if (installReturnFocus?.isConnected && !root.hidden) installReturnFocus.focus();
    installReturnFocus = null;
  }

  function closeOverlays() {
    closeInstall();
  }

  function wireInstall() {
    const overlay = find('plugins-install-overlay');
    if (!overlay) return;
    root.querySelectorAll('[data-plugin-install-tab]').forEach((tab) => tab.addEventListener('click', () => setInstallTab(tab.dataset.pluginInstallTab)));
    bindTablistKeyboard(find('plugins-install-tabs'));
    find('plugins-install-close')?.addEventListener('click', closeInstall);
    find('plugins-install-cancel')?.addEventListener('click', closeInstall);
    find('plugins-install-confirm')?.addEventListener('click', confirmInstall);
    find('plugins-catalog-search')?.addEventListener('input', debounce(renderCatalog, 120));
    find('plugins-zip-file')?.addEventListener('change', (event) => {
      const file = event.target.files?.[0];
      if (find('plugins-zip-name')) find('plugins-zip-name').textContent = file?.name || 'No file selected';
    });
    overlay.addEventListener('mousedown', (event) => { if (event.target === overlay && !state.installing) closeInstall(); });
    installKeyHandler = (event) => {
      if (overlay.hidden) return;
      if (event.key === 'Escape' && !state.installing) {
        event.preventDefault();
        closeInstall();
        return;
      }
      if (event.key !== 'Tab') return;
      const nodes = focusableInstallNodes();
      if (!nodes.length) return;
      const first = nodes[0];
      const last = nodes[nodes.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', installKeyHandler, true);
    unregisterInstallDismiss = registerOverlayDismiss(closeInstall);
  }

  function init() {
    if (initialized) return controller;
    initialized = true;
    find('plugins-search')?.addEventListener('input', debounce((event) => {
      state.query = event.target.value;
      reconcileSelectedPlugin();
      renderList();
      renderInspector();
    }, 120));
    root.querySelectorAll('[data-plugin-filter]').forEach((button) => button.addEventListener('click', () => {
      state.filter = button.dataset.pluginFilter || 'all';
      reconcileSelectedPlugin();
      renderList();
      renderInspector();
    }));
    find('plugins-check-updates')?.addEventListener('click', () => { void checkUpdates(); });
    find('plugins-add-mcp')?.addEventListener('click', () => { void editMCP(); });
    find('plugins-install')?.addEventListener('click', () => { void openInstall(); });
    find('plugins-detail-open-ui')?.addEventListener('click', openSelectedUI);
    find('plugins-detail-start')?.addEventListener('click', () => { void startSelected(); });
    find('plugins-detail-stop')?.addEventListener('click', () => { void stopSelected(); });
    find('plugins-detail-restart')?.addEventListener('click', () => { void restartSelected(); });
    find('plugins-detail-update')?.addEventListener('click', () => { void updateSelected(); });
    find('plugins-detail-edit')?.addEventListener('click', () => { void editMCP(selectedPlugin()); });
    find('plugins-detail-remove')?.addEventListener('click', () => { void removeSelected(); });
    const page = pageNode();
    if (page?.nodeType === 1 && !page.dataset.mobilePane) page.dataset.mobilePane = 'list';
    ensureMobileBack();
    wireInstall();
    renderList();
    renderInspector();
    return controller;
  }

  function destroy() {
    unregisterInstallDismiss?.();
    if (installKeyHandler) document.removeEventListener('keydown', installKeyHandler, true);
    closeOverlays();
  }

  const controller = { init, refresh, closeOverlays, destroy, openInstall, selectPlugin, state };
  return controller;
}
