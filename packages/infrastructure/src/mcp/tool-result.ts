export function unwrapMcpToolResult(result: unknown): unknown {
  if (typeof result !== "object" || result === null) return result;
  const toolResult = result as {
    readonly content?: unknown;
    readonly isError?: boolean;
  };
  if (toolResult.isError) {
    throw new Error(toolErrorMessage(toolResult.content));
  }
  return toolResult.content;
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
