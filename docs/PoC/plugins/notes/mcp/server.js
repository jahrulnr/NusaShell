#!/usr/bin/env node
/**
 * Dummy MCP server for the "Notes" plugin.
 * Protocol: JSON-RPC-style, line-delimited, over stdin/stdout.
 * Supported methods: initialize, tools/list, tools/call
 * (shaped like real MCP so swapping to the official MCP SDK later
 *  does not require changing the shell).
 *
 * Note: the original PoC environment blocked npm registry access, so this
 * implements the protocol by hand (not @modelcontextprotocol/sdk).
 * Concepts and message shapes still match real MCP.
 */

const readline = require("readline");

// in-memory notes store - resets whenever this process is respawned
let notes = [];
let nextId = 1;

const TOOLS = [
  {
    name: "createNote",
    description: "Create a new note",
    inputSchema: { text: "string" }
  },
  {
    name: "listNotes",
    description: "List all stored notes",
    inputSchema: {}
  }
];

function send(msg) {
  process.stdout.write(JSON.stringify(msg) + "\n");
}

function handleToolCall(name, args) {
  switch (name) {
    case "createNote": {
      const note = { id: nextId++, text: args.text, createdAt: new Date().toISOString() };
      notes.push(note);
      return { note, totalNotes: notes.length };
    }
    case "listNotes": {
      return { notes };
    }
    default:
      throw new Error(`Unknown tool: ${name}`);
  }
}

const rl = readline.createInterface({ input: process.stdin, terminal: false });

rl.on("line", (line) => {
  if (!line.trim()) return;
  let req;
  try {
    req = JSON.parse(line);
  } catch (e) {
    return send({ error: "invalid_json" });
  }

  const { id, method, params } = req;

  try {
    if (method === "initialize") {
      send({ id, result: { serverName: "notes-mcp", version: "1.0.0" } });
    } else if (method === "tools/list") {
      send({ id, result: { tools: TOOLS } });
    } else if (method === "tools/call") {
      const result = handleToolCall(params.name, params.args || {});
      send({ id, result });
    } else {
      send({ id, error: `unknown_method:${method}` });
    }
  } catch (err) {
    send({ id, error: err.message });
  }
});

// Tell the parent (shell) this process is alive
send({ type: "ready" });
