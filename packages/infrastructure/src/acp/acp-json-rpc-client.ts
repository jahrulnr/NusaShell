import { spawn, type ChildProcess } from "node:child_process";
import { ApplicationError } from "@nusashell/application";
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
  AcpAskOption,
  AcpAskRequest,
  AcpAskAnswer,
  AcpPlanStep,
} from "@nusashell/application";

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

    const child = this.spawnFn(provider.command, [...provider.args], {
      env: process.env as NodeJS.ProcessEnv,
      cwd,
    });

    const session: Session = {
      conversationId,
      child,
      provider,
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
      this.failSession(session, err);
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

      const authMethodIds = init.authMethods?.map((m) => m.id) ?? [];
      if (provider.authMethodId && authMethodIds.includes(provider.authMethodId)) {
        await this.request(session, "authenticate", { methodId: provider.authMethodId });
      }

      const sessionResult = (await this.request(session, "session/new", {
        cwd,
        mcpServers: [],
      })) as { sessionId: string; configOptions?: unknown };

      session.sessionId = sessionResult.sessionId;
      session.configOptions = parseConfigOptions(sessionResult.configOptions);
      return sessionResult.sessionId;
    } catch (error) {
      this.cleanup(session);
      throw new ApplicationError(
        "AGENT_PROVIDER_FAILED",
        `Failed to start ACP session: ${error instanceof Error ? error.message : String(error)}`,
        { providerId: provider.providerId, conversationId },
      );
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

    switch (method) {
      case "session/request_permission": {
        const req = parsePermissionRequest(params);
        if (!session.sink) return;
        const answer = await session.sink.requestPermission(req);
        this.send(session, { jsonrpc: "2.0", id, result: { outcome: { outcome: "selected", optionId: answer.optionId } } });
        return;
      }
      case "cursor/ask_question": {
        const req = parseAskRequest(params);
        if (!session.sink) return;
        const answer = await session.sink.askQuestion(req);
        this.send(session, { jsonrpc: "2.0", id, result: toAskAnswerJson(answer) });
        return;
      }
      case "cursor/create_plan": {
        if (session.sink) {
          const steps = parsePlanSteps(params.steps ?? params.entries);
          if (steps.length > 0) {
            session.sink.publish({ type: "acp.plan", traceId: session.traceId ?? session.sessionId ?? "unknown", steps });
          }
        }
        this.send(session, { jsonrpc: "2.0", id, result: { accepted: true } });
        return;
      }
      default:
        this.send(session, {
          jsonrpc: "2.0",
          id,
          error: { code: -32601, message: "Method not found" },
        });
        return;
    }
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
        const content = update.content as unknown;
        let summary: string | undefined;
        if (Array.isArray(content)) {
          for (const item of content) {
            if (typeof item === "object" && item !== null) {
              const c = (item as Record<string, unknown>).content as Record<string, unknown> | undefined;
              if (c && typeof c.text === "string") { summary = c.text; break; }
            }
          }
        }
        session.sink.publish({
          type: "acp.tool_call_update",
          traceId,
          callId: String(update.toolCallId ?? ""),
          status: toolStatusFrom(update.status as string | undefined),
          summary,
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
    rawInput: update.rawInput,
  };
}

function parsePlanSteps(entries: unknown): readonly AcpPlanStep[] {
  if (!Array.isArray(entries)) return [];
  const result: AcpPlanStep[] = [];
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i];
    if (typeof entry !== "object" || entry === null) continue;
    const s = entry as Record<string, unknown>;
    const text = String(s.content ?? s.text ?? s.description ?? "");
    if (!text) continue;
    const status = String(s.status ?? "pending");
    result.push({ id: String(s.id ?? `step_${i}`), text, done: status === "completed" || status === "done" });
  }
  return result;
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

function parseAskRequest(params: Record<string, unknown>): AcpAskRequest {
  const options: AcpAskOption[] = [];
  const rawOptions = Array.isArray(params.options) ? params.options : [];
  for (const opt of rawOptions) {
    if (typeof opt !== "object" || opt === null) continue;
    const o = opt as Record<string, unknown>;
    const optionId = String(o.id ?? o.optionId ?? "");
    const name = String(o.name ?? o.label ?? optionId);
    if (!optionId) continue;
    options.push({ optionId, name });
  }
  return {
    requestId: String(params.requestId ?? ""),
    question: String(params.question ?? ""),
    options: options.length > 0 ? options : undefined,
    multiSelect: typeof params.multiSelect === "boolean" ? params.multiSelect : undefined,
    allowFreeText: typeof params.allowFreeText === "boolean" ? params.allowFreeText : undefined,
  };
}

function toAskAnswerJson(answer: AcpAskAnswer): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  if (answer.text) {
    result.text = answer.text;
  }
  if (answer.optionIds) {
    result.optionIds = answer.optionIds;
  }
  return result;
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
