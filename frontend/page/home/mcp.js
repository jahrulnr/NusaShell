import { hasPluginUI } from '../../js/views/plugins-model.js';

const IMAGE_ICON_RE = /^(?:data:image\/|https?:\/\/)/i;
const FILE_ICON_PREFIX = /^file:\/\//i;
const FALLBACK_ICON = '🧩';

export function homePluginManifest(plugin) {
  return plugin?.manifest && typeof plugin.manifest === 'object' ? plugin.manifest : (plugin || {});
}

export function homePluginID(plugin) {
  return String(plugin?.id || plugin?.pluginId || '').trim();
}

export function homePluginName(plugin) {
  const manifest = homePluginManifest(plugin);
  return String(plugin?.name || manifest.name || homePluginID(plugin) || 'Unnamed MCP').trim();
}

export function homePluginDescription(plugin) {
  const manifest = homePluginManifest(plugin);
  return String(plugin?.description || manifest.description || '').trim();
}

// Search covers every declared tool name — the launcher card does not render
// tool chips, so there is no display cap to preserve here.
export function homePluginTools(plugin) {
  const tools = Array.isArray(plugin?.tools) ? plugin.tools : [];
  return tools.map((tool) => {
    if (typeof tool === 'string') return tool;
    return tool?.name || tool?.id || '';
  }).filter(Boolean);
}

export function homeMCPAppsWithUI(items) {
  return (Array.isArray(items) ? items : []).filter(hasPluginUI);
}

export function filterHomeMCPApps(items, query) {
  const normalized = String(query || '').trim().toLocaleLowerCase();
  if (!normalized) return items;
  return items.filter((plugin) => [
    homePluginName(plugin),
    homePluginID(plugin),
    plugin?.category,
    homePluginDescription(plugin),
    ...homePluginTools(plugin),
  ].filter(Boolean).join(' ').toLocaleLowerCase().includes(normalized));
}

export function homePluginCategory(plugin) {
  const manifest = homePluginManifest(plugin);
  return String(plugin?.category || manifest.category || '').trim();
}

// Category tabs mirror production renderLauncherTabs
// (frontend/js/views/home.js): 'All' first, then each distinct category in
// first-seen order with the manifest fallback labelled 'Uncategorized'. The
// shell owns the tab strip markup and the selected value.
export function homeMCPCategories(items) {
  const categories = new Set(
    (Array.isArray(items) ? items : []).map((plugin) => homePluginCategory(plugin) || 'Uncategorized'),
  );
  return ['All', ...categories];
}

export function filterHomeMCPAppsByCategory(items, category) {
  const normalized = String(category || 'All').trim();
  if (!normalized || normalized === 'All') return Array.isArray(items) ? items : [];
  return (Array.isArray(items) ? items : []).filter(
    (plugin) => (homePluginCategory(plugin) || 'Uncategorized') === normalized,
  );
}

export function homeMCPEmptyCopy(total, query) {
  const normalized = String(query || '').trim();
  if (normalized) {
    return {
      title: `No MCP apps match “${normalized}”`,
      detail: 'Try a different name, tool, or category.',
    };
  }
  if (Number(total) > 0) {
    return {
      title: 'No MCP apps found',
      detail: 'Clear the search to see all MCP apps.',
    };
  }
  return {
    title: 'No MCP apps available',
    detail: 'Install an MCP app with a UI to see it here.',
  };
}

// Opening a plugin UI is the user's explicit connection intent. Keep that
// optimistic state in the local launcher snapshot without touching unrelated
// entries; a later plugin.list response can still reconcile the authoritative
// MCP runtime status.
export function markHomeMCPConnected(items, id) {
  const source = Array.isArray(items) ? items : [];
  const targetID = String(id || '').trim();
  if (!targetID) return source.slice();

  return source.map((plugin) => (
    homePluginID(plugin) === targetID
      ? { ...plugin, status: 'connected' }
      : plugin
  ));
}

// The backend normally resolves local manifest icons to data URLs. Keep the
// raw file:// rule here as well for compatible MCP payloads that still carry
// the manifest value: a relative file is served through the local-file proxy
// from that plugin's install directory, never as a browser file:// request.
export function homePluginIconValue(plugin) {
  const manifest = homePluginManifest(plugin);
  return String(plugin?.icon || manifest.icon || '').trim();
}

export function homePluginIconSource(plugin) {
  const icon = homePluginIconValue(plugin);
  if (!icon) return null;
  if (IMAGE_ICON_RE.test(icon)) return icon;
  if (!FILE_ICON_PREFIX.test(icon)) return null;

  const rawPath = icon.slice('file://'.length);
  if (!rawPath || rawPath.startsWith('/')) return null;
  let relativePath;
  try {
    relativePath = decodeURIComponent(rawPath);
  } catch {
    relativePath = rawPath;
  }
  if (relativePath.startsWith('/')) return null;
  const segments = relativePath.split(/[\\/]+/).filter(Boolean);
  if (!segments.length || segments.some((segment) => segment === '.' || segment === '..')) return null;

  const installPath = String(plugin?.installPath || '').trim().replace(/[\\/]+$/, '');
  if (!installPath) return null;
  const absolutePath = `${installPath}/${segments.join('/')}`;
  return `/local-file?path=${encodeURIComponent(absolutePath)}`;
}

export function homePluginIconGlyph(plugin) {
  const icon = homePluginIconValue(plugin);
  return icon && !IMAGE_ICON_RE.test(icon) && !FILE_ICON_PREFIX.test(icon) ? icon : FALLBACK_ICON;
}
