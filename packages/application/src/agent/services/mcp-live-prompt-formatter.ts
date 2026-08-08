/** Maximum provider `tools[]` entries advertised beyond shell meta-tools. */
export const MCP_LIVE_TOOLS_CAP = 96;

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
  /** Full checkpoint catalog for running plugins and sticky grants. */
  readonly tools: readonly McpLiveSnapshotTool[];
  /** Tools that exist on running plugins but exceeded the 96-entry cap. */
  readonly toolsOverflow?: readonly string[];
};
