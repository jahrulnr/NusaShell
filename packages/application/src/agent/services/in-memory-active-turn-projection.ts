import type {
  ActiveTurnOpenTool,
  ActiveTurnProjectionPort,
  ActiveTurnSnapshot,
  ActiveTurnStartInput,
  ActiveTurnStreaming,
} from "../ports/active-turn-projection.port.js";
import type { AgentToolCall } from "../ports/agent-provider.port.js";
import type { AgentToolExecution, AgentTurnStep } from "./agent-turn-types.js";

/**
 * Process-local active-turn SoT. Electron hosts the backend in-process with
 * the main process, so a chat switch does not need disk — only a projection
 * that survives renderer `renderThread()` wiping the pending DOM.
 */
export class InMemoryActiveTurnProjection implements ActiveTurnProjectionPort {
  private readonly byConversation = new Map<string, ActiveTurnSnapshot>();
  private readonly traceToConversation = new Map<string, string>();

  start(input: ActiveTurnStartInput): void {
    const previous = this.byConversation.get(input.conversationId);
    if (previous) this.traceToConversation.delete(previous.traceId);
    const snap: ActiveTurnSnapshot = {
      conversationId: input.conversationId,
      traceId: input.traceId,
      ...(input.messageId ? { messageId: input.messageId } : {}),
      ...(input.messagePosition !== undefined ? { messagePosition: input.messagePosition } : {}),
      status: "running",
      steps: [],
      openTools: [],
      updatedAt: nowIso(),
      ...(input.model ? { model: input.model } : {}),
      ...(input.providerId ? { providerId: input.providerId } : {}),
    };
    this.byConversation.set(input.conversationId, snap);
    this.traceToConversation.set(input.traceId, input.conversationId);
  }

  setSteps(
    conversationId: string,
    steps: readonly AgentTurnStep[],
    meta?: { model?: string; providerId?: string },
  ): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    const { streaming: _streaming, ...rest } = current;
    this.byConversation.set(conversationId, {
      ...rest,
      steps: [...steps],
      // Sealed steps absorb the previous open segment.
      openTools: [],
      updatedAt: nowIso(),
      ...(meta?.model ? { model: meta.model } : {}),
      ...(meta?.providerId ? { providerId: meta.providerId } : {}),
    });
  }

  setStreaming(conversationId: string, streaming: ActiveTurnStreaming | null): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    const { streaming: _streaming, ...rest } = current;
    this.byConversation.set(conversationId, {
      ...rest,
      ...(streaming ? { streaming } : {}),
      updatedAt: nowIso(),
    });
  }

  openTool(conversationId: string, call: AgentToolCall): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    const next: ActiveTurnOpenTool = {
      id: call.id,
      name: call.name,
      status: "running",
      ...(call.args ? { args: call.args } : {}),
    };
    const openTools = [...current.openTools.filter((t) => t.id !== call.id), next];
    const { streaming: _streaming, ...rest } = current;
    this.byConversation.set(conversationId, {
      ...rest,
      openTools,
      // A tool call closes the streaming text/reasoning segment in the UI.
      updatedAt: nowIso(),
    });
  }

  endTool(conversationId: string, execution: AgentToolExecution): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    const openTools = current.openTools.map((t) => (
      t.id === execution.id
        ? {
            ...t,
            status: execution.ok ? "ok" as const : "fail" as const,
            ...(execution.error ? { error: execution.error } : {}),
            ...(execution.modelOutput !== undefined
              ? { output: execution.modelOutput }
              : execution.result !== undefined
              ? { output: typeof execution.result === "string" ? execution.result : JSON.stringify(execution.result) }
              : {}),
          }
        : t
    ));
    this.byConversation.set(conversationId, {
      ...current,
      openTools,
      updatedAt: nowIso(),
    });
  }

  markAwaitingInput(conversationId: string): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    this.byConversation.set(conversationId, { ...current, status: "awaiting_input", updatedAt: nowIso() });
  }

  markRunning(conversationId: string): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    this.byConversation.set(conversationId, { ...current, status: "running", updatedAt: nowIso() });
  }

  get(conversationId: string): ActiveTurnSnapshot | undefined {
    return this.byConversation.get(conversationId);
  }

  getByTraceId(traceId: string): ActiveTurnSnapshot | undefined {
    const conversationId = this.traceToConversation.get(traceId);
    return conversationId ? this.byConversation.get(conversationId) : undefined;
  }

  clear(conversationId: string, traceId?: string): void {
    const current = this.byConversation.get(conversationId);
    if (!current) return;
    if (traceId && current.traceId !== traceId) return;
    this.byConversation.delete(conversationId);
    this.traceToConversation.delete(current.traceId);
  }
}

function nowIso(): string {
  return new Date().toISOString();
}
