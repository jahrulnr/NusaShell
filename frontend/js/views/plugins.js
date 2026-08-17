// Plugins workspace: one catalog for native MCP servers and installed
// plugins, including plugins that expose a browser UI.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';
import { openPluginWindow as openPluginWindowInShell } from '../plugin-window.js';
import { hasPluginUI, pluginKind, pluginRowMeta } from './plugins-model.js';

export { hasPluginUI, pluginKind, pluginRowMeta } from './plugins-model.js';

let servers = [];
let pluginCatalog = [];
let currentServer = null;
let drawerReturnFocus = null;

const STATES = ['idle', 'connecting', 'connected', 'error'];

// Catalog entries fetched from the backend (plugin.catalog).
let catalogEntries = [];

let installTab = 'catalog';
let installReturnFocus = null;

export async function initPlugins() {
  document.getElementById('plugins-add-mcp')?.addEventListener('click', () => addMcp());
  document.getElementById('plugins-install-plugin')?.addEventListener('click', () => installPlugin());
  wireInstallDialog();
  wireDrawer();
  await refresh();
}

export async function refresh() {
  const serverResult = await rpc('plugin.list');
  servers = serverResult.plugins ?? [];
  renderList();
  if (currentServer) {
    const updated = servers.find((server) => server.id === currentServer.id);
    if (updated) openDrawer(updated);
    else closeDrawer();
  }
  void refreshUpdateBadges();
}

// refreshUpdateBadges asks the backend which catalog plugins have a newer
// version than installed and paints a "Update available" badge on rows.
async function refreshUpdateBadges() {
  const badgeHost = document.getElementById('plugins-update-badge');
  let updates = [];
  try {
    const result = await rpc('plugin.check_updates');
    updates = result.plugins ?? [];
  } catch {
    updates = [];
  }
  const count = updates.length;
  if (badgeHost) {
    badgeHost.textContent = count ? String(count) : '';
    badgeHost.hidden = count === 0;
    badgeHost.title = count ? `${count} plugin update${count === 1 ? '' : 's'} available` : '';
  }
  const available = new Map(updates.map((u) => [u.pluginId, u.version]));
  for (const row of document.querySelectorAll('.plugin-row')) {
    const id = row.dataset.pluginId;
    let badge = row.querySelector('.plugin-row-update-badge');
    const version = available.get(id);
    if (version) {
      if (!badge) {
        badge = el('span', { class: 'plugin-row-update-badge', text: '↑ update' });
        row.append(badge);
      } else {
        badge.textContent = '↑ ' + version;
      }
    } else if (badge) {
      badge.remove();
    }
  }
}

function renderList() {
  const list = document.getElementById('plugin-table');
  if (!list) return;
  list.replaceChildren();
  if (!servers.length) {
    list.append(el('div', { class: 'empty-state' },
      el('div', { class: 'empty-mark', text: '🔌' }),
      el('strong', { text: 'No plugins or MCP servers yet' }),
      el('span', { text: 'Add an MCP server to expose tools to the agent, or install a plugin from the plugins directory.' }),
    ));
    return;
  }
  for (const server of servers) {
    const kind = pluginKind(server);
    const row = el('button', {
      class: `plugin-row${currentServer?.id === server.id ? ' is-selected' : ''}`,
      type: 'button',
      dataset: { pluginId: server.id },
      'aria-label': `Open ${server.name} details`,
    });
    const iconValue = server.icon || server.name;
    const icon = el('div', { class: `plugin-row-icon ${server.plugin ? 'plugin-row-icon-plugin' : ''}` },
      iconNode(iconValue, shortIcon(iconValue)),
    );
    const info = el('div', { class: 'plugin-row-info' },
      el('div', { class: 'plugin-row-name' },
        el('span', { text: server.name }),
        server.plugin ? el('span', { class: 'plugin-row-badge', text: hasPluginUI(server) ? 'MCP + UI' : 'MCP plugin' }) : null,
      ),
      el('div', { class: 'plugin-row-meta', text: pluginRowMeta(server, kind) }),
      server.tools?.length ? el('div', { class: 'plugin-row-tools' }, server.tools.slice(0, 12).map((tool) => el('span', { class: 'plugin-tool-chip', text: tool.name }))) : null,
    );
    const state = el('span', { class: `plugin-row-state ${stateClass(server.status)}`, text: server.status || 'idle' });
    row.append(icon, info, state);
    row.addEventListener('click', () => openDrawer(server));
    list.append(row);
  }
}

function shortIcon(value) {
  return String(value || '🧩').slice(0, 2).toUpperCase();
}

function isImageIcon(icon) {
  return typeof icon === 'string' && /^(data:|https?:\/\/|file:\/\/\/)/i.test(icon);
}

// iconNode returns an <img> for image icons (data URL / http) or a text
// span for emoji/text icons. Image load failures fall back to the text.
function iconNode(icon, fallbackText) {
  if (isImageIcon(icon)) {
    const img = el('img', { class: 'plugin-icon-img', alt: '', loading: 'lazy' });
    img.addEventListener('error', () => {
      img.replaceWith(el('span', { text: fallbackText }));
    }, { once: true });
    img.src = icon;
    return img;
  }
  return el('span', { text: fallbackText });
}

function stateClass(status) {
  if (status === 'connected') return 'connected';
  if (status === 'error') return 'error';
  return 'idle';
}

function wireDrawer() {
  document.getElementById('plugin-drawer-close')?.addEventListener('click', closeDrawer);
  document.getElementById('plugin-drawer-overlay')?.addEventListener('click', closeDrawer);
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && currentServer) closeDrawer();
  });
  document.getElementById('plugin-btn-open-ui')?.addEventListener('click', () => {
    if (currentServer) openPluginWindow(currentServer);
  });
  document.getElementById('plugin-btn-start')?.addEventListener('click', () => { if (currentServer) void startServer(currentServer); });
  document.getElementById('plugin-btn-stop')?.addEventListener('click', () => { if (currentServer) void stopServer(currentServer); });
  document.getElementById('plugin-btn-restart')?.addEventListener('click', () => { if (currentServer) void restartServer(currentServer); });
  document.getElementById('plugin-btn-edit')?.addEventListener('click', () => { if (currentServer) addMcp(currentServer); });
  document.getElementById('plugin-btn-delete')?.addEventListener('click', () => { if (currentServer) void deleteServer(currentServer); });
  document.getElementById('plugin-btn-uninstall')?.addEventListener('click', () => { if (currentServer) void uninstallPlugin(currentServer); });
}

function openDrawer(server) {
  drawerReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  currentServer = server;
  renderList();

  const isPlugin = server.plugin === true;
  const drawerIcon = document.getElementById('plugin-drawer-icon');
  const drawerIconValue = server.icon || server.name;
  drawerIcon.replaceChildren(iconNode(drawerIconValue, shortIcon(drawerIconValue)));
  document.getElementById('plugin-drawer-title').textContent = server.name;
  document.getElementById('plugin-drawer-subtitle').textContent = pluginRowMeta(server);
  document.getElementById('plugin-btn-open-ui').hidden = !hasPluginUI(server);
  document.getElementById('plugin-btn-edit').hidden = isPlugin;
  document.getElementById('plugin-btn-uninstall').hidden = !isPlugin;
  document.getElementById('plugin-btn-delete').hidden = isPlugin;

  renderDrawerState(server);
  renderDrawerTools(server);
  renderDrawerManifest(server);
  renderDrawerAutoUpdate(server);
  renderDrawerAutoStart(server);

  const drawer = document.getElementById('plugin-drawer');
  const overlay = document.getElementById('plugin-drawer-overlay');
  drawer.hidden = false;
  drawer.inert = false;
  drawer.setAttribute('aria-hidden', 'false');
  overlay.setAttribute('aria-hidden', 'false');
  drawer.classList.add('active');
  overlay.classList.add('active');
  document.getElementById('plugin-drawer-close').focus();
}

function closeDrawer() {
  const drawer = document.getElementById('plugin-drawer');
  const overlay = document.getElementById('plugin-drawer-overlay');
  const wasOpen = drawer.classList.contains('active');
  drawer.classList.remove('active');
  overlay.classList.remove('active');
  drawer.setAttribute('aria-hidden', 'true');
  overlay.setAttribute('aria-hidden', 'true');
  drawer.inert = true;
  drawer.hidden = true;
  currentServer = null;
  renderList();
  if (wasOpen && drawerReturnFocus?.isConnected) drawerReturnFocus.focus();
  drawerReturnFocus = null;
}

function renderDrawerState(server) {
  const stateMachine = document.getElementById('plugin-state-machine');
  stateMachine.replaceChildren();
  const current = stateClass(server.status);
  STATES.forEach((state, index) => {
    if (index > 0) stateMachine.append(el('span', { class: 'state-arrow', text: '→' }));
    stateMachine.append(el('span', { class: `state-node${state === current ? ' current' : ''}`, text: state }));
  });
}

function renderDrawerAutoUpdate(server) {
  const section = document.getElementById('plugin-drawer-autoupdate');
  if (!section) return;
  section.hidden = !(server.plugin === true);
  if (server.plugin !== true) return;
  const toggle = document.getElementById('plugin-autoupdate-toggle');
  if (!toggle) return;
  toggle.checked = Boolean(server.autoUpdate);
  toggle.onchange = async () => {
    try {
      await rpc('plugin.set_autoupdate', { id: server.id, enabled: toggle.checked });
      toast(toggle.checked ? 'Auto update enabled' : 'Auto update disabled', 'success');
      const installed = servers.find((srv) => srv.id === server.id);
      if (installed) installed.autoUpdate = toggle.checked;
    } catch (error) {
      toast(error.message, 'error');
      toggle.checked = !toggle.checked;
    }
  };
}

function renderDrawerAutoStart(server) {
  const section = document.getElementById('plugin-drawer-autostart');
  if (!section) return;
  section.hidden = !(server.plugin === true);
  if (server.plugin !== true) return;
  const toggle = document.getElementById('plugin-autostart-toggle');
  if (!toggle) return;
  toggle.checked = Boolean(server.autostart);
  toggle.onchange = async () => {
    try {
      await rpc('plugin.set_autostart', { id: server.id, enabled: toggle.checked });
      toast(toggle.checked ? 'Auto start enabled' : 'Auto start disabled', 'success');
      const installed = servers.find((srv) => srv.id === server.id);
      if (installed) installed.autostart = toggle.checked;
    } catch (error) {
      toast(error.message, 'error');
      toggle.checked = !toggle.checked;
    }
  };
}

function renderDrawerTools(server) {
  const list = document.getElementById('plugin-tools-list');
  const count = document.getElementById('plugin-tool-count');
  const tools = server.tools ?? [];
  count.textContent = String(tools.length);
  list.replaceChildren();
  if (!tools.length) {
    list.append(el('div', { class: 'tools-empty', text: 'No tools loaded. Click Start to connect.' }));
    return;
  }
  for (const tool of tools) {
    list.append(el('div', { class: 'tool-item' },
      el('div', { class: 'tool-item-info' },
        el('div', { class: 'tool-item-name', text: tool.name }),
        tool.description ? el('div', { class: 'tool-item-desc', text: tool.description }) : null,
      ),
    ));
  }
}

function renderDrawerManifest(server) {
  const info = document.getElementById('plugin-manifest-info');
  const rows = [
    ['id', server.id],
    ['type', server.plugin ? (hasPluginUI(server) ? 'MCP + UI plugin' : 'MCP plugin') : 'MCP server'],
    ['command', server.command || '—'],
    ['args', (server.args || []).join(' ') || '—'],
    ['status', server.status || 'idle'],
  ];
  if (server.plugin) {
    rows.splice(2, 0, ['version', server.version || '—']);
    if (server.category) rows.splice(3, 0, ['category', server.category]);
    if (server.installPath) rows.push(['install path', server.installPath]);
  }
  info.replaceChildren(...rows.map(([key, value]) => el('div', { class: 'manifest-row' },
    el('span', { class: 'manifest-key', text: key }),
    el('span', { class: 'manifest-val', text: value }),
  )));
}

async function startServer(server) {
  const button = document.getElementById('plugin-btn-start');
  button.disabled = true;
  button.textContent = 'Starting…';
  try {
    const result = await rpc('plugin.test', { id: server.id });
    toast(`Started · ${result.tools?.length ?? 0} tools`, 'success');
  } catch (error) {
    toast(error.message, 'error');
  } finally {
    button.disabled = false;
    button.textContent = 'Start';
    await refresh();
  }
}

async function stopServer(server) {
  try {
    await rpc('plugin.stop', { id: server.id });
    toast('MCP server stopped', 'success');
    await refresh();
  } catch (error) {
    toast(error.message, 'error');
  }
}

async function restartServer(server) {
  try {
    await rpc('plugin.stop', { id: server.id });
    const result = await rpc('plugin.test', { id: server.id });
    toast(`Restarted · ${result.tools?.length ?? 0} tools`, 'success');
  } catch (error) {
    toast(error.message, 'error');
  } finally {
    await refresh();
  }
}

async function deleteServer(server) {
  const ok = await confirmDialog('Delete MCP server', `"${server.name}" will be removed.`, 'Delete');
  if (!ok) return;
  try {
    await rpc('plugin.delete', { id: server.id });
    toast('MCP server deleted', 'success');
    closeDrawer();
    await refresh();
  } catch (error) {
    toast(error.message, 'error');
  }
}

async function uninstallPlugin(server) {
  const pluginID = server.id;
  const ok = await confirmDialog('Uninstall plugin', `"${server.name}" and its files under plugins/${pluginID} will be removed.`, 'Uninstall');
  if (!ok) return;
  try {
    await rpc('plugin.uninstall', { id: pluginID });
    toast('Plugin uninstalled', 'success');
    closeDrawer();
    await refresh();
  } catch (error) {
    toast(error.message, 'error');
  }
}

function openPluginWindow(server) {
  if (!hasPluginUI(server)) return;
  openPluginWindowInShell({
    id: server.id.replace(/^plugin:/, ''),
    name: server.name,
    ui: server.manifest?.ui,
  });
}

async function addMcp(server = null) {
  const result = await dialog({
    title: server ? 'Edit MCP server' : 'Add MCP server',
    message: 'A stdio MCP server launched as a child process. Environment entries use KEY=VALUE.',
    fields: [
      { name: 'name', label: 'Name', value: server?.name ?? '', placeholder: 'e.g. filesystem' },
      { name: 'command', label: 'Command', value: server?.command ?? '', placeholder: 'e.g. npx' },
      { name: 'args', label: 'Arguments (space separated)', value: (server?.args ?? []).join(' '), placeholder: '-y @modelcontextprotocol/server-filesystem /path' },
      { name: 'env', label: 'Environment (optional)', value: Object.entries(server?.env ?? {}).map(([key, value]) => `${key}=${value}`).join('\n'), placeholder: 'KEY=VALUE', tag: 'textarea' },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save', primary: true },
    ],
  });
  if (result.value !== 'save') return;
  const { name, command, args, env } = result.fields;
  if (!name.trim() || !command.trim()) {
    toast('Name and command are required', 'error');
    return;
  }
  const envObject = {};
  for (const line of env.split('\n')) {
    const separator = line.indexOf('=');
    if (separator > 0) envObject[line.slice(0, separator).trim()] = line.slice(separator + 1).trim();
  }
  try {
    await rpc('plugin.save', {
      id: server?.id || undefined,
      name: name.trim(),
      command: command.trim(),
      args: args.split(/\s+/).filter(Boolean),
      env: envObject,
      enabled: server?.enabled !== false,
    });
    toast('MCP server saved', 'success');
    await refresh();
  } catch (error) {
    toast(error.message, 'error');
  }
}

function wireInstallDialog() {
  const overlay = document.getElementById('plugin-install-overlay');
  const tabs = overlay.querySelectorAll('.plugin-install-tab');
  tabs.forEach((tab) => tab.addEventListener('click', () => setInstallTab(tab.dataset.tab)));
  document.getElementById('plugin-install-close')?.addEventListener('click', closeInstallDialog);
  document.getElementById('plugin-install-cancel')?.addEventListener('click', closeInstallDialog);
  document.getElementById('plugin-install-confirm')?.addEventListener('click', () => confirmInstall());
  document.getElementById('plugin-catalog-search')?.addEventListener('input', (event) => renderCatalogList(event.target.value));
  overlay?.addEventListener('mousedown', (event) => { if (event.target === overlay) closeInstallDialog(); });
  document.getElementById('plugin-install-zip-file')?.addEventListener('change', (event) => {
    const file = event.target.files[0];
    document.getElementById('plugin-install-zip-name').textContent = file ? file.name : 'No file selected';
  });
  document.addEventListener('keydown', (event) => { if (event.key === 'Escape' && !overlay.hidden) closeInstallDialog(); });
}

function setInstallTab(tab) {
  installTab = tab;
  clearInstallError();
  const overlay = document.getElementById('plugin-install-overlay');
  overlay.querySelectorAll('.plugin-install-tab').forEach((t) => {
    const active = t.dataset.tab === tab;
    t.classList.toggle('active', active);
    t.setAttribute('aria-selected', String(active));
  });
  overlay.querySelectorAll('.plugin-install-panel').forEach((panel) => {
    panel.classList.toggle('active', panel.dataset.panel === tab);
  });
}

function showInstallError(message) {
  const banner = document.getElementById('plugin-install-error');
  if (!banner) return;
  banner.textContent = message;
  banner.hidden = false;
}

function clearInstallError() {
  const banner = document.getElementById('plugin-install-error');
  if (banner) banner.hidden = true;
}

export async function installPlugin() {
  installReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  clearInstallError();
  const overlay = document.getElementById('plugin-install-overlay');
  document.getElementById('plugin-catalog-search').value = '';
  const list = document.getElementById('plugin-catalog-list');
  list.replaceChildren(el('div', { class: 'plugin-catalog-empty', text: 'Loading catalog…' }));
  setInstallTab('catalog');
  overlay.hidden = false;
  overlay.setAttribute('aria-hidden', 'false');
  document.getElementById('plugin-install-tab-catalog').focus();
  try {
    await loadCatalog();
    renderCatalogList();
  } catch (error) {
    list.replaceChildren(el('div', { class: 'plugin-catalog-empty', text: `Catalog unavailable: ${error.message}` }));
  }
}

async function loadCatalog() {
  const result = await rpc('plugin.catalog');
  catalogEntries = result.plugins ?? [];
}

function closeInstallDialog() {
  clearInstallError();
  const overlay = document.getElementById('plugin-install-overlay');
  overlay.hidden = true;
  overlay.setAttribute('aria-hidden', 'true');
  document.getElementById('plugin-install-zip-file').value = '';
  document.getElementById('plugin-install-zip-name').textContent = 'No file selected';
  document.getElementById('plugin-install-github-url').value = '';
  document.getElementById('plugin-install-github-subdir').value = '';
  document.getElementById('plugin-install-github-ref').value = '';
  if (installReturnFocus?.isConnected) installReturnFocus.focus();
  installReturnFocus = null;
}

function renderCatalogList(query = '') {
  const list = document.getElementById('plugin-catalog-list');
  if (!list) return;
  list.replaceChildren();
  const normalized = query.trim().toLowerCase();
  const installed = new Set(pluginCatalog.map((plugin) => plugin.id));
  const filtered = normalized
    ? catalogEntries.filter((item) => (
        item.name.toLowerCase().includes(normalized) ||
        item.id.toLowerCase().includes(normalized) ||
        (item.pluginId || '').toLowerCase().includes(normalized) ||
        (item.description || '').toLowerCase().includes(normalized)
      ))
    : catalogEntries;
  if (!filtered.length) {
    list.append(el('div', { class: 'plugin-catalog-empty', text: normalized ? 'No plugins match your search.' : 'No plugins in the catalog yet.' }));
    return;
  }
  for (const item of filtered) {
    const installedPlugin = servers.find((server) => server.plugin === true && server.pluginId === item.pluginId);
    const isInstalled = installed.has(item.pluginId);
    const installedVersion = installedPlugin?.version ?? '';
    const hasUpdate = isInstalled && installedVersion && installedVersion !== item.version && newerVersion(item.version, installedVersion);
    const iconValue = item.icon || '🔌';
    const actionText = !isInstalled ? 'Install' : (hasUpdate ? 'Update' : (installedVersion === item.version ? 'Installed' : 'Reinstall'));
    const actionDisabled = !isInstalled ? false : (actionText === 'Installed');
    const row = el('div', { class: 'plugin-catalog-entry' },
      el('div', { class: 'plugin-catalog-icon' }, iconNode(iconValue, iconValue)),
      el('div', { class: 'plugin-catalog-info' },
        el('div', { class: 'plugin-catalog-name' },
          el('span', { text: item.name }),
          el('span', { class: 'plugin-catalog-version', text: `v${item.version}` }),
        ),
        el('div', { class: 'plugin-catalog-desc', text: item.description || item.pluginId }),
      ),
      el('button', {
        class: `mini-btn plugin-catalog-action${hasUpdate ? ' has-update' : ''}`,
        type: 'button',
        text: actionText,
        disabled: actionDisabled ? true : undefined,
        'aria-label': `${actionText} ${item.name}`,
      }),
    );
    row.querySelector('.plugin-catalog-action').addEventListener('click', () => {
      if (hasUpdate) updatePlugin(item);
      else installFromCatalog(item);
    });
    list.append(row);
  }
}

async function confirmInstall() {
  if (installTab === 'catalog') {
    toast('Choose a plugin from the catalog list', 'info');
    return;
  }
  if (installTab === 'github') {
    const url = document.getElementById('plugin-install-github-url').value.trim();
    if (!url) {
      toast('GitHub URL is required', 'error');
      return;
    }
    const subdir = document.getElementById('plugin-install-github-subdir').value.trim();
    const ref = document.getElementById('plugin-install-github-ref').value.trim();
    try {
      await runInstall({ source: 'github', url, subdir, ref });
      closeInstallDialog();
    } catch (error) {
      showInstallError(error.message);
    }
    return;
  }
  if (installTab === 'zip') {
    const file = document.getElementById('plugin-install-zip-file').files[0];
    if (!file) {
      toast('Select a .zip file first', 'error');
      return;
    }
    const reader = new FileReader();
    reader.onload = async (e) => {
      const base64 = e.target.result.split(',')[1];
      try {
        await runInstall({ source: 'zip', data: base64 });
        closeInstallDialog();
      } catch (error) {
        showInstallError(error.message);
      }
    };
    reader.readAsDataURL(file);
  }
}

// Simple semver compare ("major.minor.patch"); falls back to string compare.
function cmpVersions(a, b) {
  const pa = (a || '').split(/[-+]/)[0].split('.').map((n) => Number(n));
  const pb = (b || '').split(/[-+]/)[0].split('.').map((n) => Number(n));
  for (let i = 0; i < 3; i++) {
    const x = pa[i] ?? 0, y = pb[i] ?? 0;
    if (x !== y) return x > y ? 1 : -1;
  }
  return 0;
}
function newerVersion(cand, base) {
  return cmpVersions(cand, base) > 0;
}

async function updatePlugin(item) {
  try {
    await runInstall({ source: 'catalog', id: item.id });
    closeInstallDialog();
    toast(`${item.name} updated to v${item.version}`, 'success');
  } catch (error) {
    showInstallError(error.message);
  }
}

async function installFromCatalog(item) {
  try {
    await runInstall({ source: 'catalog', id: item.id });
    closeInstallDialog();
  } catch (error) {
    showInstallError(error.message);
  }
}

async function runInstall(payload) {
  await rpc('plugin.install', payload, { timeoutMs: 300000 });
  toast('Plugin installed successfully', 'success');
  await refresh();
}
