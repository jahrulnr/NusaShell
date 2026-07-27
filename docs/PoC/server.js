#!/usr/bin/env node
/**
 * SHELL - host app backend.
 *
 * Responsibilities:
 * 1. Serve launcher UI + plugin UI files
 * 2. Plugin registry (scan plugins/, read each manifest.json)
 * 3. Plugin lifecycle: lazy-spawn MCP child process on first call,
 *    reuse the same process while "running", track state per plugin
 * 4. Bridge: accept tool_call from the UI (HTTP from launcher.js),
 *    forward to the MCP child via stdio JSON-RPC, wait for the response,
 *    return it to the UI.
 *
 * Intentionally pure Node.js (no express/ws) because the original PoC
 * environment blocked npm registry access. HTTP routing and child-process
 * IPC are handwritten.
 */

const http = require("http");
const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");
const { randomUUID } = require("crypto");

const PLUGINS_DIR = path.join(__dirname, "plugins");
const PUBLIC_DIR = path.join(__dirname, "public");
const PORT = 8420;

// ---- Plugin Registry ----
function loadRegistry() {
  const ids = fs.readdirSync(PLUGINS_DIR).filter(f =>
    fs.statSync(path.join(PLUGINS_DIR, f)).isDirectory()
  );
  return ids.map(id => {
    const manifestPath = path.join(PLUGINS_DIR, id, "manifest.json");
    const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
    return manifest;
  });
}

// ---- Plugin Manager: manage MCP child process lifecycle ----
class PluginManager {
  constructor() {
    this.processes = new Map(); // pluginId -> { child, pending: Map(requestId->{resolve,reject}), buffer }
  }

  getState(pluginId) {
    return this.processes.has(pluginId) ? "running" : "idle";
  }

  ensureSpawned(pluginId, manifest) {
    if (this.processes.has(pluginId)) return this.processes.get(pluginId);

    const pluginDir = path.join(PLUGINS_DIR, pluginId);
    const [cmd, ...args] = manifest.mcp.command.split(" ");
    console.log(`[shell] spawning MCP process for plugin "${pluginId}": ${manifest.mcp.command}`);

    const child = spawn(cmd, args, { cwd: pluginDir, stdio: ["pipe", "pipe", "pipe"] });
    const entry = { child, pending: new Map(), buffer: "" };
    this.processes.set(pluginId, entry);

    child.stdout.on("data", (chunk) => {
      entry.buffer += chunk.toString();
      let idx;
      while ((idx = entry.buffer.indexOf("\n")) >= 0) {
        const line = entry.buffer.slice(0, idx);
        entry.buffer = entry.buffer.slice(idx + 1);
        if (!line.trim()) continue;
        let msg;
        try { msg = JSON.parse(line); } catch { continue; }
        if (msg.type === "ready") {
          console.log(`[shell] plugin "${pluginId}" MCP process ready`);
          continue;
        }
        const waiter = entry.pending.get(msg.id);
        if (waiter) {
          entry.pending.delete(msg.id);
          if (msg.error) waiter.reject(new Error(msg.error));
          else waiter.resolve(msg.result);
        }
      }
    });

    child.stderr.on("data", (chunk) => {
      console.error(`[plugin:${pluginId}][stderr] ${chunk.toString().trim()}`);
    });

    child.on("exit", (code) => {
      console.log(`[shell] plugin "${pluginId}" MCP process exited (code ${code})`);
      this.processes.delete(pluginId);
    });

    return entry;
  }

  callTool(pluginId, manifest, toolName, args) {
    const entry = this.ensureSpawned(pluginId, manifest);
    const id = randomUUID();
    return new Promise((resolve, reject) => {
      entry.pending.set(id, { resolve, reject });
      entry.child.stdin.write(JSON.stringify({ id, method: "tools/call", params: { name: toolName, args } }) + "\n");
      setTimeout(() => {
        if (entry.pending.has(id)) {
          entry.pending.delete(id);
          reject(new Error("tool_call_timeout"));
        }
      }, 5000);
    });
  }

  killAll() {
    for (const [id, entry] of this.processes) entry.child.kill();
  }
}

const manager = new PluginManager();

// ---- Static file serving helper ----
const MIME = { ".html": "text/html", ".js": "application/javascript", ".css": "text/css", ".json": "application/json" };

function serveFile(res, filePath) {
  fs.readFile(filePath, (err, data) => {
    if (err) { res.writeHead(404); res.end("Not found"); return; }
    const ext = path.extname(filePath);
    res.writeHead(200, { "Content-Type": MIME[ext] || "application/octet-stream" });
    res.end(data);
  });
}

function readBody(req) {
  return new Promise((resolve) => {
    let body = "";
    req.on("data", chunk => body += chunk);
    req.on("end", () => resolve(body ? JSON.parse(body) : {}));
  });
}

// ---- HTTP server / routing ----
const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://localhost:${PORT}`);

  // GET /api/plugins -> list installed plugins + runtime state
  if (url.pathname === "/api/plugins" && req.method === "GET") {
    const registry = loadRegistry();
    const withState = registry.map(m => ({ ...m, state: manager.getState(m.id) }));
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(withState));
    return;
  }

  // POST /api/plugins/:id/call  { tool, args } -> bridge to MCP child process
  const callMatch = url.pathname.match(/^\/api\/plugins\/([^/]+)\/call$/);
  if (callMatch && req.method === "POST") {
    const pluginId = callMatch[1];
    const registry = loadRegistry();
    const manifest = registry.find(m => m.id === pluginId);
    if (!manifest) { res.writeHead(404); res.end("plugin not found"); return; }

    const body = await readBody(req);
    try {
      const result = await manager.callTool(pluginId, manifest, body.tool, body.args || {});
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ result }));
    } catch (err) {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: err.message }));
    }
    return;
  }

  // Plugin UI static files: /plugins/:id/ui/...
  const uiMatch = url.pathname.match(/^\/plugins\/([^/]+)\/ui\/(.*)$/);
  if (uiMatch) {
    const [, pluginId, rest] = uiMatch;
    const filePath = path.join(PLUGINS_DIR, pluginId, "ui", rest || "index.html");
    serveFile(res, filePath);
    return;
  }

  // Launcher static files
  let filePath = url.pathname === "/" ? "/launcher.html" : url.pathname;
  serveFile(res, path.join(PUBLIC_DIR, filePath));
});

server.listen(PORT, () => {
  console.log(`\n[shell] running at http://localhost:${PORT}\n`);
});

process.on("SIGINT", () => { manager.killAll(); process.exit(0); });
