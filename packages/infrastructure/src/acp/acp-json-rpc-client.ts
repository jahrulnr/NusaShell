import { spawn, type ChildProcess } from "node:child_process";
import { ApplicationError } from "@nusashell/application";
import type {
  AcpClientPort,
  AcpClientSink,
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
  nextId: number;
  pending: Map<number, PendingRequest>;
  sink: AcpClientSink | undefined;
  buffer: string;
  closed: boolean;
}

function toolKindFrom(kind: string | undefined): AcpToolKind {
  switch (kind) {
    case "terminal":
    case "run_command":
      return "terminal";
    case "read":
    case "read_file":
      return "read";
    case "edit":
    case "write_file":
      return "edit";
    default:
      return "unknown";
  }
}

function toolStatusFrom(status: string | undefined): AcpToolStatus {
  switch (status) {
    case "ok":
    case "success":
      return "ok";
    case "fail":
    case "error":
      return "fail";
    case "running":
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
      nextId: 1,
      pending: new Map(),
      sink,
      buffer: "",
      closed: false,
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

    child.stderr?.on("data", (_chunk: Buffer) => {
      // Surface stdio diagnostics via a turn_end event? For now, ignore or log via debug.
    });

    try {
      const init = (await this.request(session, "initialize", {
        protocolVersion: 1,
        clientCapabilities: {
          fs: { readTextFile: false, writeTextFile: false },
          terminal: false,
        },
      })) as { authMethods?: readonly string[]; sessionId?: string };

      if (provider.authMethodId && init.authMethods?.includes(provider.authMethodId)) {
        await this.request(session, "authenticate", { authMethod: provider.authMethodId });
      }

      const sessionResult = (await this.request(session, "session/new", {
        cwd,
        mcpServers: [],
      })) as { sessionId: string };

      session.sessionId = sessionResult.sessionId;
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
    try {
      await this.request(session, "session/prompt", { traceId, content });
      if (session.sink) {
        session.sink.publish({ type: "acp.turn_end", traceId, ok: true });
      }
    } catch (error) {
      if (session.sink) {
        session.sink.publish({ type: "acp.turn_end", traceId, ok: false, error: error instanceof Error ? error.message : String(error) });
      }
      throw error;
    }
  }

  async cancel(traceId: string, conversationId: string): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) return;
    this.send(session, { jsonrpc: "2.0", method: "session/cancel", params: { traceId } });
  }

  async closeSession(conversationId: string): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) return;
    this.send(session, { jsonrpc: "2.0", method: "session/exit", params: { sessionId: session.sessionId } });
    this.cleanup(session);
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
        pending.reject(new Error(`${message.error.message} (code ${message.error.code})`));
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
        this.send(session, { jsonrpc: "2.0", id, result: { optionId: answer.optionId } });
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
          const steps = parsePlanSteps(params.steps);
          if (steps.length > 0) {
            const traceId = (params.traceId as string | undefined) ?? session.sessionId ?? "unknown";
            session.sink.publish({ type: "acp.plan", traceId, steps });
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
    const traceId = (params.traceId as string | undefined) ?? session.sessionId ?? "unknown";
    const updateType = params.type as string | undefined;
    switch (updateType) {
      case "agent_message_chunk":
      case "text_delta":
        session.sink.publish({ type: "acp.text_delta", traceId, delta: String(params.delta ?? "") });
        break;
      case "agent_thought_chunk":
      case "thought_delta":
        session.sink.publish({ type: "acp.thought_delta", traceId, delta: String(params.delta ?? "") });
        break;
      case "tool_call": {
        const call = params.call as Record<string, unknown> | undefined;
        if (!call) break;
        session.sink.publish({
          type: "acp.tool_call",
          traceId,
          call: toToolCall(call),
        });
        break;
      }
      case "tool_call_update": {
        const call = params.call as Record<string, unknown> | undefined;
        if (!call) break;
        session.sink.publish({
          type: "acp.tool_call_update",
          traceId,
          callId: String(call.id ?? ""),
          status: toolStatusFrom(call.status as string | undefined),
          summary: call.summary ? String(call.summary) : undefined,
        });
        break;
      }
      case "plan": {
        const steps = parsePlanSteps(params.steps);
        if (steps.length > 0) {
          session.sink.publish({ type: "acp.plan", traceId, steps });
        }
        break;
      }
      case "session_state": {
        const state = parseSessionState(params.state);
        session.sink.publish({ type: "acp.session_state", traceId, conversationId: session.conversationId, state });
        break;
      }
      case "turn_end": {
        session.sink.publish({ type: "acp.turn_end", traceId, ok: Boolean(params.ok ?? true), error: params.error ? String(params.error) : undefined });
        break;
      }
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

function toToolCall(call: Record<string, unknown>): AcpToolCall {
  return {
    id: String(call.id ?? ""),
    title: String(call.title ?? call.name ?? ""),
    kind: toolKindFrom(call.kind as string | undefined),
    status: toolStatusFrom(call.status as string | undefined),
    summary: call.summary ? String(call.summary) : "",
  };
}

function parsePlanSteps(steps: unknown): readonly AcpPlanStep[] {
  if (!Array.isArray(steps)) return [];
  const result: AcpPlanStep[] = [];
  for (const step of steps) {
    if (typeof step !== "object" || step === null) continue;
    const s = step as Record<string, unknown>;
    const id = String(s.id ?? "");
    const text = String(s.text ?? s.description ?? "");
    if (!id || !text) continue;
    result.push({ id, text, done: Boolean(s.done ?? s.completed ?? false) });
  }
  return result;
}

function parsePermissionRequest(params: Record<string, unknown>): AcpPermissionRequest {
  const options: AcpPermissionOption[] = [];
  const rawOptions = Array.isArray(params.options) ? params.options : [];
  for (const opt of rawOptions) {
    if (typeof opt !== "object" || opt === null) continue;
    const o = opt as Record<string, unknown>;
    const optionId = String(o.id ?? o.optionId ?? "");
    const name = String(o.name ?? o.label ?? optionId);
    if (!optionId) continue;
    options.push({ optionId, name, kind: (o.kind as AcpPermissionOption["kind"] | undefined) ?? undefined });
  }
  return {
    requestId: String(params.requestId ?? ""),
    toolTitle: String(params.toolTitle ?? params.title ?? ""),
    detail: params.detail ? String(params.detail) : undefined,
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

function parseSessionState(state: unknown): "idle" | "starting" | "running" | "error" | "cancelled" {
  switch (state) {
    case "idle":
    case "starting":
    case "running":
    case "error":
    case "cancelled":
      return state;
    default:
      return "running";
  }
}
