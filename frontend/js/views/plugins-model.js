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

// canAutoUpdate reports whether the auto-update toggle applies to this entry.
// Only catalog-managed plugins have a release channel to follow; manual MCP
// servers and GitHub/ZIP installs do not.
export function canAutoUpdate(server) {
  return server?.catalog === true;
}

// hasUpdate reports whether a newer catalog version is available, driving the
// drawer's manual "Update" button and the row/nav update badges.
export function hasUpdate(server) {
  return Boolean(server?.updateAvailable);
}

// hasContract reports whether a plugin declares an agent-facing usage
// contract (manifest contract.entry), driving the Plugins-row badge and
// the drawer manifest line.
export function hasContract(server) {
  return Boolean(server?.contractEntry);
}
