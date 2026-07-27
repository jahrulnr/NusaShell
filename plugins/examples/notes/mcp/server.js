#!/usr/bin/env node
const { Server } = require("@modelcontextprotocol/sdk/server/index.js");
const { StdioServerTransport } = require("@modelcontextprotocol/sdk/server/stdio.js");
const {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} = require("@modelcontextprotocol/sdk/types.js");

const notes = [];
let nextId = 1;

const server = new Server(
  { name: "notes-mcp", version: "1.0.0" },
  { capabilities: { tools: {} } },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "createNote",
      description: "Create a new note",
      inputSchema: {
        type: "object",
        properties: {
          text: { type: "string", description: "The note text" },
        },
        required: ["text"],
      },
    },
    {
      name: "listNotes",
      description: "List all stored notes",
      inputSchema: {
        type: "object",
        properties: {},
      },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const { name, arguments: args } = request.params;

  if (name === "createNote") {
    const text = (args && args.text) || "";
    const note = { id: nextId++, text, createdAt: new Date().toISOString() };
    notes.push(note);
    return {
      content: [
        {
          type: "text",
          text: JSON.stringify({ note, totalNotes: notes.length }),
        },
      ],
    };
  }

  if (name === "listNotes") {
    return {
      content: [
        {
          type: "text",
          text: JSON.stringify({ notes }),
        },
      ],
    };
  }

  throw new Error("Unknown tool: " + name);
});

const transport = new StdioServerTransport();
server.connect(transport);
