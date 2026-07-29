export function modelCompatibility(model) {
  const visionStatus = modelVisionStatus(model);
  const inputModes = new Set((model?.inputModes || []).map(normalize));
  const outputModes = new Set((model?.outputModes || []).map(normalize));
  const labels = [];
  if (visionStatus === "supported") labels.push("vision");
  if (visionStatus === "unsupported") labels.push("no vision");
  if (visionStatus === "unknown") labels.push("vision unknown");
  if (inputModes.has("file") || inputModes.has("pdf") || inputModes.has("document")) labels.push("document");
  ["audio", "video"].forEach((mode) => {
    if (inputModes.has(mode) || outputModes.has(mode)) labels.push(mode);
  });
  if (model?.supportsTools) labels.push("tools");
  if ((model?.supportedEfforts || []).length > 0) labels.push("reasoning");
  return [...new Set(labels)];
}

export function modelVisionStatus(model) {
  if (model?.supportsVision === true) return "supported";
  if (model?.supportsVision === false) return "unsupported";
  const inputModes = (model?.inputModes || []).map(normalize);
  if (inputModes.includes("image")) return "supported";
  return inputModes.length > 0 ? "unsupported" : "unknown";
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
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value >= 1_000_000) return `${Number((value / 1_000_000).toFixed(1))}M`;
  if (value >= 1_000) return `${Math.round(value / 1_000)}K`;
  return String(value);
}

export function formatContextUsage(usedTokens, contextWindow) {
  const used = Number.isFinite(usedTokens) && usedTokens > 0 ? usedTokens : 0;
  if (Number.isFinite(contextWindow) && contextWindow > 0) {
    return `${formatTokenCount(used)}/${formatTokenCount(contextWindow)} context`;
  }
  return `${used} ctx`;
}

export function estimateContextTokens(messages = []) {
  return Math.ceil(messages.reduce((total, message) => total + (typeof message.content === "string" ? message.content.length : JSON.stringify(message.content || "").length), 0) / 4);
}

function normalize(value) {
  return String(value || "").trim().toLowerCase();
}
