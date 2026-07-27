import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { SSEClientTransport } from "@modelcontextprotocol/sdk/client/sse.js";
import type { McpClientPort, ToolDescriptor } from "@nusashell/application";
import type { Logger } from "pino";

export class SseMcpClient implements McpClientPort {
  private client: Client | null = null;
  private transport: SSEClientTransport | null = null;
  private closeCallback: (() => void) | null = null;

  constructor(
    private readonly url: string,
    private readonly logger?: Logger,
  ) {}

  get pid(): number | null {
    return null;
  }

  onClose(callback: () => void): void {
    this.closeCallback = callback;
  }

  async connect(): Promise<void> {
    this.logger?.debug({ url: this.url }, "Connecting SSE MCP client");
    this.transport = new SSEClientTransport(new URL(this.url));

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

    return result.content;
  }
}
