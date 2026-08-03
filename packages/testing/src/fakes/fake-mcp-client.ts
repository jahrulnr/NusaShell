import type {
  CompletionReference,
  CompletionResult,
  McpClientFactoryPort,
  McpClientPort,
  PromptDescriptor,
  PromptResult,
  ResourceDescriptor,
  ResourceReadResult,
  ResourceTemplateDescriptor,
  RootDescriptor,
  ToolDescriptor,
} from "@nusashell/application";

export class FakeMcpClient implements McpClientPort {
  readonly tools: ToolDescriptor[] = [];
  readonly prompts: PromptDescriptor[] = [];
  readonly resources: ResourceDescriptor[] = [];
  readonly resourceTemplates: ResourceTemplateDescriptor[] = [];
  private connected = false;
  private closeCallback: (() => void) | null = null;
  /** True once onClose() registered a close watcher (test instrumentation). */
  onCloseRegistered = false;
  /** Artificial connect delay (ms); close() aborts an in-flight connect. */
  connectDelayMs = 0;
  private connectAbort: ((error: Error) => void) | null = null;
  readonly callLog: Array<{
    name: string;
    args: Readonly<Record<string, unknown>>;
  }> = [];
  private readonly callResults = new Map<string, unknown>();
  private readonly callDelays = new Map<string, number>();
  private readonly promptResults = new Map<string, PromptResult>();
  private readonly resourceResults = new Map<string, ResourceReadResult>();
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

  setPromptResult(name: string, result: PromptResult): void {
    this.promptResults.set(name, result);
  }

  setResourceResult(uri: string, result: ResourceReadResult): void {
    this.resourceResults.set(uri, result);
  }

  setThrowOnCall(should: boolean): void {
    this.callShouldThrow = should;
  }

  onClose(callback: () => void): void {
    this.closeCallback = callback;
    this.onCloseRegistered = true;
  }

  emitClose(): void {
    if (this.closeCallback) {
      this.closeCallback();
    }
  }

  async connect(): Promise<void> {
    if (this.connectDelayMs > 0) {
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => {
          this.connectAbort = null;
          this.connected = true;
          resolve();
        }, this.connectDelayMs);
        this.connectAbort = (error) => {
          clearTimeout(timer);
          this.connectAbort = null;
          reject(error);
        };
      });
      return;
    }
    this.connected = true;
  }

  async close(): Promise<void> {
    if (this.connectAbort) {
      this.connectAbort(new Error("MCP connect aborted"));
    }
    this.connected = false;
    this.closeCallback = null;
  }

  isConnected(): boolean {
    return this.connected;
  }

  async listTools(): Promise<readonly ToolDescriptor[]> {
    return this.tools;
  }

  async listPrompts(): Promise<readonly PromptDescriptor[]> {
    return this.prompts;
  }

  async getPrompt(
    name: string,
    _args: Readonly<Record<string, string>>,
  ): Promise<PromptResult> {
    return this.promptResults.get(name) ?? { messages: [] };
  }

  async listResources(): Promise<readonly ResourceDescriptor[]> {
    return this.resources;
  }

  async listResourceTemplates(): Promise<readonly ResourceTemplateDescriptor[]> {
    return this.resourceTemplates;
  }

  async readResource(uri: string): Promise<ResourceReadResult> {
    return this.resourceResults.get(uri) ?? { contents: [] };
  }

  async complete(
    _reference: CompletionReference,
    _argument: { readonly name: string; readonly value: string },
    _context?: { readonly arguments?: Readonly<Record<string, string>> },
  ): Promise<CompletionResult> {
    return { values: [] };
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

  /** Roots set via setRoots (test instrumentation). */
  roots: readonly RootDescriptor[] = [];
  /** True once rootsRequested() has been called (simulates a roots-capable server). */
  private rootsRequestedFlag = false;
  /** Roots notifications sent (test instrumentation). */
  readonly rootsNotifications: number[] = [];

  setRoots(roots: readonly RootDescriptor[]): void {
    this.roots = roots;
  }

  async notifyRootsChanged(): Promise<void> {
    this.rootsNotifications.push(Date.now());
  }

  rootsRequested(): boolean {
    return this.rootsRequestedFlag;
  }

  /** Test helper: mark this client as roots-capable (server called roots/list). */
  markRootsRequested(): void {
    this.rootsRequestedFlag = true;
  }
}

export class FakeMcpClientFactory implements McpClientFactoryPort {
  readonly created: FakeMcpClient[] = [];
  /** Applied to the next FakeMcpClient created via createForStdio/Http/Sse. */
  nextConnectDelayMs = 0;
  readonly stdioCalls: Array<{
    readonly command: string;
    readonly args: readonly string[];
    readonly env: Readonly<Record<string, string>>;
    readonly cwd?: string;
  }> = [];

  createForStdio(
    command: string,
    args: readonly string[],
    env: Readonly<Record<string, string>>,
    cwd?: string,
  ): McpClientPort {
    this.stdioCalls.push({
      command,
      args,
      env,
      ...(cwd !== undefined ? { cwd } : {}),
    });
    const client = new FakeMcpClient();
    if (this.nextConnectDelayMs > 0) {
      client.connectDelayMs = this.nextConnectDelayMs;
      this.nextConnectDelayMs = 0;
    }
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
