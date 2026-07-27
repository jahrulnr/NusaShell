import type {
  McpClientFactoryPort,
  McpClientPort,
  ToolDescriptor,
} from "@nusashell/application";

export class FakeMcpClient implements McpClientPort {
  readonly tools: ToolDescriptor[] = [];
  private connected = false;
  private closeCallback: (() => void) | null = null;
  readonly callLog: Array<{
    name: string;
    args: Readonly<Record<string, unknown>>;
  }> = [];
  private readonly callResults = new Map<string, unknown>();
  private readonly callDelays = new Map<string, number>();
  private callShouldThrow = false;

  get pid(): number | null {
    return this.connected ? 1234 : null;
  }

  setToolResult(name: string, result: unknown): void {
    this.callResults.set(name, result);
  }

  setToolDelay(name: string, ms: number): void {
    this.callDelays.set(name, ms);
  }

  setThrowOnCall(should: boolean): void {
    this.callShouldThrow = should;
  }

  onClose(callback: () => void): void {
    this.closeCallback = callback;
  }

  emitClose(): void {
    if (this.closeCallback) {
      this.closeCallback();
    }
  }

  async connect(): Promise<void> {
    this.connected = true;
  }

  async close(): Promise<void> {
    this.connected = false;
  }

  isConnected(): boolean {
    return this.connected;
  }

  async listTools(): Promise<readonly ToolDescriptor[]> {
    return this.tools;
  }

  async callTool(
    name: string,
    args: Readonly<Record<string, unknown>>,
  ): Promise<unknown> {
    this.callLog.push({ name, args });
    if (this.callShouldThrow) {
      throw new Error("MCP call failed (fake)");
    }
    const delay = this.callDelays.get(name);
    if (delay) {
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
    return this.callResults.get(name) ?? { ok: true };
  }
}

export class FakeMcpClientFactory implements McpClientFactoryPort {
  readonly created: FakeMcpClient[] = [];

  createForStdio(
    _command: string,
    _args: readonly string[],
    _env: Readonly<Record<string, string>>,
    _cwd?: string,
  ): McpClientPort {
    const client = new FakeMcpClient();
    this.created.push(client);
    return client;
  }

  createForHttp(_url: string): McpClientPort {
    const client = new FakeMcpClient();
    this.created.push(client);
    return client;
  }

  createForSse(_url: string): McpClientPort {
    const client = new FakeMcpClient();
    this.created.push(client);
    return client;
  }
}
