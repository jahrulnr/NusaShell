// Pure Plugins catalog helpers kept free of browser and transport imports.

export function hasPluginUI(server) {
  if (!server?.plugin) return false;
  return Boolean(server.hasUI || server.manifest?.ui?.entry?.trim());
}

export function pluginKind(server) {
  if (!server?.plugin) return 'mcp';
  return hasPluginUI(server) ? 'plugin-ui' : 'plugin';
}

export function pluginRowMeta(server, kind = pluginKind(server)) {
  if (kind === 'mcp') return `${server.id} · MCP`;
  const version = server.version ? `v${server.version}` : 'installed';
  return `${server.id.replace(/^plugin:/, '')} · ${version} · ${kind === 'plugin-ui' ? 'MCP + UI' : 'MCP'}`;
}
