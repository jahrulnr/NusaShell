#!/usr/bin/env node
/**
 * Standalone ACP Codex smoke against @agentclientprotocol/codex-acp.
 * Mirrors NusaShell AcpJsonRpcClient handshake; auto-answers permissions.
 *
 * Usage:
 *   node tests/test-acp-codex.mjs
 *   OPENAI_API_KEY=... AUTH_METHOD=openai-api-key node tests/test-acp-codex.mjs
 *   CODEX_PATH=$(command -v codex) node tests/test-acp-codex.mjs
 *   PROMPT="..." TIMEOUT_MS=90000 node tests/test-acp-codex.mjs
 *
 * Env knobs:
 *   CODEX_ACP_CMD   - default: npx
 *   CODEX_ACP_ARGS  - JSON array or space-separated; default: -y @agentclientprotocol/codex-acp
 *   AUTH_METHOD     - force authenticate methodId (default: first advertised, prefer openai-api-key/codex-api-key then chatgpt)
 *   NO_BROWSER      - default 1
 *   INITIAL_AGENT_MODE - default agent
 *   SKIP_PROMPT     - 1 = stop after session/new
 *   AUTO_PERMIT     - default 1; answer allow-once / allow_once / allow
 *
 * Artifacts land in tests/results/ (gitignored).
 */

import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const TESTS_DIR = import.meta.dirname;
const OUT_DIR = resolve(TESTS_DIR, "results");
const REPORT_PATH = resolve(OUT_DIR, "acp-codex-smoke-report.json");
const CWD = resolve(OUT_DIR, "workspaces", "acp-codex");
const TIMEOUT_MS = Number(process.env.TIMEOUT_MS ?? 120_000);
const SKIP_PROMPT = process.env.SKIP_PROMPT === "1";
const AUTO_PERMIT = process.env.AUTO_PERMIT !== "0";
const PROMPT =
  process.env.PROMPT ??
  "List the top-level file and directory names in the current workspace. Reply with only the names, one per line.";

mkdirSync(CWD, { recursive: true });
writeFileSync(resolve(CWD, "hello-from-smoke.txt"), "nusa acp codex smoke\n");

function parseArgs(raw, fallback) {
  if (!raw) return fallback;
  const trimmed = raw.trim();
  if (trimmed.startsWith("[")) return JSON.parse(trimmed);
  return trimmed.split(/\s+/).filter(Boolean);
}

const command = process.env.CODEX_ACP_CMD ?? "npx";
const args = parseArgs(process.env.CODEX_ACP_ARGS, ["-y", "@agentclientprotocol/codex-acp"]);

const report = {
  startedAt: new Date().toISOString(),
  command,
  args,
  cwd: CWD,
  envHints: {
    hasOpenAiApiKey: Boolean(process.env.OPENAI_API_KEY),
    hasCodexApiKey: Boolean(process.env.CODEX_API_KEY),
    hasOpenAiAlias: Boolean(process.env.OPENAI),
    codexPath: process.env.CODEX_PATH ?? null,
    noBrowser: process.env.NO_BROWSER ?? "1",
    initialAgentMode: process.env.INITIAL_AGENT_MODE ?? "agent",
  },
  initialize: null,
  authenticate: null,
  sessionNew: null,
  permissionRequests: [],
  updates: [],
  promptResult: null,
  errors: [],
  ok: false,
};

function summarize(value, depth = 0) {
  if (value == null || typeof value !== "object") return value;
  if (Array.isArray(value)) {
    if (depth > 2) return `[array:${value.length}]`;
    return value.slice(0, 40).map((item) => summarize(item, depth + 1));
  }
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (key === "content" && typeof item === "string" && item.length > 240) {
      out[key] = `${item.slice(0, 240)}…(${item.length})`;
    } else if (key.toLowerCase().includes("token") || key.toLowerCase().includes("key") || key.toLowerCase().includes("secret")) {
      out[key] = typeof item === "string" ? `<redacted len=${item.length}>` : "<redacted>";
    } else {
      out[key] = depth > 3 ? typeof item : summarize(item, depth + 1);
    }
  }
  return out;
}

class AcpProbe {
  constructor() {
    this.nextId = 1;
    this.pending = new Map();
    this.buffer = "";
    this.stderr = "";
    this.sessionId = null;
    this.child = spawn(command, args, {
      cwd: CWD,
      env: {
        ...process.env,
        NO_BROWSER: process.env.NO_BROWSER ?? "1",
        INITIAL_AGENT_MODE: process.env.INITIAL_AGENT_MODE ?? "agent",
        // Prefer explicit API key vars; do not invent OPENAI_API_KEY from OPENAI.
      },
      stdio: ["pipe", "pipe", "pipe"],
    });

    this.child.stdout.setEncoding("utf8");
    this.child.stdout.on("data", (chunk) => this.onStdout(chunk));
    this.child.stderr.setEncoding("utf8");
    this.child.stderr.on("data", (chunk) => {
      this.stderr += chunk;
    });
    this.child.on("error", (err) => {
      report.errors.push(`spawn: ${err.message}`);
      this.rejectAll(err);
    });
    this.child.on("exit", (code, signal) => {
      const err = new Error(`codex-acp exited code=${code} signal=${signal}`);
      report.errors.push(err.message);
      this.rejectAll(err);
    });
  }

  rejectAll(err) {
    for (const [, pending] of this.pending) pending.reject(err);
    this.pending.clear();
  }

  onStdout(chunk) {
    this.buffer += chunk;
    let idx;
    while ((idx = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, idx).trim();
      this.buffer = this.buffer.slice(idx + 1);
      if (!line) continue;
      let msg;
      try {
        msg = JSON.parse(line);
      } catch (error) {
        report.errors.push(`bad json line: ${line.slice(0, 200)}`);
        continue;
      }
      this.onMessage(msg);
    }
  }

  onMessage(msg) {
    if (msg.id != null && (msg.result !== undefined || msg.error !== undefined) && !msg.method) {
      const pending = this.pending.get(msg.id);
      if (!pending) return;
      this.pending.delete(msg.id);
      if (msg.error) {
        pending.reject(new Error(`${pending.method} → ${msg.error.code}: ${msg.error.message}`));
      } else {
        pending.resolve(msg.result);
      }
      return;
    }

    if (msg.method && msg.id != null) {
      void this.handleServerRequest(msg);
      return;
    }

    if (msg.method === "session/update") {
      const update = msg.params?.update ?? msg.params;
      report.updates.push(summarize(update));
      const kind = update?.sessionUpdate ?? update?.type ?? "unknown";
      process.stderr.write(`[update] ${kind}\n`);
    }
  }

  async handleServerRequest(msg) {
    const { method, params, id } = msg;
    if (method === "session/request_permission") {
      const options = params?.options ?? [];
      const tool = params?.toolCall ?? params?.tool ?? null;
      report.permissionRequests.push(summarize({ options, toolCall: tool, params }));
      process.stderr.write(`[permission] options=${options.map((o) => o.optionId ?? o.id).join(",")}\n`);
      const chosen = pickPermissionOption(options);
      if (!AUTO_PERMIT || !chosen) {
        this.respond(id, null, { code: -32000, message: "permission not auto-answered" });
        return;
      }
      this.respond(id, { outcome: { outcome: "selected", optionId: chosen } });
      return;
    }

    // Client fs/terminal capabilities are false; reject vendor extras we don't implement.
    report.errors.push(`unhandled server request: ${method}`);
    this.respond(id, null, { code: -32601, message: `Method not found: ${method}` });
  }

  respond(id, result, error) {
    const msg = error
      ? { jsonrpc: "2.0", id, error }
      : { jsonrpc: "2.0", id, result };
    this.send(msg);
  }

  send(msg) {
    this.child.stdin.write(`${JSON.stringify(msg)}\n`);
  }

  request(method, params) {
    const id = this.nextId++;
    const payload = { jsonrpc: "2.0", id, method, params };
    this.send(payload);
    return new Promise((resolvePromise, reject) => {
      this.pending.set(id, { resolve: resolvePromise, reject, method });
    });
  }

  async close() {
    try {
      if (this.sessionId) {
        this.send({ jsonrpc: "2.0", method: "session/close", params: { sessionId: this.sessionId } });
      }
    } catch {
      // ignore
    }
    this.child.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 300));
    if (!this.child.killed) this.child.kill("SIGKILL");
  }
}

function pickPermissionOption(options) {
  const ids = options.map((o) => o.optionId ?? o.id).filter(Boolean);
  const preferred = [
    "allow-once",
    "allow_once",
    "allowOnce",
    "allow",
    "approved",
    "approve",
    "yes",
  ];
  for (const want of preferred) {
    const hit = ids.find((id) => id === want || id.toLowerCase().includes(want.replace("-", "")));
    if (hit) return hit;
  }
  // Prefer non-reject options
  const nonReject = options.find((o) => {
    const kind = String(o.kind ?? "").toLowerCase();
    const id = String(o.optionId ?? o.id ?? "").toLowerCase();
    return !kind.includes("reject") && !id.includes("reject") && !id.includes("deny") && !id.includes("cancel");
  });
  return nonReject?.optionId ?? nonReject?.id ?? ids[0] ?? null;
}

function chooseAuthMethod(authMethods) {
  const ids = (authMethods ?? []).map((m) => m.id);
  if (process.env.AUTH_METHOD) {
    if (!ids.includes(process.env.AUTH_METHOD)) {
      throw new Error(`AUTH_METHOD=${process.env.AUTH_METHOD} not in advertised: ${ids.join(", ") || "(none)"}`);
    }
    return process.env.AUTH_METHOD;
  }
  // codex-acp@1.1.7 advertises `api-key` (not openai-api-key / chatgpt).
  // Only call it when a key is present — otherwise ~/.codex ChatGPT tokens
  // still allow session/new, and a failed authenticate is noise.
  const prefer = ["api-key", "openai-api-key", "codex-api-key", "chatgpt"];
  const hasKey = Boolean(process.env.OPENAI_API_KEY || process.env.CODEX_API_KEY);
  for (const id of prefer) {
    if (!ids.includes(id)) continue;
    if (id === "api-key" || id === "openai-api-key" || id === "codex-api-key") {
      if (hasKey) return id;
      continue;
    }
    return id;
  }
  return null;
}

function extractTextDeltas(updates) {
  const parts = [];
  for (const u of updates) {
    const kind = u?.sessionUpdate ?? u?.type;
    if (kind === "agent_message_chunk" || kind === "agent_message") {
      const text = u?.content?.text ?? u?.text ?? u?.content;
      if (typeof text === "string") parts.push(text);
    }
    if (kind === "message" && typeof u?.content === "string") parts.push(u.content);
  }
  return parts.join("");
}

async function main() {
  const probe = new AcpProbe();
  const timer = setTimeout(() => {
    report.errors.push(`timeout after ${TIMEOUT_MS}ms`);
    void finish(probe, 1);
  }, TIMEOUT_MS);

  try {
    process.stderr.write(`[spawn] ${command} ${args.join(" ")}\n`);
    const init = await probe.request("initialize", {
      protocolVersion: 1,
      clientCapabilities: {
        fs: { readTextFile: false, writeTextFile: false },
        terminal: false,
      },
      clientInfo: {
        name: "nusashell-smoke",
        title: "NusaShell ACP Codex Smoke",
        version: "0.0.1",
      },
    });
    report.initialize = summarize(init);
    const authMethods = init?.authMethods ?? [];
    process.stderr.write(`[initialize] authMethods=${authMethods.map((m) => m.id).join(",") || "(none)"}\n`);
    process.stderr.write(`[initialize] agentInfo=${JSON.stringify(summarize(init?.agentInfo ?? init?.agentCapabilities ?? null))}\n`);

    const methodId = chooseAuthMethod(authMethods);
    if (methodId) {
      process.stderr.write(`[authenticate] methodId=${methodId}\n`);
      try {
        const authResult = await probe.request("authenticate", { methodId });
        report.authenticate = { methodId, result: summarize(authResult) };
      } catch (error) {
        report.authenticate = { methodId, error: error.message };
        // ChatGPT with existing tokens may still allow session/new without explicit authenticate.
        process.stderr.write(`[authenticate] failed: ${error.message}\n`);
      }
    } else {
      report.authenticate = { methodId: null, note: "no auth methods advertised" };
    }

    const sessionNew = await probe.request("session/new", {
      cwd: CWD,
      mcpServers: [],
    });
    probe.sessionId = sessionNew.sessionId;
    report.sessionNew = summarize(sessionNew);
    const configOptions = sessionNew.configOptions ?? [];
    process.stderr.write(`[session/new] sessionId=${sessionNew.sessionId}\n`);
    process.stderr.write(
      `[session/new] configOptions=${configOptions.map((o) => `${o.id ?? o.configId}:${o.currentValue ?? o.value ?? "?"}`).join(" | ") || "(none)"}\n`,
    );

    if (!SKIP_PROMPT) {
      process.stderr.write(`[prompt] ${PROMPT}\n`);
      const promptResult = await probe.request("session/prompt", {
        sessionId: probe.sessionId,
        prompt: [{ type: "text", text: PROMPT }],
      });
      report.promptResult = summarize(promptResult);
      const text = extractTextDeltas(report.updates);
      process.stderr.write(`[prompt] done; textChars=${text.length}; permissions=${report.permissionRequests.length}\n`);
      if (text) process.stderr.write(`[reply]\n${text}\n`);
    }

    report.ok =
      Boolean(report.initialize) &&
      Boolean(report.sessionNew?.sessionId) &&
      (SKIP_PROMPT || report.promptResult != null || report.updates.length > 0);

    clearTimeout(timer);
    await finish(probe, report.ok ? 0 : 1);
  } catch (error) {
    report.errors.push(error instanceof Error ? error.message : String(error));
    if (probe.stderr.trim()) {
      report.stderrTail = probe.stderr.trim().slice(-4000);
    }
    clearTimeout(timer);
    await finish(probe, 1);
  }
}

async function finish(probe, code) {
  report.finishedAt = new Date().toISOString();
  report.stderrTail = (probe.stderr || "").trim().slice(-4000) || report.stderrTail || null;
  report.textReply = extractTextDeltas(report.updates);
  writeFileSync(REPORT_PATH, JSON.stringify(report, null, 2));
  process.stderr.write(`[report] ${REPORT_PATH}\n`);
  process.stderr.write(`[summary] ok=${report.ok} auth=${report.authenticate?.methodId ?? "none"} permissions=${report.permissionRequests.length} updates=${report.updates.length} errors=${report.errors.length}\n`);
  try {
    await probe.close();
  } catch {
    // ignore
  }
  process.exit(code);
}

await main();
