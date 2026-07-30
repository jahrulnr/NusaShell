#!/usr/bin/env node
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { loadRootFromEnvironment } from "./config.js";
import { safeFilesError } from "./errors.js";
import { FileService } from "./fs-service.js";
import { callFilesTool, FILES_TOOLS } from "./tools.js";

async function main() {
  const root = await loadRootFromEnvironment();
  const service = new FileService(root);
  const server = new Server(
    { name: "nusashell-files", version: "0.1.0" },
    { capabilities: { tools: {} } },
  );

  server.setRequestHandler(ListToolsRequestSchema, async () => ({
    tools: FILES_TOOLS,
  }));

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    try {
      const result = await callFilesTool(
        service,
        request.params.name,
        request.params.arguments ?? {},
      );
      return {
        content: [{ type: "text", text: JSON.stringify(result) }],
        structuredContent: result,
      };
    } catch (error) {
      const safeError = safeFilesError(error);
      process.stderr.write(
        `[nusashell-files] tool failed name=${request.params.name} error=${safeError}\n`,
      );
      return {
        isError: true,
        content: [{
          type: "text",
          text: safeError,
        }],
      };
    }
  });

  async function shutdown() {
    await server.close();
  }

  process.once("SIGINT", () => void shutdown());
  process.once("SIGTERM", () => void shutdown());

  const transport = new StdioServerTransport();
  await server.connect(transport);
  process.stderr.write(`[nusashell-files] root=${root}\n`);
}

void main().catch((error) => {
  process.stderr.write(`[nusashell-files] ${safeFilesError(error)}\n`);
  process.exitCode = 1;
});
