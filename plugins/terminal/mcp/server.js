#!/usr/bin/env node
import os from "node:os";
import fs from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { getTerminalPrompt, TERMINAL_PROMPTS } from "./prompts.js";
import {
  CallToolRequestSchema,
  GetPromptRequestSchema,
  ListPromptsRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";

// Resolve from the launched entrypoint in both ESM dev mode and the bundled
// CJS package. This avoids import.meta/`__dirname` format differences in esbuild
// and keeps packaged resource lookup independent from the current working dir.
const moduleDir = process.argv[1]
  ? path.dirname(path.resolve(process.argv[1]))
  : path.dirname(process.execPath);

let pty;
try {
  pty = require("node-pty");
} catch (err) {
  console.error("[terminal-mcp] node-pty is required for terminal sessions:", err.message);
}

const HOME = os.homedir();
const MAX_BUFFER_CHARS = 200 * 1024;
// Keep shell bootstrap files with the host-owned runtime data. Falling back to
// the OS temp directory is only for running the plugin standalone outside the
// NusaShell broker.
const BOOTSTRAP_ROOT = process.env.NUSASHELL_USER_DATA
  ? path.join(path.resolve(process.env.NUSASHELL_USER_DATA), "runtime")
  : os.tmpdir();
const BOOTSTRAP_DIR = path.join(BOOTSTRAP_ROOT, "terminal-bootstrap");
const BASH_RC = path.join(BOOTSTRAP_DIR, "bashrc");
const ZSH_RC = path.join(BOOTSTRAP_DIR, ".zshrc");
const COLOR_BOOTSTRAP_SRC = path.join(moduleDir, "color-bootstrap.sh");

function ensureBootstrapFiles() {
  fs.mkdirSync(BOOTSTRAP_DIR, { recursive: true });
  const color = fs.readFileSync(COLOR_BOOTSTRAP_SRC, "utf8");
  fs.writeFileSync(
    BASH_RC,
    `# NusaShell bash bootstrap\n[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"\n${color}`,
  );
  fs.writeFileSync(
    ZSH_RC,
    `# NusaShell zsh bootstrap\n[ -f "$HOME/.zshrc" ] && . "$HOME/.zshrc"\n${color}`,
  );
}

function shellSpawnArgs(shell) {
  const base = path.basename(shell || "");
  // Do not pass -i together with --rcfile: bash then errors with
  // "/bin/bash: --: invalid option" under node-pty.
  if (base === "bash") {
    return ["--rcfile", BASH_RC];
  }
  if (base === "zsh" || String(shell).endsWith("/zsh")) {
    return [];
  }
  return [];
}

function shellSpawnEnv(shell, baseEnv) {
  const env = { ...baseEnv };
  const base = path.basename(shell || "");
  if (base === "zsh" || String(shell).endsWith("/zsh")) {
    env.ZDOTDIR = BOOTSTRAP_DIR;
  }
  return env;
}

try {
  ensureBootstrapFiles();
} catch (err) {
  console.error("[terminal-mcp] failed to write bootstrap rc:", err.message);
}

function defaultCwd() {
  return HOME;
}

function resolveCwd(input) {
  const cwd = typeof input === "string" && input.trim() ? input.trim() : defaultCwd();
  if (!path.isAbsolute(cwd)) {
    throw new Error(`cwd must be an absolute path (got: ${cwd}). The conversation workspace is not applied automatically; pass the full path explicitly.`);
  }
  const stat = fs.statSync(cwd, { throwIfNoEntry: false });
  if (!stat || !stat.isDirectory()) {
    throw new Error(`cwd is not a directory: ${cwd}`);
  }
  return cwd;
}

function defaultShell() {
  return process.env.SHELL || (process.platform === "win32" ? "cmd.exe" : "/bin/bash");
}

function trimBuffer(text) {
  if (text.length > MAX_BUFFER_CHARS) return text.slice(text.length - MAX_BUFFER_CHARS);
  return text;
}

const server = new Server(
  { name: "nusashell-terminal", version: "1.0.0" },
  { capabilities: { tools: {}, prompts: {} } },
);

server.setRequestHandler(ListPromptsRequestSchema, async () => ({
  prompts: TERMINAL_PROMPTS,
}));

server.setRequestHandler(GetPromptRequestSchema, async (request) =>
  getTerminalPrompt(request.params.name));

const sessions = new Map();

function createSession(opts = {}) {
  if (!pty) throw new Error("node-pty is not available; rebuild the terminal plugin dependencies.");
  const shell = opts.shell || defaultShell();
  const cwd = resolveCwd(opts.cwd);
  const cols = Number.isFinite(opts.cols) ? Math.max(1, Math.floor(opts.cols)) : 120;
  const rows = Number.isFinite(opts.rows) ? Math.max(1, Math.floor(opts.rows)) : 30;
  const id = randomUUID();

  const baseEnv = {
    ...process.env,
    HOME,
    TERM: "xterm-256color",
    COLORTERM: "truecolor",
  };
  const term = pty.spawn(shell, shellSpawnArgs(shell), {
    name: "xterm-256color",
    cwd,
    cols,
    rows,
    env: shellSpawnEnv(shell, baseEnv),
  });

  const session = {
    id,
    term,
    shell,
    cwd,
    cols,
    rows,
    buffer: "",
    createdAt: Date.now(),
    exited: false,
    exitCode: null,
  };

  term.onData((data) => {
    session.buffer = trimBuffer(session.buffer + data);
  });
  term.onExit(({ exitCode }) => {
    session.exited = true;
    session.exitCode = exitCode;
  });

  sessions.set(id, session);
  return session;
}

function getSession(id) {
  const session = sessions.get(id);
  if (!session) throw new Error(`Session not found: ${id}`);
  return session;
}

function drainBuffer(session, clear = true) {
  const stdout = session.buffer;
  if (clear) session.buffer = "";
  return { stdout, stderr: "" };
}

function runExec({ command, cwd, timeoutMs }, extra) {
  return new Promise((resolve, reject) => {
    if (typeof command !== "string" || !command.trim()) {
      reject(new Error("command is required"));
      return;
    }
    const resolvedCwd = resolveCwd(cwd);
    const shell = defaultShell();
    const args = process.platform === "win32" ? ["/d", "/s", "/c", command] : ["-lc", command];
    const child = spawn(shell, args, { cwd: resolvedCwd, env: { ...process.env, HOME } });

    let stdout = "";
    let stderr = "";
    let killed = false;
    const max = MAX_BUFFER_CHARS;
    const progressToken = extra?._meta?.progressToken;
    const signal = extra?.signal;
    let progressSeq = 0;

    const sendProgress = (text) => {
      if (progressToken === undefined) return;
      progressSeq++;
      const chunk = text.slice(-2000);
      extra.sendNotification({
        method: "notifications/progress",
        params: { progressToken, progress: progressSeq, message: chunk },
      }).catch(() => {});
    };

    const timer = timeoutMs
      ? setTimeout(() => {
          killed = true;
          child.kill("SIGKILL");
        }, timeoutMs)
      : null;

    // If the abort signal fires (async_kill / turn cancel), kill the child process.
    const onAbort = () => {
      killed = true;
      try { child.kill("SIGKILL"); } catch {}
    };
    if (signal) {
      if (signal.aborted) { onAbort(); }
      else { signal.addEventListener("abort", onAbort, { once: true }); }
    }

    child.stdout.on("data", (chunk) => {
      const text = chunk.toString();
      stdout = trimBuffer(stdout + text);
      sendProgress(text);
    });
    child.stderr.on("data", (chunk) => {
      const text = chunk.toString();
      stderr = trimBuffer(stderr + text);
      sendProgress(text);
    });
    child.on("error", (err) => {
      if (timer) clearTimeout(timer);
      if (signal) signal.removeEventListener("abort", onAbort);
      reject(err);
    });
    child.on("close", (code, signalName) => {
      if (timer) clearTimeout(timer);
      if (signal) signal.removeEventListener("abort", onAbort);
      resolve({ stdout, stderr, exitCode: code, signal: signalName, timedOut: killed, cwd: resolvedCwd, shell });
    });
  });
}

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "exec",
      description:
        "Run a one-shot shell command and return stdout/stderr/exitCode. cwd defaults to the user's home directory; the conversation workspace is not applied automatically, so pass an absolute cwd if you want a specific folder.",
      inputSchema: {
        type: "object",
        required: ["command"],
        properties: {
          command: { type: "string", description: "Shell command to execute (run via the user's login shell)." },
          cwd: { type: "string", description: `Absolute working directory (default: ${HOME}).` },
          timeoutMs: { type: "number", description: "Optional timeout in milliseconds before the command is killed." },
        },
      },
    },
    {
      name: "open",
      description:
        "Open a new interactive terminal session (PTY) in the user's shell. cwd defaults to the user's home directory; the conversation workspace is not applied automatically, so pass an absolute cwd to open elsewhere.",
      inputSchema: {
        type: "object",
        properties: {
          shell: { type: "string", description: `Shell command (default: $SHELL or ${defaultShell()})` },
          cwd: { type: "string", description: `Absolute working directory (default: ${HOME}).` },
          cols: { type: "number", description: "Columns (default: 120)" },
          rows: { type: "number", description: "Rows (default: 30)" },
        },
      },
    },
    {
      name: "write",
      description: "Write input to a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId", "data"],
        properties: {
          sessionId: { type: "string" },
          data: { type: "string", description: "Text to send to the terminal (include \\n to run a command)." },
        },
      },
    },
    {
      name: "read",
      description: "Read buffered output from a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId"],
        properties: {
          sessionId: { type: "string" },
          clear: { type: "boolean", description: "Clear the buffer after reading (default: true)" },
        },
      },
    },
    {
      name: "resize",
      description: "Resize a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId", "cols", "rows"],
        properties: {
          sessionId: { type: "string" },
          cols: { type: "number", minimum: 1 },
          rows: { type: "number", minimum: 1 },
        },
      },
    },
    {
      name: "close",
      description: "Close a terminal session.",
      inputSchema: {
        type: "object",
        required: ["sessionId"],
        properties: { sessionId: { type: "string" } },
      },
    },
    {
      name: "list",
      description: "List active terminal sessions.",
      inputSchema: { type: "object", properties: {} },
    },
  ],
}));

server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
  const { name, arguments: args = {} } = request.params;
  try {
    switch (name) {
      case "exec": {
        const timeoutMs = Number.isFinite(args.timeoutMs) ? Math.max(0, Math.floor(args.timeoutMs)) : null;
        const result = await runExec({ command: args.command, cwd: args.cwd, timeoutMs }, extra);
        return { content: [{ type: "text", text: JSON.stringify(result) }] };
      }
      case "open": {
        const session = createSession({
          shell: typeof args.shell === "string" ? args.shell : undefined,
          cwd: args.cwd,
          cols: args.cols,
          rows: args.rows,
        });
        return {
          content: [{
            type: "text",
            text: JSON.stringify({ sessionId: session.id, shell: session.shell, cwd: session.cwd, cols: session.cols, rows: session.rows }),
          }],
        };
      }
      case "write": {
        const session = getSession(args.sessionId);
        if (session.exited) throw new Error("Session has exited");
        session.term.write(String(args.data ?? ""));
        return { content: [{ type: "text", text: "OK" }] };
      }
      case "read": {
        const session = getSession(args.sessionId);
        const clear = args.clear === undefined ? true : Boolean(args.clear);
        const { stdout, stderr } = drainBuffer(session, clear);
        return {
          content: [{
            type: "text",
            text: JSON.stringify({ stdout, stderr, exited: session.exited, exitCode: session.exitCode }),
          }],
        };
      }
      case "resize": {
        const session = getSession(args.sessionId);
        const cols = Math.max(1, Math.floor(args.cols));
        const rows = Math.max(1, Math.floor(args.rows));
        session.cols = cols;
        session.rows = rows;
        if (!session.exited) session.term.resize(cols, rows);
        return { content: [{ type: "text", text: "OK" }] };
      }
      case "close": {
        const session = getSession(args.sessionId);
        if (!session.exited) {
          try { session.term.kill(); } catch (_) { /* ignore */ }
        }
        sessions.delete(args.sessionId);
        return { content: [{ type: "text", text: "OK" }] };
      }
      case "list": {
        const list = Array.from(sessions.values()).map((session) => ({
          sessionId: session.id,
          shell: session.shell,
          cwd: session.cwd,
          cols: session.cols,
          rows: session.rows,
          createdAt: session.createdAt,
          exited: session.exited,
          exitCode: session.exitCode,
        }));
        return { content: [{ type: "text", text: JSON.stringify(list, null, 2) }] };
      }
      default:
        throw new Error(`Unknown tool: ${name}`);
    }
  } catch (err) {
    return { content: [{ type: "text", text: `Error: ${err.message}` }], isError: true };
  }
});

async function main() {
  const transport = new StdioServerTransport();
  await server.connect(transport);
  console.error("[terminal-mcp] Server running on stdio");
}

main().catch((err) => {
  console.error("[terminal-mcp] fatal:", err);
  process.exit(1);
});
