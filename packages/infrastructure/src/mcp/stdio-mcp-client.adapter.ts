import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import type { McpClientPort, ToolDescriptor } from "@nusashell/application";

export class StdioMcpClient implements McpClientPort {
  private client: Client | null = null;
  private transport: StdioClientTransport | null = null;

  constructor(
    private readonly command: string,
    private readonly args: readonly string[],
    private readonly env: Readonly<Record<string, string>>,
  ) {}

  async connect(): Promise<void> {
    this.transport = new StdioClientTransport({
      command: this.command,
      args: [...this.args],
      env: { ...process.env, ...this.env } as Record<string, string>,
    });

    this.client = new Client(
      { name: "nusashell-backend", version: "0.0.2" },
      { capabilities: {} },
    );

    await this.client.connect(this.transport);
  }

  async close(): Promise<void> {
    if (this.client) {
      await this.client.close();
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

    return result.content;
  }
}
