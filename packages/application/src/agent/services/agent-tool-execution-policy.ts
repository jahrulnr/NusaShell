import { randomUUID } from "node:crypto";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type {
  AgentMessage,
  AgentToolCall,
  AgentToolExecution,
  AgentToolGateway,
} from "./agent-turn-types.js";
import {
  assertTurnActive,
  cancelledExecution,
  isLazyResolvableMcpToolName,
  isToolAllowed,
  runPool,
  segmentToolBatch,
  serializeToolResult,
  unknownToolExecution,
} from "./agent-turn-utils.js";
import {
  errorToolResult,
  fromIngestedMcp,
  successToolResult,
  fromThrownError,
  ingestMcpToolResult,
  projectModelToolResult,
  type McpRawResult,
} from "./agent-tool-result.js";

function isMcpRawResult(value: unknown): value is McpRawResult {
  if (!value || typeof value !== "object") return false;
  const raw = value as Record<string, unknown>;
  return Array.isArray(raw.content)
    || typeof raw.isError === "boolean"
    || Object.prototype.hasOwnProperty.call(raw, "structuredContent");
}

/**
 * Tool execution policy — dispatches a round's tool-call batch with
 * segmentation and bounded parallelism.
 *
 * - Barrier tools (e.g. `ask_question`) run alone, in order.
 * - Contiguous non-barrier calls form a parallel segment executed via a
 *   bounded pool (`maxConcurrentToolCalls`). Same-plugin calls naturally
 *   serialize through `PluginOperationQueue` inside the gateway.
 * - Every `tool_call_id` in the batch gets a tool result message (success,
 *   failure, or cancelled) — siblings are never dropped.
 * - On cancel mid-batch: in-flight calls drain via `cancelTurn`; any slot
 *   without an execution is filled with a cancelled stub. Results are
 *   recorded into `messages`/`toolCalls`/`roundExecutions` in call order
 *   before the caller throws `AGENT_TURN_CANCELLED`.
 */
export class ToolExecutionPolicy {
  constructor(
    private readonly toolGateway: AgentToolGateway,
    private readonly maxConcurrentToolCalls: number,
    private readonly logger?: LoggerPort,
  ) {}

  async executeBatch(
    requestedCalls: readonly AgentToolCall[],
    ctx: {
      readonly traceId: string;
      readonly round: number;
      readonly signal?: AbortSignal;
      readonly toolsByName: ReadonlyMap<string, unknown>;
      readonly onToolCallStart?: (call: AgentToolCall) => void;
      readonly onToolCallEnd?: (execution: AgentToolExecution) => void;
    },
    toolCalls: AgentToolExecution[],
    roundExecutions: AgentToolExecution[],
    messages: AgentMessage[],
  ): Promise<void> {
    const segments = segmentToolBatch(requestedCalls);
    for (const segment of segments) {
      if (segment.kind === "barrier") {
        const call = segment.calls[0];
        if (!call) continue;
        const execution = await this.runOneTool(call, ctx);
        this.recordExecution(execution, call, toolCalls, roundExecutions, messages);
      } else {
        const executions = await this.runParallelSegment(segment.calls, ctx);
        for (let i = 0; i < segment.calls.length; i++) {
          const call = segment.calls[i];
          if (!call) continue;
          const execution = executions[i] ?? cancelledExecution(call);
          this.recordExecution(execution, call, toolCalls, roundExecutions, messages);
        }
      }
    }
  }

  private async executeTool(
    call: AgentToolCall,
    toolsByName: ReadonlyMap<string, unknown>,
    traceId: string,
    round: number,
    signal?: AbortSignal,
  ): Promise<AgentToolExecution> {
    if (call.argumentError) {
      this.logger?.warn("Agent tool call arguments rejected traceId=%s tool=%s round=%d code=%s", traceId, call.name, round, call.argumentError.code);
      const toolResult = errorToolResult(
        call.id,
        call.name,
        call.argumentError.code,
        call.argumentError.message,
        true,
      );
      return { id: call.id, name: call.name, ok: false, args: call.args, error: call.argumentError.message, toolResult };
    }
    if (!isToolAllowed(call, toolsByName) && !isLazyResolvableMcpToolName(call.name)) {
      this.logger?.warn("Agent MCP tool soft-rejected (unknown) traceId=%s tool=%s round=%d", traceId, call.name || "(missing)", round);
      return unknownToolExecution(call, toolsByName);
    }
    this.logger?.info("Agent MCP tool started traceId=%s tool=%s round=%d", traceId, call.name, round);
    const requestId = randomUUID();
    try {
      const result = await this.toolGateway.execute(call.name, call.args, requestId, traceId, call.id, signal ? { signal } : undefined);
      this.logger?.info("Agent MCP tool completed traceId=%s tool=%s round=%d", traceId, call.name, round);
      // Plugin MCP calls return the protocol's CallToolResult shape. Ingest
      // its text content before projection so terminal output is not treated
      // as a generic object and rendered again as `content[1] type text`.
      const toolResult = isMcpRawResult(result)
        ? fromIngestedMcp(call.id, call.name, ingestMcpToolResult(result))
        : successToolResult(call.id, call.name, result);
      return { id: call.id, name: call.name, ok: true, args: call.args, result, toolResult };
    } catch (error) {
      const message = error instanceof Error ? error.message : "Tool execution failed";
      this.logger?.warn("Agent MCP tool failed traceId=%s tool=%s round=%d", traceId, call.name, round);
      const toolResult = fromThrownError(call, error);
      return { id: call.id, name: call.name, ok: false, args: call.args, error: message, toolResult };
    }
  }

  private async runOneTool(
    call: AgentToolCall,
    ctx: { readonly traceId: string; readonly round: number; readonly signal?: AbortSignal;
      readonly toolsByName: ReadonlyMap<string, unknown>;
      readonly onToolCallStart?: (call: AgentToolCall) => void; readonly onToolCallEnd?: (execution: AgentToolExecution) => void; },
  ): Promise<AgentToolExecution> {
    assertTurnActive(ctx.signal, ctx.traceId);
    ctx.onToolCallStart?.(call);
    const execution = this.withModelOutput(
      await this.executeTool(call, ctx.toolsByName, ctx.traceId, ctx.round, ctx.signal),
      call,
    );
    ctx.onToolCallEnd?.(execution);
    return execution;
  }

  private async runParallelSegment(
    calls: readonly AgentToolCall[],
    ctx: { readonly traceId: string; readonly round: number; readonly signal?: AbortSignal;
      readonly toolsByName: ReadonlyMap<string, unknown>;
      readonly onToolCallStart?: (call: AgentToolCall) => void; readonly onToolCallEnd?: (execution: AgentToolExecution) => void; },
  ): Promise<(AgentToolExecution | undefined)[]> {
    for (const call of calls) {
      ctx.onToolCallStart?.(call);
    }
    const results = await runPool(calls, this.maxConcurrentToolCalls, async (call, index) => {
      try {
        assertTurnActive(ctx.signal, ctx.traceId);
      } catch {
        const execution = this.withModelOutput(cancelledExecution(call), call);
        ctx.onToolCallEnd?.(execution);
        return { index, execution };
      }
      const execution = this.withModelOutput(
        await this.executeTool(call, ctx.toolsByName, ctx.traceId, ctx.round, ctx.signal),
        call,
      );
      ctx.onToolCallEnd?.(execution);
      return { index, execution };
    });
    const ordered: (AgentToolExecution | undefined)[] = new Array(calls.length).fill(undefined);
    for (const { index, execution } of results) {
      ordered[index] = execution;
    }
    for (let i = 0; i < ordered.length; i++) {
      if (!ordered[i]) {
        const call = calls[i];
        if (!call) continue;
        const stub = this.withModelOutput(cancelledExecution(call), call);
        ordered[i] = stub;
        ctx.onToolCallEnd?.(stub);
      }
    }
    return ordered;
  }

  private recordExecution(
    execution: AgentToolExecution,
    call: AgentToolCall,
    toolCalls: AgentToolExecution[],
    roundExecutions: AgentToolExecution[],
    messages: AgentMessage[],
  ): void {
    const content = execution.modelOutput ?? (execution.toolResult
      ? projectModelToolResult(execution.toolResult)
      : serializeToolResult(execution, call.name));
    // The final transcript must retain exactly the string that becomes the
    // provider's role:"tool" message; this keeps UI and rehydrated context in
    // lockstep with the live round.
    const persistedExecution = { ...execution, modelOutput: content };
    toolCalls.push(persistedExecution);
    roundExecutions.push(persistedExecution);
    const toolIsError = execution.toolResult
      ? execution.toolResult.status !== "success"
      : !execution.ok;
    messages.push({
      role: "tool",
      toolCallId: call.id,
      name: call.name,
      content,
      ...(toolIsError ? { toolIsError: true } : {}),
    });
  }

  /**
   * Produce the provider-facing representation before live observers run.
   * This keeps event cards, active-turn recovery, persisted history, and the
   * next provider round on one byte-identical output contract.
   */
  private withModelOutput(execution: AgentToolExecution, call: AgentToolCall): AgentToolExecution {
    const modelOutput = execution.modelOutput ?? (execution.toolResult
      ? projectModelToolResult(execution.toolResult)
      : serializeToolResult(execution, call.name));
    return { ...execution, modelOutput };
  }
}
