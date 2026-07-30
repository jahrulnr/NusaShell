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
  if (value >= 1_000) return `${Math.round(value / 1_000)}k`;
  return String(value);
}

export function formatContextUsage(usedTokens, contextWindow) {
  const used = Number.isFinite(usedTokens) && usedTokens > 0 ? usedTokens : 0;
  if (Number.isFinite(contextWindow) && contextWindow > 0) {
    return `${formatTokenCount(used)}/${formatTokenCount(contextWindow)} context`;
  }
  return `${formatTokenCount(used)} ctx`;
}

export function estimateContextTokens(messages = []) {
  return Math.ceil(messages.reduce((total, message) => total + estimateMessageChars(message), 0) / 4);
}

export function estimateTokenChars(value) {
  if (value == null) return 0;
  if (typeof value === "string") return value.length;
  try {
    return JSON.stringify(value).length;
  } catch {
    return String(value).length;
  }
}

function estimateMessageChars(message) {
  if (!message || typeof message !== "object") return 0;
  let chars = 0;
  if (typeof message.content === "string") chars += message.content.length;
  else if (message.content != null) chars += estimateTokenChars(message.content);
  if (typeof message.reasoning === "string") chars += message.reasoning.length;
  if (message.toolCalls) chars += estimateTokenChars(message.toolCalls);
  if (message.steps) chars += estimateTokenChars(message.steps);
  if (message.attachments) chars += estimateTokenChars(message.attachments);
  if (message.role === "tool") {
    chars += estimateTokenChars(message.name);
    chars += estimateTokenChars(message.toolCallId);
  }
  return chars;
}

function normalize(value) {
  return String(value || "").trim().toLowerCase();
}
