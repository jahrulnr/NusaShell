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
  if (modelEffortOptions(model).length > 0 || model?.reasoningSupported) labels.push("reasoning");
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

/**
 * User-facing label for a reasoning effort level.
 * Internal sentinel stays "auto" (omit on the wire); the UI says "default"
 * so it is not confused with automatic model selection.
 */
export function formatEffortLabel(effort) {
  const level = normalize(effort) || "auto";
  return level === "auto" ? "default" : level;
}

/**
 * Effort levels shown in the agent model picker.
 * Catalog-only: no invented levels when the provider omitted supported_efforts.
 */
export function modelEffortOptions(model) {
  return (model?.supportedEfforts || []).filter((effort) => effort && effort !== "auto");
}

export function clampModelEffort(model, effort) {
  const wanted = normalize(effort) || "auto";
  if (wanted === "auto") return "auto";
  const supported = modelEffortOptions(model);
  // No catalog options → force auto (omit reasoning_effort on the wire).
  if (supported.length === 0) return "auto";
  if (supported.includes(wanted)) return wanted;
  return model.defaultEffort && supported.includes(model.defaultEffort)
    ? model.defaultEffort
    : supported[0] || "auto";
}

/**
 * Resolve the effective model for a room (ticket #38).
 *
 * A conversation can carry an explicit per-conversation model binding
 * (`conversation.model`). When present, the room keeps using that model even
 * if the user later changes the global picker in another room. When absent,
 * the room falls back to the global active model (`activeModelKey`).
 *
 * Effort is always room-scoped when bound. Unbound rooms use "auto" and never
 * inherit the Settings-page global effort, so picking effort in one room cannot
 * leak into another.
 *
 * @param {{ kind?: string, model?: { modelKey?: string, effort?: string } }|null|undefined} conversation
 * @param {readonly { key: string }[]} models - the full imported model catalog
 * @param {string} activeModelKey - the global picker's active model key
 * @returns {{ model: any|null, effort: string, source: "room"|"global", explicit: boolean }|null}
 *   null for ACP conversations or when neither the room nor global has a resolvable model.
 */
export function resolveRoomModel(conversation, models, activeModelKey) {
  if (!conversation || conversation.kind === "acp") return null;
  const roomBinding = conversation.model;
  if (roomBinding && roomBinding.modelKey) {
    const model = (models || []).find((m) => m.key === roomBinding.modelKey) ?? null;
    const rawEffort = roomBinding.effort || "auto";
    return {
      model,
      // Keep effort valid for this model's catalog; empty allow-list → auto.
      effort: clampModelEffort(model, rawEffort),
      source: "room",
      explicit: roomBinding.explicit !== false,
    };
  }
  const globalModel = (models || []).find((m) => m.key === activeModelKey) ?? null;
  return {
    model: globalModel,
    effort: "auto",
    source: "global",
    explicit: false,
  };
}

/**
 * Effective reasoning effort for a room turn. Never falls back to the
 * Settings-page global effort (see resolveRoomModel).
 */
export function resolveRoomEffort(conversation, models, activeModelKey, _globalEffortIgnored) {
  const resolved = resolveRoomModel(conversation, models, activeModelKey);
  return resolved?.effort || "auto";
}

/**
 * Explain whether the picker value is for the current turn or queued for the
 * next turn. A running stream remains bound to its original model.
 */
export function formatModelPickerLabel({ model, effort = "auto", source = "global", isRunning = false, liveModelKey = "" } = {}) {
  const label = model?.id || "Choose model";
  const parts = [label, formatEffortLabel(effort)];
  if (source === "room") parts.push("room");
  if (isRunning && liveModelKey && model?.key && liveModelKey !== model.key) parts.push("next turn");
  return parts.join(" · ");
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

/**
 * Resolve the context-window denominator shown to the user, matching the
 * backend's compaction threshold (`resolveContextThreshold` uses
 * `min(settings.maxInputTokens, effectiveModelWindow)`).
 *
 * A model may advertise a large window (e.g. 1M) while the global context
 * cap bounds it (default 200k). The badge must show the *effective* window
 * so idle and live numbers agree with when compaction actually fires
 * (ticket #41).
 *
 * @param {number} modelWindow - model advertised contextWindow (0/NaN = unknown)
 * @param {number|undefined} globalMaxInputTokens - global context cap
 * @returns {number} effective window denominator (0 when unknown)
 */
export function effectiveContextWindow(modelWindow, globalMaxInputTokens) {
  const windowValue = Number.isFinite(modelWindow) && modelWindow > 0 ? modelWindow : 0;
  if (windowValue === 0) return 0;
  const cap = Number.isFinite(globalMaxInputTokens) && globalMaxInputTokens > 0 ? globalMaxInputTokens : Infinity;
  return Math.min(windowValue, cap);
}

/**
 * Resolve the token count to display in the context badge during a live turn.
 *
 * The badge shows approximate *current prompt window* fill — NOT cumulative
 * billing tokens. `estimatedTokens` from `agent.context` events is the display
 * signal; `inputTokens` on that event is cumulative billing and is intentionally
 * ignored here so multi-round tool turns do not inflate the badge to ~N× the
 * real window.
 *
 * @param {{ estimatedTokens?: number, inputTokens?: number, liveTokens?: number }} input
 * @returns {number} tokens to show (never below already-streamed output)
 */
export function resolveContextBadgeTokens({ estimatedTokens, inputTokens, liveTokens } = {}) {
  // inputTokens is cumulative billing across tool rounds — intentionally ignored
  // for the badge (BH-CTX-01/04). Accepted in the signature so callers can pass
  // the full event payload without a separate strip step.
  void inputTokens;
  const estimated = Number(estimatedTokens) || 0;
  const live = Number(liveTokens) || 0;
  // A late agent.context event must not drop the badge below output already
  // streamed to the user; take the richer of the two estimate-based values.
  return Math.max(estimated, live);
}

/**
 * Decide whether a post-await ACP UI update should still apply.
 *
 * ACP session/config awaits can resolve after the user has already switched to
 * a regular chat. Applying the ACP label/options then would stick the model
 * trigger on `"{model} · ACP"` for a non-ACP conversation. This guard returns
 * true only when the currently active conversation is still ACP and is the same
 * conversation that started the await.
 *
 * @param {{ activeId?: string, activeKind?: string, startedId?: string }} input
 * @returns {boolean}
 */
export function shouldApplyAcpUiUpdate({ activeId, activeKind, startedId } = {}) {
  if (activeKind !== "acp") return false;
  if (!startedId || !activeId) return false;
  return activeId === startedId;
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
  // Durable assistant messages mirror content/reasoning/toolCalls inside
  // `steps`. When steps are present, estimate from steps only to avoid
  // double-counting the same text twice (which inflated the badge ~2x).
  if (Array.isArray(message.steps) && message.steps.length > 0) {
    chars += estimateTokenChars(message.steps);
    if (message.attachments) chars += estimateTokenChars(message.attachments);
    return chars;
  }
  if (typeof message.content === "string") chars += message.content.length;
  else if (message.content != null) chars += estimateTokenChars(message.content);
  if (typeof message.reasoning === "string") chars += message.reasoning.length;
  if (message.toolCalls) chars += estimateTokenChars(message.toolCalls);
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
