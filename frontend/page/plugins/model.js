import {
  canAutoUpdate,
  hasContract,
  hasPluginUI,
  hasUpdate,
  pluginKind,
} from '../../js/views/plugins-model.js';

export { canAutoUpdate, hasContract, hasPluginUI, hasUpdate, pluginKind };

const STATUS_LABELS = Object.freeze({
  connected: 'Connected',
  connecting: 'Connecting',
  error: 'Needs attention',
  idle: 'Idle',
});

export function pluginTypeLabel(plugin) {
  if (!plugin?.plugin) return 'MCP server';
  return hasPluginUI(plugin) ? 'MCP + UI' : 'MCP plugin';
}

export function pluginStatusLabel(status) {
  const normalized = String(status || 'idle').trim().toLowerCase();
  return STATUS_LABELS[normalized] || (normalized ? normalized : 'Idle');
}

export function pluginStatusTone(status) {
  const normalized = String(status || 'idle').trim().toLowerCase();
  return ['connected', 'connecting', 'error', 'idle'].includes(normalized) ? normalized : 'idle';
}

export function pluginTransport(plugin) {
  return String(plugin?.manifest?.mcp?.transport || 'stdio').trim().toLowerCase() || 'stdio';
}

export function pluginToolCount(plugin) {
  return Array.isArray(plugin?.tools) ? plugin.tools.length : 0;
}

export function pluginNeedsAttention(plugin) {
  return pluginStatusTone(plugin?.status) === 'error' || hasUpdate(plugin);
}

export function pluginIconFallback(plugin) {
  const value = String(plugin?.icon || plugin?.name || '🧩').trim();
  return value.slice(0, 2).toUpperCase() || '🧩';
}

export function filterPlugins(plugins = [], query = '', filter = 'all') {
  const normalizedQuery = String(query || '').trim().toLocaleLowerCase();
  const normalizedFilter = String(filter || 'all').trim().toLocaleLowerCase();
  return (Array.isArray(plugins) ? plugins : []).filter((plugin) => {
    const matchesFilter = normalizedFilter === 'all'
      || (normalizedFilter === 'connected' && pluginStatusTone(plugin?.status) === 'connected')
      || (normalizedFilter === 'attention' && pluginNeedsAttention(plugin));
    if (!matchesFilter) return false;
    if (!normalizedQuery) return true;
    return [
      plugin?.id,
      plugin?.name,
      plugin?.category,
      pluginTypeLabel(plugin),
      pluginTransport(plugin),
      plugin?.contractEntry,
      ...(Array.isArray(plugin?.tools) ? plugin.tools.flatMap((tool) => [tool?.name, tool?.description]) : []),
    ].filter(Boolean).join(' ').toLocaleLowerCase().includes(normalizedQuery);
  });
}

export function pluginFilterLabel(filter, count) {
  const label = filter === 'connected' ? 'connected' : filter === 'attention' ? 'needs attention' : 'installed';
  return `${count} ${label}`;
}
