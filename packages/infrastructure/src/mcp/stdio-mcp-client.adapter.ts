import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import type { McpClientPort, ToolDescriptor } from "@nusashell/application";
import type { Logger } from "pino";

function redactMcpLog(message: string): string {
  return message
    .replace(/([?&](?:token|password|secret|api[_-]?key|authorization)=)[^&\s]+/gi, "$1[REDACTED]")
    .replace(/((?:token|password|secret|api[_-]?key|authorization)["']?\s*[:=]\s*["']?)[^,\s}"']+/gi, "$1[REDACTED]");
}

export class StdioMcpClient implements McpClientPort {
  private client: Client | null = null;
  private transport: StdioClientTransport | null = null;
  private closeCallback: (() => void) | null = null;

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
    this.logger?.debug({ command: this.command }, "Connecting stdio MCP client");
    this.transport = new StdioClientTransport({
      command: this.command,
      args: [...this.args],
      env: { ...process.env, ...this.env } as Record<string, string>,
      stderr: "pipe",
      ...(this.cwd !== undefined ? { cwd: this.cwd } : {}),
    });

    this.transport.stderr?.on("data", (chunk: Buffer | string) => {
      const message = String(chunk).trim();
      if (message) this.logger?.warn({ command: this.command, message: redactMcpLog(message) }, "MCP stderr");
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
      { capabilities: {} },
    );

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

    return result.content;
  }
}
