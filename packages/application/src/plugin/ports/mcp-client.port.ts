export interface ToolDescriptor {
  readonly name: string;
  readonly description: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
}

export interface McpClientPort {
  connect(): Promise<void>;
  close(): Promise<void>;
  listTools(): Promise<readonly ToolDescriptor[]>;
  callTool(
    name: string,
    args: Readonly<Record<string, unknown>>,
  ): Promise<unknown>;
  onClose?: (callback: () => void) => void;
  readonly pid?: number | null;
}

export interface McpClientFactoryPort {
  createForStdio(
    command: string,
    args: readonly string[],
    env: Readonly<Record<string, string>>,
    cwd?: string,
  ): McpClientPort;

  createForHttp(url: string): McpClientPort;

  createForSse(url: string): McpClientPort;
}
