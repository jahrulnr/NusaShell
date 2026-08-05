/**
 * Pure formatter for the Live MCP runtime snapshot.
 *
 * When at least one MCP plugin is **running**, the snapshot carries the full
 * tool catalog (name + description + `inputSchema`) for every tool on those
 * plugins. The formatter renders an IDE-style block directly after the static
 * `mcp-tools.md` workflow prompt so the model can call provider names directly
 * without progressive discovery.
 *
 * Returns `undefined` when the snapshot is empty (no running plugins, no
 * tools) so no block is injected — matching the contract of
 * `formatMemoryPrompt` / `buildSkillsCatalogPrompt`.
 *
 * Budget: hard cap ~80_000 chars (high ceiling, mostly prompt-cacheable).
 * Overflow tools (beyond the 96-entry provider `tools[]` cap) are listed by
 * name only with a pointer to `tool_list` / `tool_schema`.
 */

export const MCP_LIVE_PROMPT_BUDGET_CHARS = 80_000;

/** Maximum provider `tools[]` entries advertised beyond shell meta-tools. */
export const MCP_LIVE_TOOLS_CAP = 96;

const TRUNCATED_TAIL = "…truncated; use tool_list / tool_schema for the rest";
const HEADER = "## Live MCP (runtime)";
const GUIDANCE = [
  "Prefer these tools; call provider names directly. Do not call mcp_list only to re-list running plugins.",
  "Idle plugins still need mcp_enable then appear here on a later turn.",
  "If a name is missing or args fail: tool_list / tool_schema as needed.",
].join("\n");

export type McpLiveSnapshotTool = {
  /** Provider tool name (`mcp_<plugin>_<tool>`). */
  readonly providerName: string;
  /** Plugin id that owns this tool. */
  readonly pluginId: string;
  /** Raw MCP tool name (without the `mcp_<plugin>_` prefix). */
  readonly toolName: string;
  readonly description?: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
};

export type McpLiveSnapshot = {
  /** Currently running MCP plugin ids (runtime SoT). */
  readonly running: readonly { readonly pluginId: string }[];
  /**
   * Full tool catalog for running plugins (and sticky conversation grants
   * still active this turn). Sorted by `providerName` for prompt-cache
   * stability. Capped at `MCP_LIVE_TOOLS_CAP` entries.
   */
  readonly tools: readonly McpLiveSnapshotTool[];
  /** Tools that exist on running plugins but exceeded the 96-entry cap. */
  readonly toolsOverflow?: readonly string[];
};

/**
 * Format a Live MCP snapshot into a system-prompt block.
 * Returns `undefined` when no plugins are running and no tools are advertised.
 */
export function formatMcpLivePrompt(
  snapshot: McpLiveSnapshot,
  budget: number = MCP_LIVE_PROMPT_BUDGET_CHARS,
): string | undefined {
  const running = uniqueSorted(snapshot.running.map((p) => p.pluginId));
  const tools = [...snapshot.tools].sort((a, b) =>
    a.providerName < b.providerName ? -1 : a.providerName > b.providerName ? 1 : 0,
  );
  const overflow = uniqueSorted(snapshot.toolsOverflow ?? []);

  if (running.length === 0 && tools.length === 0 && overflow.length === 0) {
    return undefined;
  }

  const lines: string[] = [HEADER];
  if (running.length > 0) lines.push(`Running: ${running.join(", ")}`);
  lines.push("");

  for (const tool of tools) {
    lines.push(`### ${tool.providerName}`);
    if (tool.description) {
      lines.push(`description: ${tool.description}`);
    }
    lines.push("inputSchema:");
    lines.push("```json");
    lines.push(JSON.stringify(tool.inputSchema, null, 2));
    lines.push("```");
    lines.push("");
  }

  if (overflow.length > 0) {
    lines.push("Present but not in tools[] (overflow) — call via known name or tool_schema:");
    lines.push(overflow.join(", "));
    lines.push("");
  }

  lines.push(GUIDANCE);

  let out = lines.join("\n");
  if (out.length <= budget) return out;

  // Truncate: keep header + guidance, trim the catalog middle.
  const overhead = HEADER.length + 1 + GUIDANCE.length + 1 + TRUNCATED_TAIL.length;
  const middleBudget = Math.max(0, budget - overhead);
  const headerEnd = HEADER.length + 1;
  const guidanceStart = out.length - GUIDANCE.length;
  const middle = out.slice(headerEnd, guidanceStart).trim();
  const trimmed = middle.slice(0, middleBudget).replace(/```\s*$/, "").trim();
  out = `${HEADER}\n${trimmed}\n${TRUNCATED_TAIL}\n${GUIDANCE}`;
  return out.length > budget
    ? `${out.slice(0, budget - TRUNCATED_TAIL.length)}${TRUNCATED_TAIL}`
    : out;
}

function uniqueSorted(values: readonly string[]): string[] {
  return Array.from(new Set(values)).sort();
}
