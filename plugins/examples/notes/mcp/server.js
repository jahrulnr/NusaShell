#!/usr/bin/env node
const { Server } = require("@modelcontextprotocol/sdk/server/index.js");
const { StdioServerTransport } = require("@modelcontextprotocol/sdk/server/stdio.js");
const {
  CallToolRequestSchema,
  GetPromptRequestSchema,
  ListPromptsRequestSchema,
  ListResourcesRequestSchema,
  ListResourceTemplatesRequestSchema,
  ReadResourceRequestSchema,
  ListToolsRequestSchema,
} = require("@modelcontextprotocol/sdk/types.js");

const notes = [];
let nextId = 1;

const server = new Server(
  { name: "notes-mcp", version: "1.0.0" },
  { capabilities: { tools: {}, prompts: {}, resources: {} } },
);

server.setRequestHandler(ListPromptsRequestSchema, async () => ({
  prompts: [
    {
      name: "summarize_notes",
      description: "Ask the assistant to summarize the Notes MCP resource.",
    },
  ],
}));

server.setRequestHandler(GetPromptRequestSchema, async (request) => {
  if (request.params.name !== "summarize_notes") throw new Error("Unknown prompt: " + request.params.name);
  return {
    description: "Summarize the notes currently exposed by this MCP server.",
    messages: [{
      role: "user",
      content: { type: "text", text: "Summarize the attached Notes MCP resource. Call out themes and action items." },
    }],
  };
});

server.setRequestHandler(ListResourcesRequestSchema, async () => ({
  resources: [{
    uri: "notes://all",
    name: "All notes",
    description: "Current notes in this local Notes MCP server.",
    mimeType: "application/json",
  }],
}));

server.setRequestHandler(ListResourceTemplatesRequestSchema, async () => ({
  resourceTemplates: [{
    uriTemplate: "notes://{id}",
    name: "Note by ID",
    description: "One note addressed by its numeric ID.",
    mimeType: "application/json",
  }],
}));

server.setRequestHandler(ReadResourceRequestSchema, async (request) => {
  const uri = request.params.uri;
  if (uri === "notes://all") {
    return { contents: [{ uri, mimeType: "application/json", text: JSON.stringify({ notes }) }] };
  }
  const match = /^notes:\/\/(\d+)$/.exec(uri);
  if (match) {
    const note = notes.find((item) => item.id === Number(match[1]));
    if (!note) throw new Error("Note not found: " + match[1]);
    return { contents: [{ uri, mimeType: "application/json", text: JSON.stringify({ note }) }] };
  }
  throw new Error("Unknown resource: " + uri);
});

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
