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
  runPool,
  segmentToolBatch,
  serializeToolResult,
} from "./agent-turn-utils.js";

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

  private async executeTool(call: AgentToolCall, traceId: string, round: number): Promise<AgentToolExecution> {
    this.logger?.info("Agent MCP tool started traceId=%s tool=%s round=%d", traceId, call.name, round);
    const requestId = randomUUID();
    try {
      const result = await this.toolGateway.execute(call.name, call.args, requestId, traceId, call.id);
      this.logger?.info("Agent MCP tool completed traceId=%s tool=%s round=%d", traceId, call.name, round);
      return { id: call.id, name: call.name, ok: true, args: call.args, result };
    } catch (error) {
      const message = error instanceof Error ? error.message : "Tool execution failed";
      this.logger?.warn("Agent MCP tool failed traceId=%s tool=%s round=%d", traceId, call.name, round);
      return { id: call.id, name: call.name, ok: false, args: call.args, error: message };
    }
  }

  private async runOneTool(
    call: AgentToolCall,
    ctx: { readonly traceId: string; readonly round: number; readonly signal?: AbortSignal;
      readonly onToolCallStart?: (call: AgentToolCall) => void; readonly onToolCallEnd?: (execution: AgentToolExecution) => void; },
  ): Promise<AgentToolExecution> {
    assertTurnActive(ctx.signal, ctx.traceId);
    ctx.onToolCallStart?.(call);
    const execution = await this.executeTool(call, ctx.traceId, ctx.round);
    ctx.onToolCallEnd?.(execution);
    return execution;
  }

  private async runParallelSegment(
    calls: readonly AgentToolCall[],
    ctx: { readonly traceId: string; readonly round: number; readonly signal?: AbortSignal;
      readonly onToolCallStart?: (call: AgentToolCall) => void; readonly onToolCallEnd?: (execution: AgentToolExecution) => void; },
  ): Promise<(AgentToolExecution | undefined)[]> {
    for (const call of calls) {
      ctx.onToolCallStart?.(call);
    }
    const results = await runPool(calls, this.maxConcurrentToolCalls, async (call, index) => {
      try {
        assertTurnActive(ctx.signal, ctx.traceId);
      } catch {
        return { index, execution: cancelledExecution(call) };
      }
      const execution = await this.executeTool(call, ctx.traceId, ctx.round);
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
        const stub = cancelledExecution(call);
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
    toolCalls.push(execution);
    roundExecutions.push(execution);
    messages.push({
      role: "tool",
      toolCallId: call.id,
      name: call.name,
      content: serializeToolResult(execution, call.name),
    });
  }
}
