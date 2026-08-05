/**
 * Pure token-OR scorer for `tool_search`.
 *
 * Whole-string `.includes(query)` silently returns zero hits for multi-keyword
 * queries like "read file list directory terminal" because the full phrase
 * almost never appears as one contiguous substring. Token-OR matches when
 * **any** whitespace-separated token hits the tool name or description.
 *
 * Scoring: +3 per token match in **name**, +1 per token match in
 * **description**. A tool must hit at least one token to be included. Sorted
 * by score desc, then name asc. Capped at 20 by the caller.
 */

export type ToolDiscoveryHit = {
  readonly name: string;
  readonly description?: string;
};

export type ScoredTool = ToolDiscoveryHit & {
  readonly score: number;
};

/** Cap on returned matches (preserves the prior 20-tool limit). */
export const TOOL_SEARCH_MAX_MATCHES = 20;

/** Fixed English hint for zero-hit results (stable for tests). */
export const TOOL_SEARCH_ZERO_HIT_HINT =
  "No tools matched. Try a shorter one-keyword query, call `tool_list` on this plugin, or use `mcp_list` if the plugin may be wrong or not running. Empty matches are success (ok), not a turn interrupt.";

/**
 * Tokenize a query: lowercase, split on whitespace, drop empty tokens.
 * Returns `[]` for empty/whitespace-only queries.
 */
export function tokenizeQuery(query: string): readonly string[] {
  return query.trim().toLowerCase().split(/\s+/).filter((t) => t.length > 0);
}

/**
 * Score a single tool against the query tokens.
 * Returns 0 if no token hits (tool should be excluded).
 */
export function scoreTool(tool: ToolDiscoveryHit, tokens: readonly string[]): number {
  if (tokens.length === 0) return 0;
  const name = tool.name.toLowerCase();
  const description = (tool.description ?? "").toLowerCase();
  let score = 0;
  for (const token of tokens) {
    if (name.includes(token)) score += 3;
    if (description.includes(token)) score += 1;
  }
  return score;
}

/**
 * Rank tools by token-OR score. Excludes zero-score tools. Sorted by
 * score desc, then name asc. Does **not** cap — caller slices to
 * `TOOL_SEARCH_MAX_MATCHES`.
 */
export function rankToolsByTokens(
  tools: readonly ToolDiscoveryHit[],
  tokens: readonly string[],
): readonly ScoredTool[] {
  return tools
    .map((tool) => ({ ...tool, score: scoreTool(tool, tokens) }))
    .filter((tool) => tool.score > 0)
    .sort((a, b) => (b.score - a.score) || a.name.localeCompare(b.name));
}
