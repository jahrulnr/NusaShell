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
await server.connect(transport);
