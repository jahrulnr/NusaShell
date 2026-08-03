import type { McpClientFactoryPort, McpClientPort, AutomationClientDeps } from "@nusashell/application";
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
    automation?: AutomationClientDeps,
  ): McpClientPort {
    return new StdioMcpClient(command, args, env, cwd, this.logger, automation);
  }

  createForHttp(url: string, headers?: Readonly<Record<string, string>>, automation?: AutomationClientDeps): McpClientPort {
    return new HttpMcpClient(url, this.logger, headers, automation);
  }

  createForSse(url: string, headers?: Readonly<Record<string, string>>, automation?: AutomationClientDeps): McpClientPort {
    return new SseMcpClient(url, this.logger, headers, automation);
  }
}
