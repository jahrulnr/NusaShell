import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  ListToolsRequestSchema,
  CallToolRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

const server = new Server(
  { name: "mock", version: "0.0.1" },
  { capabilities: { tools: {} } },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "echo",
      description: "Echoes the input",
      inputSchema: {
        type: "object",
        properties: { message: { type: "string" } },
        required: ["message"],
      },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (req) => {
  const args = req.params.arguments;
  if (req.params.name === "echo") {
    return { content: [{ type: "text", text: args.message }] };
  }
  if (req.params.name === "fail") {
    return {
      isError: true,
      content: [{ type: "text", text: "Mailbox authentication failed" }],
    };
  }
  return { content: [{ type: "text", text: "unknown tool" }] };
});


const transport = new StdioServerTransport();

// Test hook: when MCP_MOCK_SPAM_STDERR is set, write a large volume of
// stderr lines BEFORE the handshake completes, forcing the client's
// stderrBuffer to exceed its cap. Useful for verifying the bounded tail.
if (process.env.MCP_MOCK_SPAM_STDERR) {
  const lines = Number(process.env.MCP_MOCK_SPAM_STDERR) || 10_000;
  const chunk = "spam line content padding padding padding\n";
  for (let i = 0; i < lines; i++) {
    process.stderr.write(chunk);
  }
}


if (process.env.MCP_MOCK_DELAY_MS) {
  const ms = Number(process.env.MCP_MOCK_DELAY_MS) || 0;
  if (ms > 0) {
    await new Promise((r) => setTimeout(r, ms));
  }
}

await server.connect(transport);
