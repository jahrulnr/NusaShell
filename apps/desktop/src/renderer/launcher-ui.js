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

export function applyTextEdit(selection, action, clipboardText = "") {
  const value = String(selection.value ?? "");
  const start = Math.max(0, Math.min(value.length, Number(selection.selectionStart) || 0));
  const end = Math.max(start, Math.min(value.length, Number(selection.selectionEnd) || start));
  const selectedText = value.slice(start, end);

  if (action === "copy") {
    return {
      value,
      selectionStart: start,
      selectionEnd: end,
      clipboardText: selectedText,
    };
  }

  const replacement = action === "paste" ? String(clipboardText) : "";
  const nextValue = `${value.slice(0, start)}${replacement}${value.slice(end)}`;
  const nextCaret = start + replacement.length;
  return {
    value: nextValue,
    selectionStart: nextCaret,
    selectionEnd: nextCaret,
    clipboardText: action === "cut" ? selectedText : "",
  };
}

export function countLogsBySource(entries) {
  return entries.reduce((counts, entry) => {
    counts.all += 1;
    counts[entry.source] = (counts[entry.source] ?? 0) + 1;
    return counts;
  }, { all: 0 });
}
