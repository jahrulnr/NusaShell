// Plugins workspace: one catalog for native MCP servers and installed
// plugins, including plugins that expose a browser UI.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';
import { hasPluginUI, pluginKind, pluginRowMeta } from './plugins-model.js';

export { hasPluginUI, pluginKind, pluginRowMeta } from './plugins-model.js';

let servers = [];
let pluginCatalog = [];
let currentServer = null;
let drawerReturnFocus = null;

const STATES = ['idle', 'connecting', 'connected', 'error'];

export async function initPlugins() {
  document.getElementById('plugins-add-mcp')?.addEventListener('click', () => addMcp());
  wireDrawer();
  await refresh();
}

export async function refresh() {
  const [serverResult, catalogResult] = await Promise.allSettled([
    rpc('mcp.servers.list'),
    rpc('plugin.list'),
  ]);
  if (serverResult.status === 'rejected') throw serverResult.reason;
  servers = serverResult.value.servers ?? [];
  pluginCatalog = catalogResult.status === 'fulfilled' ? catalogResult.value.plugins ?? [] : [];
  const catalogByID = new Map(pluginCatalog.map((plugin) => [plugin.id, plugin]));
  servers = servers.map((server) => {
    if (!server.plugin) return server;
    const plugin = catalogByID.get(server.id.replace(/^plugin:/, ''));
    return plugin ? { ...server, hasUI: server.hasUI || plugin.hasUI, manifest: plugin.manifest } : server;
  });
  renderList();
  if (currentServer) {
    const updated = servers.find((server) => server.id === currentServer.id);
    if (updated) openDrawer(updated);
    else closeDrawer();
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
    const icon = el('div', { class: `plugin-row-icon ${server.plugin ? 'plugin-row-icon-plugin' : ''}`, text: shortIcon(server.icon || server.name) });
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
  document.getElementById('plugin-btn-test')?.addEventListener('click', () => { if (currentServer) void testServer(currentServer); });
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
  document.getElementById('plugin-drawer-icon').textContent = shortIcon(server.icon || server.name);
  document.getElementById('plugin-drawer-title').textContent = server.name;
  document.getElementById('plugin-drawer-subtitle').textContent = pluginRowMeta(server);
  document.getElementById('plugin-btn-open-ui').hidden = !hasPluginUI(server);
  document.getElementById('plugin-btn-edit').hidden = isPlugin;
  document.getElementById('plugin-btn-uninstall').hidden = !isPlugin;
  document.getElementById('plugin-btn-delete').hidden = isPlugin;

  renderDrawerState(server);
  renderDrawerTools(server);
  renderDrawerManifest(server);

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

function renderDrawerTools(server) {
  const list = document.getElementById('plugin-tools-list');
  const count = document.getElementById('plugin-tool-count');
  const tools = server.tools ?? [];
  count.textContent = String(tools.length);
  list.replaceChildren();
  if (!tools.length) {
    list.append(el('div', { class: 'tools-empty', text: 'No tools loaded. Click Test to connect.' }));
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

async function testServer(server) {
  const button = document.getElementById('plugin-btn-test');
  button.disabled = true;
  button.textContent = 'Testing…';
  try {
    const result = await rpc('mcp.servers.test', { id: server.id });
    toast(`Connected · ${result.tools?.length ?? 0} tools`, 'success');
  } catch (error) {
    toast(error.message, 'error');
  } finally {
    button.disabled = false;
    button.textContent = 'Test';
    await refresh();
  }
}

async function stopServer(server) {
  try {
    await rpc('mcp.servers.stop', { id: server.id });
    toast('MCP server stopped', 'success');
    await refresh();
  } catch (error) {
    toast(error.message, 'error');
  }
}

async function restartServer(server) {
  try {
    await rpc('mcp.servers.stop', { id: server.id });
    const result = await rpc('mcp.servers.test', { id: server.id });
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
    await rpc('mcp.servers.delete', { id: server.id });
    toast('MCP server deleted', 'success');
    closeDrawer();
    await refresh();
  } catch (error) {
    toast(error.message, 'error');
  }
}

async function uninstallPlugin(server) {
  const pluginID = server.id.replace(/^plugin:/, '');
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
  const pluginID = server.id.replace(/^plugin:/, '');
  const windowConfig = server.manifest?.ui?.window ?? {};
  const width = windowConfig.defaultSize?.width || 1024;
  const height = windowConfig.defaultSize?.height || 720;
  window.open(`/plugins/${pluginID}/`, pluginID, `width=${width},height=${height},menubar=no,toolbar=no,location=no,status=no,resizable=${windowConfig.resizable === false ? 'no' : 'yes'}`);
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
    await rpc('mcp.servers.save', {
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
