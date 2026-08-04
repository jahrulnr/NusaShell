#!/usr/bin/env node
/**
 * Cursor ACP smoke — capture real session/update kinds (text / thought / tool_call).
 * Adapted from tests/test-acp-codex.mjs leftovers.
 *
 * Usage:
 *   node tests/test-acp-cursor.mjs
 *   PROMPT="..." TIMEOUT_MS=180000 node tests/test-acp-cursor.mjs
 *
 * Artifacts land in tests/results/ (gitignored).
 */

import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, readFileSync, existsSync } from "node:fs";
import { resolve } from "node:path";

const TESTS_DIR = import.meta.dirname;
const OUT_DIR = resolve(TESTS_DIR, "results");
const REPORT_PATH = resolve(OUT_DIR, "acp-cursor-smoke-report.json");
const CWD = resolve(OUT_DIR, "workspaces", "acp-cursor");
const TIMEOUT_MS = Number(process.env.TIMEOUT_MS ?? 180_000);
const AUTO_PERMIT = process.env.AUTO_PERMIT !== "0";
const PROMPT =
  process.env.PROMPT ??
  [
    "Working directory (cwd): " + CWD,
    "",
    "Create exactly one file named smoke-marker.txt in the current working directory.",
    "Content must be exactly: nusa-cursor-acp-smoke",
    "Use your file editing tools. After writing, reply with OK and the absolute path.",
  ].join("\n");

mkdirSync(CWD, { recursive: true });

const command = process.env.CURSOR_ACP_CMD ?? "agent";
const args = process.env.CURSOR_ACP_ARGS
  ? JSON.parse(process.env.CURSOR_ACP_ARGS)
  : ["acp"];

const report = {
  startedAt: new Date().toISOString(),
  command,
  args,
  cwd: CWD,
  prompt: PROMPT,
  initialize: null,
  authenticate: null,
  sessionNew: null,
  permissionRequests: [],
  updates: [],
  updateKinds: {},
  promptResult: null,
  errors: [],
  ok: false,
  markerExists: false,
  markerContent: null,
};

function summarize(value, depth = 0) {
  if (value == null || typeof value !== "object") return value;
  if (Array.isArray(value)) {
    if (depth > 2) return `[array:${value.length}]`;
    return value.slice(0, 40).map((item) => summarize(item, depth + 1));
  }
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (typeof item === "string" && item.length > 400) {
      out[key] = `${item.slice(0, 400)}…(${item.length})`;
    } else if (/token|key|secret|password/i.test(key)) {
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
      env: { ...process.env },
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
      if (this.pending.size) {
        const err = new Error(`cursor-acp exited code=${code} signal=${signal}`);
        report.errors.push(err.message);
        this.rejectAll(err);
      }
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
      } catch {
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
      if (msg.error) pending.reject(new Error(`${pending.method} → ${msg.error.code}: ${msg.error.message}`));
      else pending.resolve(msg.result);
      return;
    }
    if (msg.method && msg.id != null) {
      void this.handleServerRequest(msg);
      return;
    }
    if (msg.method === "session/update") {
      const update = msg.params?.update ?? msg.params;
      const kind = update?.sessionUpdate ?? update?.type ?? "unknown";
      report.updateKinds[kind] = (report.updateKinds[kind] ?? 0) + 1;
      report.updates.push(summarize(update));
      process.stderr.write(`[update] ${kind}\n`);
    }
  }

  async handleServerRequest(msg) {
    const { method, params, id } = msg;
    if (method === "session/request_permission") {
      const options = params?.options ?? [];
      report.permissionRequests.push(summarize({ options, toolCall: params?.toolCall ?? null }));
      process.stderr.write(`[permission] ${options.map((o) => o.optionId ?? o.id).join(",")}\n`);
      const chosen = pickPermissionOption(options);
      if (!AUTO_PERMIT || !chosen) {
        this.respond(id, null, { code: -32000, message: "permission not auto-answered" });
        return;
      }
      this.respond(id, { outcome: { outcome: "selected", optionId: chosen } });
      return;
    }
    report.errors.push(`unhandled server request: ${method}`);
    this.respond(id, null, { code: -32601, message: `Method not found: ${method}` });
  }

  respond(id, result, error) {
    this.send(error ? { jsonrpc: "2.0", id, error } : { jsonrpc: "2.0", id, result });
  }

  send(msg) {
    this.child.stdin.write(`${JSON.stringify(msg)}\n`);
  }

  request(method, params) {
    const id = this.nextId++;
    this.send({ jsonrpc: "2.0", id, method, params });
    return new Promise((resolvePromise, reject) => {
      this.pending.set(id, { resolve: resolvePromise, reject, method });
    });
  }

  async close() {
    try {
      if (this.sessionId) this.send({ jsonrpc: "2.0", method: "session/cancel", params: { sessionId: this.sessionId } });
    } catch {
      // ignore
    }
    this.child.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 400));
    if (!this.child.killed) this.child.kill("SIGKILL");
  }
}

function pickPermissionOption(options) {
  const ids = options.map((o) => o.optionId ?? o.id).filter(Boolean);
  const preferred = ["allow-once", "allow_once", "allowOnce", "allow", "approved", "approve", "yes"];
  for (const want of preferred) {
    const hit = ids.find((id) => String(id).toLowerCase().includes(want.replace("-", "")));
    if (hit) return hit;
  }
  const nonReject = options.find((o) => {
    const kind = String(o.kind ?? "").toLowerCase();
    const id = String(o.optionId ?? o.id ?? "").toLowerCase();
    return !kind.includes("reject") && !id.includes("reject") && !id.includes("deny") && !id.includes("cancel");
  });
  return nonReject?.optionId ?? nonReject?.id ?? ids[0] ?? null;
}

function extractText(updates) {
  const parts = [];
  for (const u of updates) {
    if (u?.sessionUpdate === "agent_message_chunk") {
      const text = u?.content?.text;
      if (typeof text === "string") parts.push(text);
    }
  }
  return parts.join("");
}

function timeline(updates) {
  return updates.map((u) => {
    const kind = u?.sessionUpdate ?? "?";
    if (kind === "agent_message_chunk") return { kind, text: u?.content?.text ?? "" };
    if (kind === "agent_thought_chunk") return { kind, text: u?.content?.text ?? "" };
    if (kind === "tool_call") {
      return {
        kind,
        toolCallId: u?.toolCallId ?? u?.id,
        title: u?.title ?? u?.kind ?? u?.rawInput?.toolName,
        status: u?.status,
        rawInput: u?.rawInput,
      };
    }
    if (kind === "tool_call_update") {
      return { kind, toolCallId: u?.toolCallId, status: u?.status };
    }
    return { kind };
  });
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
      clientInfo: { name: "nusashell-smoke", title: "NusaShell ACP Cursor Smoke", version: "0.0.1" },
    });
    report.initialize = summarize(init);
    process.stderr.write(`[initialize] authMethods=${(init?.authMethods ?? []).map((m) => m.id).join(",") || "(none)"}\n`);

    // Prefer file auth; only authenticate if required env says so.
    if (process.env.AUTH_METHOD) {
      try {
        const authResult = await probe.request("authenticate", { methodId: process.env.AUTH_METHOD });
        report.authenticate = { methodId: process.env.AUTH_METHOD, result: summarize(authResult) };
      } catch (error) {
        report.authenticate = { methodId: process.env.AUTH_METHOD, error: error.message };
        process.stderr.write(`[authenticate] soft-fail: ${error.message}\n`);
      }
    } else {
      report.authenticate = { methodId: null, note: "skipped; rely on Cursor file auth" };
    }

    const sessionNew = await probe.request("session/new", { cwd: CWD, mcpServers: [] });
    probe.sessionId = sessionNew.sessionId;
    report.sessionNew = summarize(sessionNew);
    process.stderr.write(`[session/new] ${sessionNew.sessionId}\n`);

    process.stderr.write(`[prompt] ${PROMPT.split("\n")[0]}…\n`);
    const promptResult = await probe.request("session/prompt", {
      sessionId: probe.sessionId,
      prompt: [{ type: "text", text: PROMPT }],
    });
    report.promptResult = summarize(promptResult);

    const marker = resolve(CWD, "smoke-marker.txt");
    report.markerExists = existsSync(marker);
    report.markerContent = report.markerExists ? readFileSync(marker, "utf8") : null;
    report.textReply = extractText(report.updates);
    report.timeline = timeline(report.updates);
    report.ok = Boolean(report.sessionNew?.sessionId) && report.updates.length > 0;

    clearTimeout(timer);
    await finish(probe, report.ok ? 0 : 1);
  } catch (error) {
    report.errors.push(error instanceof Error ? error.message : String(error));
    clearTimeout(timer);
    await finish(probe, 1);
  }
}

async function finish(probe, code) {
  report.finishedAt = new Date().toISOString();
  report.stderrTail = (probe.stderr || "").trim().slice(-4000) || null;
  if (!report.textReply) report.textReply = extractText(report.updates);
  if (!report.timeline) report.timeline = timeline(report.updates);
  writeFileSync(REPORT_PATH, JSON.stringify(report, null, 2));
  process.stderr.write(`[report] ${REPORT_PATH}\n`);
  process.stderr.write(`[kinds] ${JSON.stringify(report.updateKinds)}\n`);
  process.stderr.write(`[marker] exists=${report.markerExists} content=${JSON.stringify(report.markerContent)}\n`);
  process.stderr.write(`[summary] ok=${report.ok} permissions=${report.permissionRequests.length} updates=${report.updates.length} errors=${report.errors.length}\n`);
  try {
    await probe.close();
  } catch {
    // ignore
  }
  process.exit(code);
}

await main();
