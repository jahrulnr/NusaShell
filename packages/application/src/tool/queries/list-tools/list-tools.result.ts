export interface ToolItem {
  readonly name: string;
  readonly description?: string;
  readonly inputSchema?: Readonly<Record<string, unknown>>;
}

export interface ListToolsResult {
  readonly tools: readonly ToolItem[];
}
