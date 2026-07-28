export function modelCompatibility(model) {
  const inputModes = new Set((model?.inputModes || []).map(normalize));
  const outputModes = new Set((model?.outputModes || []).map(normalize));
  const labels = [];
  if (inputModes.has("image")) labels.push("vision");
  if (inputModes.has("file") || inputModes.has("pdf") || inputModes.has("document")) labels.push("document");
  ["audio", "video"].forEach((mode) => {
    if (inputModes.has(mode) || outputModes.has(mode)) labels.push(mode);
  });
  if (model?.supportsTools) labels.push("tools");
  if ((model?.supportedEfforts || []).length > 0) labels.push("reasoning");
  return [...new Set(labels)];
}

export function searchModels(models, query) {
  const needle = normalize(query);
  if (!needle) return [...(models || [])];
  return (models || []).filter((model) => [
    model.id,
    model.label,
    model.providerName,
    ...modelCompatibility(model),
  ].some((value) => normalize(value).includes(needle)));
}

export function clampModelEffort(model, effort) {
  const wanted = normalize(effort) || "auto";
  if (wanted === "auto") return "auto";
  const supported = model?.supportedEfforts || [];
  if (supported.length === 0) return "auto";
  if (supported.includes(wanted)) return wanted;
  return model.defaultEffort || supported[0] || "auto";
}

export function formatTokenCount(value) {
  if (!Number.isFinite(value) || value <= 0) return "";
  if (value >= 1_000_000) return `${Number((value / 1_000_000).toFixed(1))}M`;
  if (value >= 1_000) return `${Math.round(value / 1_000)}K`;
  return String(value);
}

function normalize(value) {
  return String(value || "").trim().toLowerCase();
}
