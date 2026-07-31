import { describe, it, expect, afterAll } from "vitest";
import { spawn } from "node:child_process";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SERVER = path.resolve(__dirname, "../mcp/server.cjs");
const HOME = os.homedir();

function createMcpClient() {
  const child = spawn(process.execPath, [SERVER], {
    stdio: ["pipe", "pipe", "pipe"],
    env: process.env,
  });

  let buffer = "";
  const pending = new Map();
  let nextId = 1;
  let stderr = "";

  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk) => {
    stderr += chunk;
  });

  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk) => {
    buffer += chunk;
    let idx;
    while ((idx = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, idx).trim();
      buffer = buffer.slice(idx + 1);
      if (!line) continue;
      let msg;
      try {
        msg = JSON.parse(line);
      } catch {
        continue;
      }
      if (msg.id != null && pending.has(msg.id)) {
        const { resolve, reject } = pending.get(msg.id);
        pending.delete(msg.id);
        if (msg.error) reject(new Error(msg.error.message || JSON.stringify(msg.error)));
        else resolve(msg.result);
      }
    }
  });

  function request(method, params) {
    const id = nextId++;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
      setTimeout(() => {
        if (pending.has(id)) {
          pending.delete(id);
          reject(new Error(`timeout waiting for ${method}; stderr=${stderr.slice(-500)}`));
        }
      }, 10_000);
    });
  }

  async function initialize() {
    await request("initialize", {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "terminal-e2e", version: "0" },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" })}\n`);
  }

  async function listTools() {
    const result = await request("tools/list", {});
    return result.tools ?? [];
  }

  async function callTool(name, args = {}) {
    const result = await request("tools/call", { name, arguments: args });
    const text = result?.content?.find((part) => part?.type === "text")?.text;
    if (result?.isError) throw new Error(text || "tool error");
    if (!text) return result;
    try {
      return JSON.parse(text);
    } catch {
      return text;
    }
  }

  function close() {
    try {
      child.stdin.end();
    } catch {
      /* ignore */
    }
    try {
      child.kill("SIGTERM");
    } catch {
      /* ignore */
    }
  }

  return { initialize, listTools, callTool, close };
}

describe("terminal MCP e2e", () => {
  const client = createMcpClient();

  afterAll(() => client.close());

  it("initializes and lists session + exec tools", async () => {
    await client.initialize();
    const names = (await client.listTools()).map((tool) => tool.name);
    expect(names).toEqual(
      expect.arrayContaining([
        "terminal_exec",
        "terminal_open",
        "terminal_write",
        "terminal_read",
        "terminal_resize",
        "terminal_close",
        "terminal_list",
      ]),
    );
  });

  it("terminal_exec defaults cwd to the user home directory", async () => {
    const result = await client.callTool("terminal_exec", { command: "pwd" });
    expect(result.cwd).toBe(HOME);
    expect(String(result.stdout).trim()).toBe(HOME);
    expect(result.exitCode).toBe(0);
  });

  it("opens a PTY session with colored prompt / ls ANSI escapes", async () => {
    const opened = await client.callTool("terminal_open", {});
    expect(opened.sessionId).toBeTruthy();
    expect(opened.cwd).toBe(HOME);

    // Wait for bashrc bootstrap + first prompt (should include SGR / OSC escapes).
    let boot = "";
    for (let i = 0; i < 40; i++) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      const read = await client.callTool("terminal_read", {
        sessionId: opened.sessionId,
        clear: true,
      });
      boot += read.stdout || "";
      if (boot.includes("$") || boot.includes("#")) break;
    }
    expect(boot).toMatch(/\x1b\[|\x1b\]/);

    await client.callTool("terminal_write", {
      sessionId: opened.sessionId,
      data: "ls --color=always\n",
    });

    let out = "";
    for (let i = 0; i < 40; i++) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      const read = await client.callTool("terminal_read", {
        sessionId: opened.sessionId,
        clear: true,
      });
      out += read.stdout || "";
      if (out.includes("\x1b[")) break;
    }
    expect(out).toContain("\x1b[");

    await client.callTool("terminal_close", { sessionId: opened.sessionId });
  });

  it("rejects relative cwd", async () => {
    await expect(
      client.callTool("terminal_exec", { command: "pwd", cwd: "relative/path" }),
    ).rejects.toThrow(/absolute path/i);
  });
});
