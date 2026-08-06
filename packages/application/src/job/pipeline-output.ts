/**
 * Bounded output policy for pipeline step runs and cross-step context.
 * Keeps JSON store and prompt context from growing without limit.
 */

export const DEFAULT_PIPELINE_SUMMARY_MAX_CHARS = 4_000;
export const DEFAULT_PIPELINE_OUTPUT_PREVIEW_MAX_CHARS = 2_000;
export const DEFAULT_PIPELINE_CONTEXT_VALUE_MAX_CHARS = 2_000;
export const DEFAULT_PIPELINE_LIST_SUMMARY_MAX_CHARS = 500;

export interface BoundedText {
  readonly text: string;
  readonly truncated: boolean;
}

/** Convert arbitrary step output to a stable string for preview/history. */
export function serializePipelineValue(value: unknown): string {
  if (value === undefined) return "";
  if (value === null) return "null";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") {
    return String(value);
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

export function boundPipelineText(
  value: unknown,
  maxChars: number = DEFAULT_PIPELINE_SUMMARY_MAX_CHARS,
): BoundedText {
  const raw = serializePipelineValue(value);
  if (raw.length <= maxChars) {
    return { text: raw, truncated: false };
  }
  return {
    text: `${raw.slice(0, Math.max(0, maxChars - 1))}…`,
    truncated: true,
  };
}

/**
 * Value for downstream template context: string form of output, size-capped.
 * Prefer summary when already short; otherwise bound full serialization.
 */
export function boundContextValue(
  output: unknown,
  summary: string,
  maxChars: number = DEFAULT_PIPELINE_CONTEXT_VALUE_MAX_CHARS,
): string {
  const preferred =
    typeof output === "string"
      ? output
      : output !== undefined
        ? serializePipelineValue(output)
        : summary;
  return boundPipelineText(preferred, maxChars).text;
}
