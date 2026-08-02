export interface ToolDescriptor {
  readonly name: string;
  readonly description: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
}

export interface PromptArgumentDescriptor {
  readonly name: string;
  readonly description?: string;
  readonly required?: boolean;
}

export interface PromptDescriptor {
  readonly name: string;
  readonly description?: string;
  readonly arguments?: readonly PromptArgumentDescriptor[];
}

export interface PromptResult {
  readonly description?: string;
  readonly messages: readonly {
    readonly role: "user" | "assistant";
    readonly content: Readonly<Record<string, unknown>>;
  }[];
}

export interface ResourceDescriptor {
  readonly uri: string;
  readonly name: string;
  readonly description?: string;
  readonly mimeType?: string;
  readonly size?: number;
}

export interface ResourceTemplateDescriptor {
  readonly uriTemplate: string;
  readonly name: string;
  readonly description?: string;
  readonly mimeType?: string;
}

export interface ResourceReadResult {
  readonly contents: readonly {
    readonly uri: string;
    readonly mimeType?: string;
    readonly text?: string;
    readonly blob?: string;
  }[];
}

export type CompletionReference =
  | { readonly type: "ref/prompt"; readonly name: string }
  | { readonly type: "ref/resource"; readonly uri: string };

export interface CompletionResult {
  readonly values: readonly string[];
  readonly total?: number;
  readonly hasMore?: boolean;
}

export interface RootDescriptor {
  readonly uri: string;
  readonly name?: string;
}

export interface McpClientPort {
  connect(): Promise<void>;
  close(): Promise<void>;
  listTools(): Promise<readonly ToolDescriptor[]>;
  listPrompts(): Promise<readonly PromptDescriptor[]>;
  getPrompt(
    name: string,
    args: Readonly<Record<string, string>>,
  ): Promise<PromptResult>;
  listResources(): Promise<readonly ResourceDescriptor[]>;
  listResourceTemplates(): Promise<readonly ResourceTemplateDescriptor[]>;
  readResource(uri: string): Promise<ResourceReadResult>;
  complete(
    reference: CompletionReference,
    argument: { readonly name: string; readonly value: string },
    context?: { readonly arguments?: Readonly<Record<string, string>> },
  ): Promise<CompletionResult>;
  callTool(
    name: string,
    args: Readonly<Record<string, unknown>>,
  ): Promise<unknown>;
  onClose?: (callback: () => void) => void;
  readonly pid?: number | null;
  /**
   * Update the roots this client reports to a server via `roots/list`. The
   * client must advertise the `roots` capability (with `listChanged`) at
   * handshake so roots-capable servers can query it.
   */
  setRoots?(roots: readonly RootDescriptor[]): Promise<void> | void;
  /**
   * Notify the server that the roots changed (`roots/list_changed`). Only
   * meaningful after the server has called `roots/list` at least once.
   */
  notifyRootsChanged?(): Promise<void> | void;
  /**
   * True once the server has called `roots/list` at least once, i.e. the
   * server is roots-capable and consumes workspace roots. Static servers
   * (which only read env/args at spawn) never call it and stay false.
   */
  rootsRequested?(): boolean;
}

export interface McpClientFactoryPort {
  createForStdio(
    command: string,
    args: readonly string[],
    env: Readonly<Record<string, string>>,
    cwd?: string,
  ): McpClientPort;

  createForHttp(url: string, headers?: Readonly<Record<string, string>>): McpClientPort;

  createForSse(url: string, headers?: Readonly<Record<string, string>>): McpClientPort;
}
