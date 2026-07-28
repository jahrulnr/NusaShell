export function filterLauncherPlugins(plugins, query) {
  const normalized = String(query ?? "").trim().toLocaleLowerCase();
  if (!normalized) return plugins;
  return plugins.filter((plugin) => `${plugin.name ?? ""} ${plugin.pluginId ?? ""} ${plugin.description ?? ""}`.toLocaleLowerCase().includes(normalized));
}

export function positionContextMenu(point, menuSize, viewportSize) {
  const margin = 8;
  return {
    x: Math.min(Math.max(margin, point.x), Math.max(margin, viewportSize.width - menuSize.width - margin)),
    y: Math.min(Math.max(margin, point.y), Math.max(margin, viewportSize.height - menuSize.height - margin)),
  };
}
