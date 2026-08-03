import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js";
import type { CompletionReference, CompletionResult, McpClientPort, PromptDescriptor, PromptResult, ResourceDescriptor, ResourceReadResult, ResourceTemplateDescriptor, ToolDescriptor, AutomationClientDeps } from "@nusashell/application";
import type { Logger } from "pino";
import { registerMcpLogging } from "./mcp-logging.js";
import { registerMcpAutomation } from "./mcp-automation.js";
import { unwrapMcpToolResult } from "./tool-result.js";

export class SseMcpClient implements McpClientPort {
  private client: Client | null = null;
  private transport: SSEClientTransport | null = null;
  private closeCallback: (() => void) | null = null;

  constructor(
    private readonly url: string,
    private readonly logger?: Logger,
    private readonly headers?: Readonly<Record<string, string>>,
    private readonly automation?: AutomationClientDeps,
  ) {}

  get pid(): number | null {
    return null;
  }

  onClose(callback: () => void): void {
    this.closeCallback = callback;
  }

  async connect(): Promise<void> {
    this.logger?.debug({ url: this.url }, "Connecting SSE MCP client");
    this.transport = new SSEClientTransport(new URL(this.url), {
      ...(this.headers ? { requestInit: { headers: this.headers } } : {}),
    });

    this.transport.onclose = () => {
      this.logger?.debug({ url: this.url }, "SSE MCP transport closed");
      if (this.closeCallback) {
        this.closeCallback();
      }
    };

    this.client = new Client(
      { name: "nusashell-backend", version: "0.0.2" },
      { capabilities: {} },
    );
    registerMcpLogging(this.client, this.logger, this.url);
    if (this.automation) {
      registerMcpAutomation(this.client, this.automation.pluginId, {
        eventDispatcher: this.automation.eventDispatcher,
        emitRegistry: this.automation.emitRegistry,
        rateLimiter: this.automation.rateLimiter,
        ...(this.logger ? { logger: this.logger } : {}),
      });
    }

    await this.client.connect(this.transport as never);
  }

  async close(): Promise<void> {
    this.closeCallback = null;
    if (this.client) {
      try {
        await this.client.close();
      } catch (err) {
        this.logger?.warn({ err }, "Error closing SSE MCP client");
      }
      this.client = null;
    }
    this.transport = null;
  }

  isConnected(): boolean {
    return this.client !== null;
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
    const result = await this.requireClient().listPrompts();
    return result.prompts.map((prompt) => ({ name: prompt.name, ...(prompt.description !== undefined ? { description: prompt.description } : {}), ...(prompt.arguments !== undefined ? { arguments: prompt.arguments.map((argument) => ({ name: argument.name, ...(argument.description !== undefined ? { description: argument.description } : {}), ...(argument.required !== undefined ? { required: argument.required } : {}) })) } : {}) }));
  }

  async getPrompt(name: string, args: Readonly<Record<string, string>>): Promise<PromptResult> {
    const result = await this.requireClient().getPrompt({ name, arguments: { ...args } });
    return { ...(result.description !== undefined ? { description: result.description } : {}), messages: result.messages.map((message) => ({ role: message.role, content: message.content })) };
  }

  async listResources(): Promise<readonly ResourceDescriptor[]> {
    const result = await this.requireClient().listResources();
    return result.resources.map((resource) => ({ uri: resource.uri, name: resource.name, ...(resource.description !== undefined ? { description: resource.description } : {}), ...(resource.mimeType !== undefined ? { mimeType: resource.mimeType } : {}), ...(resource.size !== undefined ? { size: resource.size } : {}) }));
  }

  async listResourceTemplates(): Promise<readonly ResourceTemplateDescriptor[]> {
    const result = await this.requireClient().listResourceTemplates();
    return result.resourceTemplates.map((template) => ({ uriTemplate: template.uriTemplate, name: template.name, ...(template.description !== undefined ? { description: template.description } : {}), ...(template.mimeType !== undefined ? { mimeType: template.mimeType } : {}) }));
  }

  async readResource(uri: string): Promise<ResourceReadResult> {
    const result = await this.requireClient().readResource({ uri });
    return { contents: result.contents.map((content) => ({ uri: content.uri, ...(content.mimeType !== undefined ? { mimeType: content.mimeType } : {}), ...("text" in content ? { text: content.text } : { blob: content.blob }) })) };
  }

  async complete(reference: CompletionReference, argument: { readonly name: string; readonly value: string }, context?: { readonly arguments?: Readonly<Record<string, string>> }): Promise<CompletionResult> {
    const result = await this.requireClient().complete({ ref: reference, argument, ...(context ? { context: { arguments: { ...context.arguments } } } : {}) });
    return {
      values: result.completion.values,
      ...(typeof result.completion.total === "number" ? { total: result.completion.total } : {}),
      ...(typeof result.completion.hasMore === "boolean" ? { hasMore: result.completion.hasMore } : {}),
    };
  }

  private requireClient(): Client {
    if (!this.client) throw new Error("MCP client not connected");
    return this.client;
  }
}
