import type { McpRawResult, McpIngestedResult, McpContentPart } from "@nusashell/application";

/**
 * Legacy unwrap: throws on `isError`, returns `structuredContent` or `content`.
 * Kept for client adapters that still throw on protocol errors. New code should
 * call {@link ingestMcpToolResult} from `@nusashell/application` instead.
 */
export function unwrapMcpToolResult(result: unknown): unknown {
  if (typeof result !== "object" || result === null) return result;
  const toolResult = result as {
    readonly content?: unknown;
    readonly isError?: boolean;
    readonly structuredContent?: unknown;
  };
  if (toolResult.isError) {
    throw new Error(toolErrorMessage(toolResult.content));
  }
  if (toolResult.structuredContent !== undefined) {
    return toolResult.structuredContent;
  }
  return toolResult.content;
}

/**
 * Ingest an MCP `CallToolResult` preserving `content`, `structuredContent`,
 * and `isError` without throwing. Execution errors (`isError: true`) become
 * `{ kind: "error" }` so the gateway can build a model-recoverable
 * `AgentToolResult` instead of throwing.
 *
 * Transport/protocol failures (RPC disconnect, timeout) still throw at the
 * client adapter — this function only handles the MCP result envelope.
 */
export function ingestMcpToolResultFromWire(raw: unknown): McpIngestedResult {
  if (typeof raw !== "object" || raw === null) {
    return { kind: "ok", content: [] };
  }
  const result = raw as McpRawResult;
  const content = Array.isArray(result.content)
    ? result.content.filter((p): p is McpContentPart => p != null && typeof p === "object")
    : [];
  if (result.isError) {
    return {
      kind: "error",
      message: toolErrorMessage(result.content),
      content,
      ...(result.structuredContent !== undefined ? { structuredContent: result.structuredContent } : {}),
    };
  }
  return {
    kind: "ok",
    content,
    ...(result.structuredContent !== undefined ? { structuredContent: result.structuredContent } : {}),
  };
}

function toolErrorMessage(content: unknown): string {
  if (!Array.isArray(content)) return "MCP tool call failed";
  const messages = content
    .filter((item): item is { readonly text: string } =>
      typeof item === "object" &&
      item !== null &&
      "text" in item &&
      typeof item.text === "string")
    .map((item) => item.text.trim())
    .filter(Boolean);
  return messages.join("\n") || "MCP tool call failed";
}
