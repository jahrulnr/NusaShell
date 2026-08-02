import type { McpClientFactoryPort, McpClientPort } from "@nusashell/application";
import type { Logger } from "pino";
import { StdioMcpClient } from "./stdio-mcp-client.adapter.js";
import { HttpMcpClient } from "./http-mcp-client.adapter.js";
import { SseMcpClient } from "./sse-mcp-client.adapter.js";

export class McpClientFactory implements McpClientFactoryPort {
  constructor(private readonly logger?: Logger) {}

  createForStdio(
    command: string,
    args: readonly string[],
    env: Readonly<Record<string, string>>,
    cwd?: string,
  ): McpClientPort {
    return new StdioMcpClient(command, args, env, cwd, this.logger);
  }

  createForHttp(url: string, headers?: Readonly<Record<string, string>>): McpClientPort {
    return new HttpMcpClient(url, this.logger, headers);
  }

  createForSse(url: string, headers?: Readonly<Record<string, string>>): McpClientPort {
    return new SseMcpClient(url, this.logger, headers);
  }
}
