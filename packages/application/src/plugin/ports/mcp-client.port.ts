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
