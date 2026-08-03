import { spawn, type ChildProcess } from "node:child_process";
import { ApplicationError, type LoggerPort } from "@nusashell/application";
import type {
  AcpClientPort,
  AcpClientSink,
  AcpConfigOption,
  AcpConfigOptionValue,
  AcpContentBlock,
  AcpPermissionOption,
  AcpPermissionRequest,
  AcpProviderDescriptor,
  AcpToolCall,
  AcpToolKind,
  AcpToolStatus,
} from "@nusashell/application";
import { enrichSpawnEnv, formatSpawnEnoentHint } from "../process/spawn-env.js";
import { resolveAcpExtension, parsePlanSteps } from "./extensions/index.js";

interface JsonRpcRequestMessage {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params?: unknown;
}

interface JsonRpcNotification {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
}

interface JsonRpcResponse {
  jsonrpc: "2.0";
  id: number;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

type JsonRpcOutbound = JsonRpcRequestMessage | JsonRpcResponse | JsonRpcNotification;

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  method: string;
}

interface Session {
  readonly conversationId: string;
  readonly child: ChildProcess;
  readonly provider: AcpProviderDescriptor;
  readonly extension: ReturnType<typeof resolveAcpExtension>;
  sessionId: string;
  traceId: string | null;
  nextId: number;
  pending: Map<number, PendingRequest>;
  sink: AcpClientSink | undefined;
  buffer: string;
  stderrBuffer: string;
  closed: boolean;
  configOptions: readonly AcpConfigOption[];
}

function toolKindFrom(kind: string | undefined): AcpToolKind {
  switch (kind) {
    case "terminal":
    case "execute":
    case "run_command":
      return "terminal";
    case "read":
    case "read_file":
    case "search":
    case "fetch":
      return "read";
    case "edit":
    case "write_file":
    case "delete":
    case "move":
      return "edit";
    default:
      return "unknown";
  }
}

function toolStatusFrom(status: string | undefined): AcpToolStatus {
  switch (status) {
    case "ok":
    case "success":
    case "completed":
      return "ok";
    case "fail":
    case "error":
    case "failed":
      return "fail";
    case "running":
    case "in_progress":
      return "running";
    default:
      return "pending";
  }
}

export class AcpJsonRpcClient implements AcpClientPort {
  private readonly sessions = new Map<string, Session>();

  constructor(
    private readonly spawnFn: (command: string, args: readonly string[], options: { cwd: string; env: NodeJS.ProcessEnv }) => ChildProcess = spawn,
    private readonly logger?: LoggerPort,
  ) {}

  async startSession(
    conversationId: string,
    provider: AcpProviderDescriptor,
    cwd: string,
    sink: AcpClientSink,
  ): Promise<string> {
    if (this.sessions.has(conversationId)) {
      await this.closeSession(conversationId);
    }

    const extension = resolveAcpExtension(provider.providerId);
    const baseEnv = { ...(process.env as NodeJS.ProcessEnv), ...(provider.env ?? {}) };
    const providerEnv = extension?.enrichSpawnEnv ? extension.enrichSpawnEnv(baseEnv) : baseEnv;
    const spawnEnv = enrichSpawnEnv(provider.command, providerEnv);

    const child = this.spawnFn(provider.command, [...provider.args], {
      env: spawnEnv,
      cwd,
    });

    const session: Session = {
      conversationId,
      child,
      provider,
      extension,
      sessionId: "",
      traceId: null,
      nextId: 1,
      pending: new Map(),
      sink,
      buffer: "",
      stderrBuffer: "",
      closed: false,
      configOptions: [],
    };
    this.sessions.set(conversationId, session);

    child.on("error", (err) => {
      this.failSession(session, this.enrichSpawnError(provider.command, err));
    });

    child.on("exit", (code) => {
      if (!session.closed) {
        this.failSession(session, new Error(`ACP process exited with code ${code ?? -1}`));
      }
    });

    child.stdout?.on("data", (chunk: Buffer) => {
      this.onData(session, chunk.toString("utf8"));
    });

    child.stderr?.on("data", (chunk: Buffer) => {
      const text = chunk.toString("utf8").trim();
      if (text) session.stderrBuffer += text + "\n";
    });

    try {
      const init = (await this.request(session, "initialize", {
        protocolVersion: 1,
        clientCapabilities: {
          fs: { readTextFile: false, writeTextFile: false },
          terminal: false,
        },
        clientInfo: {
          name: "nusashell",
          title: "NusaShell",
          version: "0.0.1",
        },
      })) as { authMethods?: readonly { id: string }[]; sessionId?: string };

      await this.authenticate(session, init.authMethods);

      const sessionResult = (await this.request(session, "session/new", {
        cwd,
        mcpServers: [],
      })) as { sessionId: string; configOptions?: unknown };

      session.sessionId = sessionResult.sessionId;
      session.configOptions = parseConfigOptions(sessionResult.configOptions);
      return sessionResult.sessionId;
    } catch (error) {
      this.cleanup(session);
      const enriched = this.enrichSpawnError(
        provider.command,
        error instanceof Error ? error : new Error(String(error)),
      );
      throw new ApplicationError(
        "AGENT_PROVIDER_FAILED",
        `Failed to start ACP session: ${enriched.message}`,
        { providerId: provider.providerId, conversationId },
      );
    }
  }

  /**
   * Authenticate with the provider using the descriptor's `authMethodId`.
   *
   * Auth policy:
   * - No `authMethodId` → skip (e.g. Codex relies on `~/.codex` file auth).
   * - `authMethodId` set but not advertised → hard fail with the available ids.
   * - `authMethodId` set and advertised → call `authenticate`; on failure
   *   soft-fail (log) and continue to `session/new`. File-auth users (Codex
   *   ChatGPT tokens, Cursor cached login) still reach `session/new`.
   */
  private async authenticate(
    session: Session,
    authMethods: readonly { id: string }[] | undefined,
  ): Promise<void> {
    const { provider } = session;
    if (!provider.authMethodId) return;
    const authMethodIds = authMethods?.map((m) => m.id) ?? [];
    if (!authMethodIds.includes(provider.authMethodId)) {
      throw new ApplicationError(
        "AGENT_PROVIDER_FAILED",
        `ACP provider "${provider.providerId}" did not advertise auth method "${provider.authMethodId}". Available: ${authMethodIds.length ? authMethodIds.join(", ") : "(none)"}`,
        { providerId: provider.providerId, authMethodId: provider.authMethodId, available: authMethodIds },
      );
    }
    try {
      await this.request(session, "authenticate", { methodId: provider.authMethodId });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.logger?.warn(`ACP authenticate soft-fail for ${provider.providerId} (method ${provider.authMethodId}): ${message}; continuing to session/new`);
    }
  }

  async prompt(traceId: string, conversationId: string, content: readonly AcpContentBlock[]): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) {
      throw new ApplicationError("AGENT_PROVIDER_FAILED", `No ACP session for conversation ${conversationId}`, { conversationId });
    }
    session.traceId = traceId;
    try {
      await this.request(session, "session/prompt", { sessionId: session.sessionId, prompt: content });
      if (session.sink) {
        session.sink.publish({ type: "acp.turn_end", traceId, ok: true });
      }
    } catch (error) {
      if (session.sink) {
        session.sink.publish({ type: "acp.turn_end", traceId, ok: false, error: error instanceof Error ? error.message : String(error) });
      }
      throw new ApplicationError(
        "AGENT_PROVIDER_FAILED",
        `ACP prompt failed: ${error instanceof Error ? error.message : String(error)}`,
        { traceId, conversationId, providerId: session.provider.providerId },
      );
    }
  }

  async cancel(_traceId: string, conversationId: string): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) return;
    this.send(session, { jsonrpc: "2.0", method: "session/cancel", params: { sessionId: session.sessionId } });
  }

  async closeSession(conversationId: string): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) return;
    this.send(session, { jsonrpc: "2.0", method: "session/close", params: { sessionId: session.sessionId } });
    this.cleanup(session);
  }

  getConfigOptions(conversationId: string): readonly AcpConfigOption[] {
    return this.sessions.get(conversationId)?.configOptions ?? [];
  }

  async setConfigOption(conversationId: string, configId: string, value: string | boolean): Promise<readonly AcpConfigOption[]> {
    const session = this.sessions.get(conversationId);
    if (!session) {
      throw new ApplicationError("AGENT_PROVIDER_FAILED", `No ACP session for conversation ${conversationId}`, { conversationId });
    }
    const result = (await this.request(session, "session/set_config_option", {
      sessionId: session.sessionId,
      configId,
      value,
    })) as { configOptions?: unknown };
    session.configOptions = parseConfigOptions(result.configOptions);
    return session.configOptions;
  }

  private request(session: Session, method: string, params?: unknown): Promise<unknown> {
    const id = session.nextId++;
    this.send(session, { jsonrpc: "2.0", id, method, params });
    return new Promise<unknown>((resolve, reject) => {
      session.pending.set(id, { resolve, reject, method });
    });
  }

  private send(session: Session, message: JsonRpcOutbound): void {
    if (session.closed || !session.child.stdin) return;
    const line = JSON.stringify(message) + "\n";
    session.child.stdin.write(line);
  }

  private onData(session: Session, data: string): void {
    session.buffer += data;
    const lines = session.buffer.split("\n");
    session.buffer = lines.pop() ?? "";
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      try {
        const message = JSON.parse(trimmed) as JsonRpcResponse | JsonRpcNotification | JsonRpcRequestMessage;
        this.handleMessage(session, message);
      } catch {
        // Ignore malformed lines.
      }
    }
  }

  private handleMessage(
    session: Session,
    message: JsonRpcResponse | JsonRpcNotification | JsonRpcRequestMessage,
  ): void {
    if ("id" in message && message.id !== undefined) {
      if ("method" in message) {
        // server->client request
        void this.handleServerRequest(session, message);
        return;
      }
      const pending = session.pending.get(message.id);
      if (!pending) return;
      session.pending.delete(message.id);
      if (message.error) {
        const stderrTail = session.stderrBuffer.trim().split("\n").slice(-3).join("\n");
        const detail = stderrTail ? `\n[ACP stderr]\n${stderrTail}` : "";
        pending.reject(new Error(`${message.error.message} (code ${message.error.code})${detail}`));
      } else {
        pending.resolve(message.result);
      }
      return;
    }

    if ("method" in message) {
      void this.handleNotification(session, message as JsonRpcNotification);
    }
  }

  private async handleServerRequest(session: Session, request: JsonRpcRequestMessage): Promise<void> {
    const id = request.id;
    const method = request.method;
    const params = (request.params ?? {}) as Record<string, unknown>;

    if (method === "session/request_permission") {
      const req = parsePermissionRequest(params);
      if (!session.sink) return;
      const answer = await session.sink.requestPermission(req);
      this.send(session, { jsonrpc: "2.0", id, result: { outcome: { outcome: "selected", optionId: answer.optionId } } });
      return;
    }

    const extension = session.extension;
    if (extension?.handleServerRequest) {
      const ctx = {
        provider: session.provider,
        sink: session.sink,
        traceId: session.traceId,
        sessionId: session.sessionId,
      };
      try {
        const handled = await extension.handleServerRequest(ctx, method, params);
        if (handled) {
          if (handled.error) {
            this.send(session, { jsonrpc: "2.0", id, error: handled.error });
          } else {
            this.send(session, { jsonrpc: "2.0", id, result: handled.result ?? null });
          }
          return;
        }
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        this.logger?.warn(`ACP extension ${extension.constructor.name} threw for ${method}: ${message}`);
        this.send(session, { jsonrpc: "2.0", id, error: { code: -32603, message } });
        return;
      }
    }

    this.send(session, {
      jsonrpc: "2.0",
      id,
      error: { code: -32601, message: "Method not found" },
    });
  }

  private handleNotification(session: Session, notification: JsonRpcNotification): void {
    if (!session.sink) return;
    if (notification.method !== "session/update") return;
    const params = (notification.params ?? {}) as Record<string, unknown>;
    const update = (params.update ?? {}) as Record<string, unknown>;
    const traceId = session.traceId ?? session.sessionId ?? "unknown";
    const updateType = update.sessionUpdate as string | undefined;
    if (updateType) session.stderrBuffer += `[acp-debug] sessionUpdate=${updateType} messageId=${String(update.messageId ?? "none")}\n`;
    switch (updateType) {
      case "agent_message_chunk": {
        const content = update.content as Record<string, unknown> | undefined;
        const text = content ? String(content.text ?? "") : "";
        if (text) {
          const messageId = String(update.messageId ?? "");
          session.sink.publish({ type: "acp.text_delta", traceId, delta: text, messageId: messageId || undefined });
        }
        break;
      }
      case "user_message_chunk":
        // The agent echoes the user's message back. Do not display it as agent output.
        break;
      case "agent_thought_chunk": {
        const content = update.content as Record<string, unknown> | undefined;
        const text = content ? String(content.text ?? "") : "";
        if (text) session.sink.publish({ type: "acp.thought_delta", traceId, delta: text });
        break;
      }
      case "tool_call": {
        session.sink.publish({
          type: "acp.tool_call",
          traceId,
          call: toToolCall(update),
        });
        break;
      }
      case "tool_call_update": {
        session.sink.publish({
          type: "acp.tool_call_update",
          traceId,
          callId: String(update.toolCallId ?? ""),
          status: toolStatusFrom(update.status as string | undefined),
          summary: summarizeToolUpdate(update),
        });
        break;
      }
      case "plan": {
        const steps = parsePlanSteps(update.entries);
        if (steps.length > 0) {
          session.sink.publish({ type: "acp.plan", traceId, steps });
        }
        break;
      }
      case "usage_update":
      case "session_info_update":
      case "available_commands_update":
        // Not surfaced to the UI yet.
        break;
    }
  }

  private failSession(session: Session, error: Error): void {
    for (const pending of session.pending.values()) {
      pending.reject(error);
    }
    session.pending.clear();
    if (session.sink) {
      session.sink.publish({
        type: "acp.turn_end",
        traceId: session.sessionId ?? "unknown",
        ok: false,
        error: error.message,
      });
    }
    this.cleanup(session);
  }

  private enrichSpawnError(command: string, error: Error): Error {
    const code = "code" in error ? String((error as { code?: unknown }).code) : "";
    if (code !== "ENOENT" && !/spawn .* ENOENT/i.test(error.message)) {
      return error;
    }
    const hint = formatSpawnEnoentHint(command);
    if (error.message.includes(hint)) return error;
    return Object.assign(error, { message: `${error.message}\n${hint}` });
  }

  private cleanup(session: Session): void {
    if (session.closed) return;
    session.closed = true;
    session.sink = undefined;
    this.sessions.delete(session.conversationId);
    if (!session.child.killed) {
      session.child.kill("SIGTERM");
    }
  }
}

function toToolCall(update: Record<string, unknown>): AcpToolCall {
  return {
    id: String(update.toolCallId ?? update.id ?? ""),
    title: String(update.title ?? update.name ?? ""),
    kind: toolKindFrom(update.kind as string | undefined),
    status: toolStatusFrom(update.status as string | undefined),
    summary: "",
    rawInput: normalizeToolRawInput(update),
  };
}

/**
 * Cursor often sends empty `rawInput` for read/edit; shell tools put the
 * command in `rawInput.command` or in a backticked `title`. Prefer whatever
 * is useful for the UI args panel.
 */
function normalizeToolRawInput(update: Record<string, unknown>): unknown {
  const raw = update.rawInput;
  if (raw && typeof raw === "object" && !Array.isArray(raw) && Object.keys(raw as object).length > 0) {
    return raw;
  }
  const title = typeof update.title === "string" ? update.title.trim() : "";
  if (title.startsWith("`") && title.endsWith("`") && title.length > 2) {
    return { command: title.slice(1, -1) };
  }
  if (typeof raw === "object" && raw !== null) return raw;
  return {};
}

/**
 * Build a short UI summary from Cursor/Codex tool_call_update payloads.
 * Prefer path (diff), then stdout/stderr, then nested text content.
 */
export function summarizeToolUpdate(update: Record<string, unknown>): string | undefined {
  const content = update.content;
  if (Array.isArray(content)) {
    for (const item of content) {
      if (typeof item !== "object" || item === null) continue;
      const rec = item as Record<string, unknown>;
      if (rec.type === "diff" && typeof rec.path === "string" && rec.path.trim()) {
        return rec.path.trim();
      }
      const nested = rec.content as Record<string, unknown> | undefined;
      if (nested && typeof nested.text === "string" && nested.text.trim()) {
        return nested.text.trim().slice(0, 2_000);
      }
      if (typeof rec.text === "string" && rec.text.trim()) {
        return rec.text.trim().slice(0, 2_000);
      }
    }
  }

  const rawOutput = update.rawOutput;
  if (typeof rawOutput === "string" && rawOutput.trim()) {
    return rawOutput.trim().slice(0, 2_000);
  }
  if (typeof rawOutput === "object" && rawOutput !== null) {
    const out = rawOutput as Record<string, unknown>;
    if (typeof out.stdout === "string" && out.stdout.trim()) return out.stdout.trim().slice(0, 2_000);
    if (typeof out.stderr === "string" && out.stderr.trim()) return out.stderr.trim().slice(0, 2_000);
    if (typeof out.content === "string" && out.content.trim()) return out.content.trim().slice(0, 2_000);
  }
  return undefined;
}

function parsePermissionRequest(params: Record<string, unknown>): AcpPermissionRequest {
  const options: AcpPermissionOption[] = [];
  const rawOptions = Array.isArray(params.options) ? params.options : [];
  for (const opt of rawOptions) {
    if (typeof opt !== "object" || opt === null) continue;
    const o = opt as Record<string, unknown>;
    const optionId = String(o.optionId ?? o.id ?? "");
    const name = String(o.name ?? o.label ?? optionId);
    if (!optionId) continue;
    options.push({ optionId, name, kind: (o.kind as AcpPermissionOption["kind"] | undefined) ?? undefined });
  }
  const toolCall = (params.toolCall ?? {}) as Record<string, unknown>;
  return {
    requestId: String(params.sessionId ?? params.requestId ?? ""),
    toolTitle: String(toolCall.title ?? params.toolTitle ?? params.title ?? ""),
    detail: toolCall.detail ? String(toolCall.detail) : (params.detail ? String(params.detail) : undefined),
    options,
  };
}

function parseConfigOptions(raw: unknown): readonly AcpConfigOption[] {
  if (!Array.isArray(raw)) return [];
  const result: AcpConfigOption[] = [];
  for (const item of raw) {
    if (typeof item !== "object" || item === null) continue;
    const o = item as Record<string, unknown>;
    const id = String(o.id ?? "");
    const name = String(o.name ?? "");
    const type = (o.type as string | undefined) === "boolean" ? "boolean" : "select";
    if (!id || !name) continue;
    const options: AcpConfigOptionValue[] = [];
    if (Array.isArray(o.options)) {
      for (const opt of o.options) {
        if (typeof opt !== "object" || opt === null) continue;
        const ov = opt as Record<string, unknown>;
        const value = String(ov.value ?? "");
        const optName = String(ov.name ?? value);
        if (!value) continue;
        options.push({ value, name: optName, description: ov.description ? String(ov.description) : undefined });
      }
    }
    result.push({
      id,
      name,
      description: o.description ? String(o.description) : undefined,
      category: o.category ? String(o.category) : undefined,
      type,
      currentValue: typeof o.currentValue === "boolean" ? o.currentValue : String(o.currentValue ?? ""),
      options: options.length > 0 ? options : undefined,
    });
  }
  return result;
}
