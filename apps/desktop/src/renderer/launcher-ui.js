export function pluginIconPresentation(icon) {
  const value = String(icon ?? "").trim() || "🧩";
  if (/^(?:https?:\/\/|file:\/\/)/i.test(value)) {
    return { kind: "image", source: value };
  }
  return { kind: "text", text: value };
}

/**
 * Whether a plugin exposes a UI surface (a window entry). Headless MCP-only
 * plugins omit `ui` and return false here so the Home grid can keep them off
 * while the Plugins view still lists them.
 */
export function hasPluginUi(plugin) {
  return Boolean(plugin?.ui?.entry?.trim());
}

export function findOpaqueBounds(pixels, width, height, alphaThreshold = 8) {
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1) {
    return null;
  }
  let minX = width;
  let minY = height;
  let maxX = -1;
  let maxY = -1;

  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const alpha = pixels[((y * width) + x) * 4 + 3] ?? 0;
      if (alpha <= alphaThreshold) continue;
      minX = Math.min(minX, x);
      minY = Math.min(minY, y);
      maxX = Math.max(maxX, x);
      maxY = Math.max(maxY, y);
    }
  }

  return maxX < minX || maxY < minY
    ? null
    : {
        x: minX,
        y: minY,
        width: maxX - minX + 1,
        height: maxY - minY + 1,
      };
}

export function normalizeTransparentIcon(image) {
  try {
    const scale = Math.min(1, 512 / Math.max(image.naturalWidth, image.naturalHeight));
    const width = Math.max(1, Math.round(image.naturalWidth * scale));
    const height = Math.max(1, Math.round(image.naturalHeight * scale));
    const source = document.createElement("canvas");
    source.width = width;
    source.height = height;
    const sourceContext = source.getContext("2d", { willReadFrequently: true });
    if (!sourceContext) return;
    sourceContext.drawImage(image, 0, 0, width, height);
    const pixels = sourceContext.getImageData(0, 0, width, height).data;
    const bounds = findOpaqueBounds(pixels, width, height);
    if (!bounds) return;

    const alreadyTight = bounds.width >= width * 0.9 && bounds.height >= height * 0.9;
    if (alreadyTight) return;

    const padding = Math.max(2, Math.ceil(Math.max(bounds.width, bounds.height) * 0.06));
    const side = Math.max(bounds.width, bounds.height) + (padding * 2);
    const output = document.createElement("canvas");
    output.width = side;
    output.height = side;
    const outputContext = output.getContext("2d");
    if (!outputContext) return;
    const x = padding + ((side - (padding * 2) - bounds.width) / 2);
    const y = padding + ((side - (padding * 2) - bounds.height) / 2);
    outputContext.drawImage(
      source,
      bounds.x,
      bounds.y,
      bounds.width,
      bounds.height,
      x,
      y,
      bounds.width,
      bounds.height,
    );
    image.src = output.toDataURL("image/png");
  } catch {
    // Cross-origin images may not permit canvas reads; keep their original source.
  }
}

export function filterLauncherPlugins(plugins, query) {
  const normalized = String(query ?? "").trim().toLocaleLowerCase();
  if (!normalized) return plugins;
  return plugins.filter((plugin) => `${plugin.name ?? ""} ${plugin.pluginId ?? ""} ${plugin.description ?? ""}`.toLocaleLowerCase().includes(normalized));
}

export function launcherGridNeedsRebuild(previousPlugins, nextPlugins) {
  const snapshot = (plugins) => plugins
    .filter(hasPluginUi)
    .map((plugin) => ({
      pluginId: plugin.pluginId ?? "",
      name: plugin.name ?? "",
      description: plugin.description ?? "",
      category: plugin.category || "Uncategorized",
      icon: plugin.icon || "🧩",
      installPath: plugin.installPath ?? "",
      uiEntry: plugin.ui?.entry?.trim() ?? "",
    }));

  return JSON.stringify(snapshot(previousPlugins)) !== JSON.stringify(snapshot(nextPlugins));
}

export function launcherPluginTableNeedsRebuild(previousPlugins, nextPlugins) {
  const snapshot = (plugins) => plugins.map((plugin) => ({
    pluginId: plugin.pluginId ?? "",
    name: plugin.name ?? "",
    version: plugin.version ?? "",
    source: plugin.source ?? "",
    icon: plugin.icon || "🧩",
    installPath: plugin.installPath ?? "",
  }));

  return JSON.stringify(snapshot(previousPlugins)) !== JSON.stringify(snapshot(nextPlugins));
}

export function launcherAutostartListNeedsRebuild(previousPlugins, nextPlugins) {
  const snapshot = (plugins) => plugins.map((plugin) => ({
    pluginId: plugin.pluginId ?? "",
    name: plugin.name ?? "",
    icon: plugin.icon || "🧩",
    installPath: plugin.installPath ?? "",
  }));

  return JSON.stringify(snapshot(previousPlugins)) !== JSON.stringify(snapshot(nextPlugins));
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

export function providerApiModes(providerType) {
  if (providerType === "claude") {
    return [{ value: "messages", label: "Anthropic Messages" }];
  }

  const openAiCompatible = [
    { value: "chat", label: "Chat Completions" },
    { value: "responses", label: "Responses API" },
  ];
  return providerType === "openai-compatible"
    ? [...openAiCompatible, { value: "messages", label: "Anthropic Messages" }]
    : openAiCompatible;
}

/**
 * Maps a `tool.list` outcome to a drawer UI descriptor, distinguishing a
 * genuine empty toolset from a transport/runtime failure (finding 3a).
 *
 * The launcher previously swallowed listTools errors as `{ tools: [] }`,
 * which made the drawer show "No tools available" even when the plugin was
 * running but the listing failed. This helper keeps the caller honest: it
 * surfaces the error so the drawer can show "Tools unavailable: …" instead
 * of a silent Tools=0.
 *
 * @param {{ tools?: unknown[], error?: { message?: string } | null } | null} result
 * @param {{ state?: string } | null} [plugin]
 * @returns {{ status: "ready" | "empty" | "unavailable", count: number, tools: unknown[], message: string }}
 */
export function describeToolsPanel(result, plugin) {
  const tools = Array.isArray(result?.tools) ? result.tools : [];
  const count = tools.length;
  const state = plugin?.state ?? "idle";

  if (result?.error) {
    const reason = result.error.message || "tool.list failed";
    return { status: "unavailable", count, tools, message: `Tools unavailable: ${reason}` };
  }
  if (count > 0) {
    return { status: "ready", count, tools, message: `${count} tool${count === 1 ? "" : "s"}` };
  }
  // No tools and no error: distinguish idle (not started) from running-but-empty.
  if (state === "running") {
    return { status: "empty", count: 0, tools, message: "No tools exposed by this plugin." };
  }
  return { status: "empty", count: 0, tools, message: "No tools available. Start the plugin to discover tools." };
}
