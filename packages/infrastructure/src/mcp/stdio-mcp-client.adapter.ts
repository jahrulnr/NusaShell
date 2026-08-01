import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { ListRootsRequestSchema } from "@modelcontextprotocol/sdk/types.js";
import type {
  CompletionReference,
  CompletionResult,
  McpClientPort,
  PromptDescriptor,
  PromptResult,
  ResourceDescriptor,
  ResourceReadResult,
  ResourceTemplateDescriptor,
  RootDescriptor,
  ToolDescriptor,
} from "@nusashell/application";
import type { Logger } from "pino";
import { redactMcpText, registerMcpLogging } from "./mcp-logging.js";
import { unwrapMcpToolResult } from "./tool-result.js";

export interface StdioRuntime {
  readonly execPath: string;
  readonly electronVersion?: string;
}

export interface StdioLaunch {
  readonly command: string;
  readonly env: Readonly<Record<string, string>>;
}

export function resolveStdioLaunch(
  command: string,
  env: Readonly<Record<string, string>>,
  runtime: StdioRuntime,
): StdioLaunch {
  if (command !== "node" || !runtime.electronVersion) {
    return { command, env };
  }

  return {
    command: runtime.execPath,
    env: { ...env, ELECTRON_RUN_AS_NODE: "1" },
  };
}

export class StdioMcpClient implements McpClientPort {
  private client: Client | null = null;
  private transport: StdioClientTransport | null = null;
  private closeCallback: (() => void) | null = null;
  private currentRoots: readonly RootDescriptor[] = [];
  private rootsRequestedFlag = false;

  constructor(
    private readonly command: string,
    private readonly args: readonly string[],
    private readonly env: Readonly<Record<string, string>>,
    private readonly cwd?: string,
    private readonly logger?: Logger,
  ) {}

  get pid(): number | null {
    return this.transport?.pid ?? null;
  }

  onClose(callback: () => void): void {
    this.closeCallback = callback;
  }

  async connect(): Promise<void> {
    const launch = resolveStdioLaunch(
      this.command,
      { ...process.env, ...this.env } as Record<string, string>,
      {
        execPath: process.execPath,
        ...(process.versions.electron ? { electronVersion: process.versions.electron } : {}),
      },
    );
    this.logger?.debug(
      { command: this.command, executable: launch.command },
      "Connecting stdio MCP client",
    );
    this.transport = new StdioClientTransport({
      command: launch.command,
      args: [...this.args],
      env: { ...launch.env },
      stderr: "pipe",
      ...(this.cwd !== undefined ? { cwd: this.cwd } : {}),
    });

    this.transport.stderr?.on("data", (chunk: Buffer | string) => {
      const message = String(chunk).trim();
      if (message) this.logger?.warn({ command: this.command, message: redactMcpText(message) }, "MCP stderr");
    });

    let closed = false;
    this.transport.onclose = () => {
      closed = true;
      this.logger?.debug({ command: this.command }, "Stdio MCP transport closed");
      if (this.closeCallback) {
        this.closeCallback();
      }
    };

    this.client = new Client(
      { name: "nusashell-backend", version: "0.0.2" },
      { capabilities: { roots: { listChanged: true } } },
    );
    registerMcpLogging(this.client, this.logger, this.command);

    this.client.setRequestHandler(ListRootsRequestSchema, async () => {
      this.rootsRequestedFlag = true;
      return {
        roots: this.currentRoots.map((root) => ({
          uri: root.uri,
          ...(root.name !== undefined ? { name: root.name } : {}),
        })),
      };
    });

    // Race connect against transport close + timeout to avoid hanging
    // when the MCP process exits immediately (e.g. broken deps)
    const CONNECT_TIMEOUT_MS = 10_000;
    await Promise.race([
      this.client.connect(this.transport),
      new Promise<never>((_, reject) => {
        const timer = setTimeout(() => {
          reject(new Error(`MCP connect timed out after ${CONNECT_TIMEOUT_MS}ms`));
        }, CONNECT_TIMEOUT_MS);
        // If transport closes before connect completes, reject immediately
        const checkClose = setInterval(() => {
          if (closed) {
            clearInterval(checkClose);
            clearTimeout(timer);
            reject(new Error("MCP process exited before handshake completed"));
          }
        }, 100);
        // Clean up interval when connect succeeds or times out
        const origClear = clearInterval;
        setTimeout(() => origClear(checkClose), CONNECT_TIMEOUT_MS + 100);
      }),
    ]);
  }

  async close(): Promise<void> {
    this.closeCallback = null;
    if (this.client) {
      try {
        await this.client.close();
      } catch (err) {
        this.logger?.warn({ err }, "Error closing stdio MCP client");
      }
      this.client = null;
    }
    this.transport = null;
  }

  async listTools(): Promise<readonly ToolDescriptor[]> {
    if (!this.client) {
      throw new Error("MCP client not connected");
    }

    const result = await this.client.listTools();
    return result.tools.map((tool) => ({
      name: tool.name,
      description: tool.description ?? "",
      inputSchema: (tool.inputSchema ?? {}) as Readonly<Record<string, unknown>>,
    }));
  }

  async callTool(
    name: string,
    args: Readonly<Record<string, unknown>>,
  ): Promise<unknown> {
    if (!this.client) {
      throw new Error("MCP client not connected");
    }

    const result = await this.client.callTool({
      name,
      arguments: { ...args },
    });

    return unwrapMcpToolResult(result);
  }

  async listPrompts(): Promise<readonly PromptDescriptor[]> {
    const client = this.requireClient();
    const result = await client.listPrompts();
    return result.prompts.map((prompt) => ({
      name: prompt.name,
      ...(prompt.description !== undefined ? { description: prompt.description } : {}),
      ...(prompt.arguments !== undefined ? { arguments: prompt.arguments.map((argument) => ({
        name: argument.name,
        ...(argument.description !== undefined ? { description: argument.description } : {}),
        ...(argument.required !== undefined ? { required: argument.required } : {}),
      })) } : {}),
    }));
  }

  async getPrompt(name: string, args: Readonly<Record<string, string>>): Promise<PromptResult> {
    const result = await this.requireClient().getPrompt({ name, arguments: { ...args } });
    return {
      ...(result.description !== undefined ? { description: result.description } : {}),
      messages: result.messages.map((message) => ({ role: message.role, content: message.content })),
    };
  }

  async listResources(): Promise<readonly ResourceDescriptor[]> {
    const result = await this.requireClient().listResources();
    return result.resources.map((resource) => ({
      uri: resource.uri,
      name: resource.name,
      ...(resource.description !== undefined ? { description: resource.description } : {}),
      ...(resource.mimeType !== undefined ? { mimeType: resource.mimeType } : {}),
      ...(resource.size !== undefined ? { size: resource.size } : {}),
    }));
  }

  async listResourceTemplates(): Promise<readonly ResourceTemplateDescriptor[]> {
    const result = await this.requireClient().listResourceTemplates();
    return result.resourceTemplates.map((template) => ({
      uriTemplate: template.uriTemplate,
      name: template.name,
      ...(template.description !== undefined ? { description: template.description } : {}),
      ...(template.mimeType !== undefined ? { mimeType: template.mimeType } : {}),
    }));
  }

  async readResource(uri: string): Promise<ResourceReadResult> {
    const result = await this.requireClient().readResource({ uri });
    return {
      contents: result.contents.map((content) => ({
        uri: content.uri,
        ...(content.mimeType !== undefined ? { mimeType: content.mimeType } : {}),
        ...("text" in content ? { text: content.text } : { blob: content.blob }),
      })),
    };
  }

  async complete(
    reference: CompletionReference,
    argument: { readonly name: string; readonly value: string },
    context?: { readonly arguments?: Readonly<Record<string, string>> },
  ): Promise<CompletionResult> {
    const result = await this.requireClient().complete({
      ref: reference,
      argument,
      ...(context ? { context: { arguments: { ...context.arguments } } } : {}),
    });
    return {
      values: result.completion.values,
      ...(typeof result.completion.total === "number" ? { total: result.completion.total } : {}),
      ...(typeof result.completion.hasMore === "boolean" ? { hasMore: result.completion.hasMore } : {}),
    };
  }

  setRoots(roots: readonly RootDescriptor[]): void {
    this.currentRoots = roots;
  }

  async notifyRootsChanged(): Promise<void> {
    if (!this.client) return;
    await this.client.sendRootsListChanged();
  }

  rootsRequested(): boolean {
    return this.rootsRequestedFlag;
  }

  private requireClient(): Client {
    if (!this.client) throw new Error("MCP client not connected");
    return this.client;
  }
}
