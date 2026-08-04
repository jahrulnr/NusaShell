import type { ReasoningEffort } from "@nusashell/application";
import { resolveModelContextDefaults } from "@nusashell/application";

export interface ModelCapabilities {
  readonly contextWindow?: number;
  readonly maxOutput?: number;
  readonly inputModes?: readonly string[];
  readonly outputModes?: readonly string[];
  readonly supportedEfforts?: readonly ReasoningEffort[];
  readonly defaultEffort?: ReasoningEffort;
  readonly reasoningSupported?: boolean;
  readonly reasoningMandatory?: boolean;
  readonly reasoningSupportsMaxTokens?: boolean;
  readonly supportsTools?: boolean;
  readonly supportsVision?: boolean;
}

export interface ModelRuntimePolicy {
  readonly effort?: Exclude<ReasoningEffort, "auto">;
  readonly contextWindow?: number;
  readonly maxOutput?: number;
  readonly supportsTools: boolean;
  readonly supportsVision: boolean;
}

const effortOrder: readonly Exclude<ReasoningEffort, "auto">[] = [
  "none",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];

export function resolveModelRuntimePolicy(input: {
  readonly model: string;
  readonly requestedEffort?: ReasoningEffort;
  readonly capabilities?: ModelCapabilities;
}): ModelRuntimePolicy {
  const capabilities = input.capabilities;
  // Catalog often stores reasoningSupported:false when the provider listing
  // omitted reasoning fields. Known reasoner families still take the heuristic
  // so effort/thinking params are not silently dropped (e.g. glm-5.2, kimi).
  const supportsReasoning =
    capabilities?.reasoningSupported === true
    || Boolean(capabilities?.reasoningMandatory)
    || Boolean(capabilities?.supportedEfforts?.length)
    || heuristicModelSupportsEffort(input.model);
  const effort = resolveEffort(
    input.requestedEffort ?? "auto",
    supportsReasoning,
    capabilities,
  );
  const inputModes = capabilities?.inputModes?.map((mode) => mode.toLowerCase()) ?? [];
  const supportsVision = capabilities?.supportsVision !== undefined
    ? capabilities.supportsVision
    : inputModes.length > 0
      ? inputModes.includes("image")
      : heuristicModelSupportsVision(input.model);

  const defaults = resolveModelContextDefaults(input.model);
  return {
    ...(effort ? { effort } : {}),
    ...(positiveInteger(capabilities?.contextWindow)
      ? { contextWindow: capabilities?.contextWindow }
      : { contextWindow: defaults.contextWindow }),
    ...(positiveInteger(capabilities?.maxOutput)
      ? { maxOutput: capabilities?.maxOutput }
      : { maxOutput: defaults.maxOutput }),
    supportsTools: capabilities?.supportsTools !== false,
    supportsVision,
  };
}

function resolveEffort(
  requested: ReasoningEffort,
  supported: boolean,
  capabilities: ModelCapabilities | undefined,
): Exclude<ReasoningEffort, "auto"> | undefined {
  if (!supported) return undefined;
  let wanted: Exclude<ReasoningEffort, "auto"> | undefined;
  if (requested === "auto") {
    const defaultEffort = capabilities?.defaultEffort;
    if (defaultEffort === "none" || defaultEffort === "auto") return defaultEffort === "none" ? undefined : "medium";
    wanted = defaultEffort ?? "medium";
  } else {
    wanted = requested;
  }

  const advertised = (capabilities?.supportedEfforts ?? [])
    .filter((effort): effort is Exclude<ReasoningEffort, "auto"> => effort !== "auto");
  if (wanted === "none" && capabilities?.reasoningMandatory) {
    const fallback = advertised.find((effort) => effort !== "none");
    return fallback ?? (capabilities.defaultEffort !== "none" && capabilities.defaultEffort !== "auto"
      ? capabilities.defaultEffort
      : undefined);
  }
  if (advertised.length === 0 || advertised.includes(wanted)) return wanted;

  const target = effortOrder.indexOf(wanted);
  return [...advertised].sort((left, right) => {
    const leftDistance = Math.abs(effortOrder.indexOf(left) - target);
    const rightDistance = Math.abs(effortOrder.indexOf(right) - target);
    return leftDistance - rightDistance;
  })[0];
}

export function heuristicModelSupportsEffort(modelId: string): boolean {
  const model = modelId.trim().toLowerCase();
  return [
    "o1", "o3", "o4-mini", "o4-", "gpt-5", "gpt5", "reasoning", "thinking",
    "deepseek-r1", "deepseek-reasoner", "claude-3-7", "claude-4",
    "claude-opus-4", "claude-sonnet-4", "gemini-2.5", "gemini-3",
    "grok-3", "grok-4", "qwq", "qwen3",
    "glm-5", "glm-4.7", "glm-4.6", "glm-4.5", "kimi", "moonshot",
  ].some((marker) => model.includes(marker));
}

export function heuristicModelSupportsVision(modelId: string): boolean {
  const model = modelId.trim().toLowerCase();
  return [
    "mimo", "omni", "vision", "-vl", "vl-", "pixtral", "llava",
    "gpt-4o", "gpt-4.1", "gpt-4-turbo", "gpt-4-vision", "gpt-5",
    "claude-3", "claude-4", "claude-sonnet", "claude-opus", "claude-haiku",
    "gemini", "qwen2-vl", "qwen-vl", "qwen2.5-vl", "qwen3-vl",
  ].some((marker) => model.includes(marker));
}

function positiveInteger(value: number | undefined): value is number {
  return Number.isInteger(value) && (value ?? 0) > 0;
}
