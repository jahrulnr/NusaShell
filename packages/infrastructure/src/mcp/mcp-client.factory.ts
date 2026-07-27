import type { McpClientFactoryPort, McpClientPort } from "@nusashell/application";
import { StdioMcpClient } from "./stdio-mcp-client.adapter.js";

export class McpClientFactory implements McpClientFactoryPort {
  createForStdio(
    command: string,
    args: readonly string[],
    env: Readonly<Record<string, string>>,
    cwd?: string,
  ): McpClientPort {
    return new StdioMcpClient(command, args, env, cwd);
  }

  createForHttp(_url: string): McpClientPort {
    throw new Error("HTTP MCP transport not yet implemented");
  }

  createForSse(_url: string): McpClientPort {
    throw new Error("SSE MCP transport not yet implemented");
  }
}
