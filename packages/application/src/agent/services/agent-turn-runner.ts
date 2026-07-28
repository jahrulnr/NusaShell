import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type {
  AgentMessage,
  AgentModelCapabilities,
  AgentProvider,
  AgentTokenUsage,
  AgentToolCall,
  ReasoningEffort,
} from "../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../ports/agent-tool-gateway.port.js";

export interface RunAgentTurnInput {
  readonly messages: readonly AgentMessage[];
  readonly pluginIds: readonly string[];
  readonly maxToolRounds?: number;
  readonly model?: string;
  readonly effort?: ReasoningEffort;
  readonly modelCapabilities?: AgentModelCapabilities;
  readonly traceId?: string;
  readonly signal?: AbortSignal;
  readonly onTextDelta?: (delta: string) => void;
}

export interface AgentToolExecution {
  readonly id: string;
  readonly name: string;
  readonly ok: boolean;
  readonly result?: unknown;
  readonly error?: string;
}

export interface AgentTurnResult {
  readonly traceId: string;
  readonly text: string;
  readonly rounds: number;
  readonly toolCalls: readonly AgentToolExecution[];
  readonly model?: string;
  readonly providerId?: string;
  readonly api?: "chat" | "responses" | "messages";
  readonly reasoning?: string;
  readonly usage?: AgentTokenUsage;
  readonly compaction?: AgentCompactionCheckpoint;
}

export interface AgentCompactionCheckpoint {
  readonly summary: string;
  readonly compactedMessageCount: number;
  readonly estimatedInputTokens: number;
  readonly via: "provider" | "extractive";
}

export interface AgentContextOptions {
  readonly compactionEnabled: boolean;
  readonly maxInputTokens: number;
  readonly reserveTokens: number;
  readonly recentTurns: number;
  readonly summaryMaxChars: number;
}

export interface AgentTurnRunnerDeps {
  readonly provider: AgentProvider;
  readonly toolGateway: AgentToolGateway;
  readonly logger?: LoggerPort;
  readonly defaultMaxToolRounds?: number;
  readonly context?: AgentContextOptions;
}

const DEFAULT_MAX_TOOL_ROUNDS = 8;

/**
 * Provider-agnostic, bounded agent loop. The MCP gateway is the only path for
 * executing a model-requested tool; providers receive schemas, never clients.
 */
export class AgentTurnRunner {
  private readonly defaultMaxToolRounds: number;

  constructor(private readonly deps: AgentTurnRunnerDeps) {
    this.defaultMaxToolRounds = normalizeMaxRounds(deps.defaultMaxToolRounds);
  }

  async run(input: RunAgentTurnInput): Promise<AgentTurnResult> {
    if (input.messages.length === 0) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "At least one message is required");
    }

    const traceId = input.traceId ?? randomUUID();
    this.deps.toolGateway.beginTurn?.(traceId);
    const cancelTools = () => {
      void this.deps.toolGateway.cancelTurn?.(traceId);
    };
    if (input.signal?.aborted) cancelTools();
    else input.signal?.addEventListener("abort", cancelTools, { once: true });
    try {
      return await this.runSession(input, traceId);
    } finally {
      input.signal?.removeEventListener("abort", cancelTools);
      this.deps.toolGateway.endTurn?.(traceId);
    }
  }

  private async runSession(input: RunAgentTurnInput, traceId: string): Promise<AgentTurnResult> {
    assertTurnActive(input.signal, traceId);
    const maxToolRounds = normalizeMaxRounds(input.maxToolRounds ?? this.defaultMaxToolRounds);
    const compacted = await this.compactMessages(input, traceId);
    const messages: AgentMessage[] = [...compacted.messages];
    const toolCalls: AgentToolExecution[] = [];
    const repeatedCalls = new Map<string, number>();
    const usage = emptyUsage();
    let model: string | undefined;
    let providerId: string | undefined;
    let api: "chat" | "responses" | "messages" | undefined;
    let reasoning: string | undefined;
    let emptyResponseNudged = false;

    this.deps.logger?.info("Agent turn started traceId=%s provider=%s", traceId, this.deps.provider.id);

    for (let round = 1; round <= maxToolRounds; round += 1) {
      assertTurnActive(input.signal, traceId);
      const tools = await this.deps.toolGateway.listTools(input.pluginIds, traceId);
      assertTurnActive(input.signal, traceId);
      const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));
      let response;
      try {
        response = await this.deps.provider.complete({
          traceId,
          round,
          messages,
          tools,
          ...(input.model ? { model: input.model } : {}),
          ...(input.effort ? { effort: input.effort } : {}),
          ...(input.modelCapabilities ? { modelCapabilities: input.modelCapabilities } : {}),
          ...(input.signal ? { signal: input.signal } : {}),
          ...(input.onTextDelta ? { onTextDelta: input.onTextDelta } : {}),
        });
      } catch (error) {
        if (input.signal?.aborted) {
          throw new ApplicationError("AGENT_TURN_CANCELLED", "Agent turn cancelled", { traceId });
        }
        this.deps.logger?.error("Agent provider failed traceId=%s provider=%s", traceId, this.deps.provider.id);
        throw new ApplicationError("AGENT_PROVIDER_FAILED", "AI provider request failed", {
          providerId: this.deps.provider.id,
          traceId,
          cause: error instanceof Error ? error.message : String(error),
        });
      }
      model = response.model ?? model;
      providerId = response.providerId ?? providerId;
      api = response.api ?? api;
      reasoning = response.reasoning ?? reasoning;
      addUsage(usage, response.usage);
      const requestedCalls = response.toolCalls ?? [];

      if (requestedCalls.length === 0) {
        let text = response.text?.trim();
        if (!text) {
          this.deps.logger?.warn("Agent provider returned an empty response traceId=%s provider=%s round=%d", traceId, this.deps.provider.id, round);
          if (!emptyResponseNudged && round < maxToolRounds) {
            emptyResponseNudged = true;
            const reasoningOnly = Boolean(response.reasoning?.trim());
            messages.push(
              { role: "assistant", content: "" },
              {
                role: "system",
                content: reasoningOnly
                  ? "You produced reasoning but no user-facing answer and no tool call. Answer the user now in plain text, or call a tool with concrete arguments."
                  : "You produced no user-facing answer and no tool call. Answer the user now in plain text, or call a tool with concrete arguments.",
              },
            );
            continue;
          }
          text = "(empty model response)";
        }
        this.deps.logger?.info("Agent turn completed traceId=%s provider=%s rounds=%d", traceId, this.deps.provider.id, round);
        return {
          traceId,
          text,
          rounds: round,
          toolCalls,
          ...(model ? { model } : {}),
          ...(providerId ? { providerId } : {}),
          ...(api ? { api } : {}),
          ...(reasoning ? { reasoning } : {}),
          ...(hasUsage(usage) ? { usage } : {}),
          ...(compacted.checkpoint ? { compaction: compacted.checkpoint } : {}),
        };
      }

      validateRequestedTools(requestedCalls, toolsByName, traceId);
      const duplicate = repeatedToolDecision(requestedCalls, repeatedCalls);
      if (duplicate === "stop") {
        return {
          traceId,
          text: "The agent stopped because the model repeated the same tool call three times.",
          rounds: round,
          toolCalls,
          ...(model ? { model } : {}),
          ...(providerId ? { providerId } : {}),
          ...(api ? { api } : {}),
          ...(reasoning ? { reasoning } : {}),
          ...(hasUsage(usage) ? { usage } : {}),
          ...(compacted.checkpoint ? { compaction: compacted.checkpoint } : {}),
        };
      }
      if (duplicate === "nudge") {
        messages.push(
          { role: "assistant", ...(response.text ? { content: response.text } : {}), toolCalls: requestedCalls },
          {
            role: "system",
            content: "You are repeating the same tool call with identical arguments. Use the previous tool result, change the arguments, or answer the user without repeating it.",
          },
        );
        continue;
      }
      messages.push({ role: "assistant", ...(response.text ? { content: response.text } : {}), toolCalls: requestedCalls });

      for (const call of requestedCalls) {
        assertTurnActive(input.signal, traceId);
        const execution = await this.executeTool(call, traceId, round);
        toolCalls.push(execution);
        messages.push({
          role: "tool",
          toolCallId: call.id,
          name: call.name,
          content: serializeToolResult(execution),
        });
      }
    }

    this.deps.logger?.warn("Agent turn reached tool-round limit traceId=%s provider=%s limit=%d", traceId, this.deps.provider.id, maxToolRounds);
    return {
      traceId,
      text: "The agent reached the maximum tool rounds before producing a final answer.",
      rounds: maxToolRounds,
      toolCalls,
      ...(model ? { model } : {}),
      ...(providerId ? { providerId } : {}),
      ...(api ? { api } : {}),
      ...(reasoning ? { reasoning } : {}),
      ...(hasUsage(usage) ? { usage } : {}),
      ...(compacted.checkpoint ? { compaction: compacted.checkpoint } : {}),
    };
  }

  private async executeTool(call: AgentToolCall, traceId: string, round: number): Promise<AgentToolExecution> {
    this.deps.logger?.info("Agent MCP tool started traceId=%s tool=%s round=%d", traceId, call.name, round);
    try {
      const result = await this.deps.toolGateway.execute(call.name, call.args, call.id, traceId);
      this.deps.logger?.info("Agent MCP tool completed traceId=%s tool=%s round=%d", traceId, call.name, round);
      return { id: call.id, name: call.name, ok: true, result };
    } catch (error) {
      const message = error instanceof Error ? error.message : "Tool execution failed";
      this.deps.logger?.warn("Agent MCP tool failed traceId=%s tool=%s round=%d", traceId, call.name, round);
      return { id: call.id, name: call.name, ok: false, error: message };
    }
  }

  private async compactMessages(
    input: RunAgentTurnInput,
    traceId: string,
  ): Promise<{ messages: readonly AgentMessage[]; checkpoint?: AgentCompactionCheckpoint }> {
    const options = this.deps.context;
    if (!options?.compactionEnabled) return { messages: input.messages };
    const estimatedInputTokens = estimateMessageTokens(input.messages);
    const modelWindow = positiveInteger(input.modelCapabilities?.contextWindow)
      ? input.modelCapabilities.contextWindow
      : options.maxInputTokens;
    const maxInputTokens = Math.min(options.maxInputTokens, modelWindow);
    const wantedReserve = Math.max(options.reserveTokens, input.modelCapabilities?.maxOutput ?? 0);
    const reserveTokens = Math.min(Math.max(0, maxInputTokens - 1000), wantedReserve);
    const threshold = Math.max(1000, maxInputTokens - reserveTokens);
    if (estimatedInputTokens <= threshold) return { messages: input.messages };

    const userIndexes = input.messages.flatMap((message, index) => message.role === "user" ? [index] : []);
    const recentTurns = Math.max(1, options.recentTurns);
    if (userIndexes.length <= recentTurns) return { messages: input.messages };
    const keepFrom = userIndexes[userIndexes.length - recentTurns] ?? 0;
    const oldMessages = input.messages.slice(0, keepFrom);
    const recentMessages = input.messages.slice(keepFrom);
    if (oldMessages.length === 0) return { messages: input.messages };

    const excerpt = clampText(formatMessagesForSummary(oldMessages), options.summaryMaxChars);
    let summary = excerpt;
    let via: AgentCompactionCheckpoint["via"] = "extractive";
    try {
      const response = await this.deps.provider.complete({
        traceId,
        round: 0,
        messages: [
          {
            role: "system",
            content: "Create a concise context checkpoint for another AI. Preserve goals, decisions, constraints, important tool results, and unfinished work. Reply with the checkpoint only.",
          },
          { role: "user", content: excerpt },
        ],
        tools: [],
        ...(input.model ? { model: input.model } : {}),
        ...(input.effort ? { effort: input.effort } : {}),
        ...(input.modelCapabilities ? { modelCapabilities: input.modelCapabilities } : {}),
        ...(input.signal ? { signal: input.signal } : {}),
      });
      if (response.text?.trim()) {
        summary = clampText(response.text, options.summaryMaxChars);
        via = "provider";
      }
    } catch {
      this.deps.logger?.warn("Agent context compaction used extractive fallback traceId=%s", traceId);
    }

    const checkpoint: AgentCompactionCheckpoint = {
      summary,
      compactedMessageCount: oldMessages.length,
      estimatedInputTokens,
      via,
    };
    return {
      messages: [{ role: "system", content: `Conversation summary:\n${summary}` }, ...recentMessages],
      checkpoint,
    };
  }
}

function assertTurnActive(signal: AbortSignal | undefined, traceId: string): void {
  if (signal?.aborted) {
    throw new ApplicationError("AGENT_TURN_CANCELLED", "Agent turn cancelled", { traceId });
  }
}

function repeatedToolDecision(
  calls: readonly AgentToolCall[],
  counts: Map<string, number>,
): "execute" | "nudge" | "stop" {
  let decision: "execute" | "nudge" | "stop" = "execute";
  for (const call of calls) {
    const fingerprint = `${call.name}:${stableJson(call.args)}`;
    const count = (counts.get(fingerprint) ?? 0) + 1;
    counts.set(fingerprint, count);
    if (count >= 3) return "stop";
    if (count === 2) decision = "nudge";
  }
  return decision;
}

function stableJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(",")}]`;
  if (typeof value === "object" && value !== null) {
    return `{${Object.entries(value).sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => `${JSON.stringify(key)}:${stableJson(item)}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

function emptyUsage(): AgentTokenUsage {
  return { inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, cacheWriteTokens: 0, reasoningOutputTokens: 0 };
}

function addUsage(target: Mutable<AgentTokenUsage>, value: AgentTokenUsage | undefined): void {
  if (!value) return;
  target.inputTokens += value.inputTokens;
  target.outputTokens += value.outputTokens;
  target.cachedInputTokens += value.cachedInputTokens;
  target.cacheWriteTokens += value.cacheWriteTokens;
  target.reasoningOutputTokens += value.reasoningOutputTokens;
}

function hasUsage(value: AgentTokenUsage): boolean {
  return Object.values(value).some((tokens) => tokens > 0);
}

type Mutable<T> = { -readonly [Key in keyof T]: T[Key] };

function validateRequestedTools(
  calls: readonly AgentToolCall[],
  toolsByName: ReadonlyMap<string, unknown>,
  traceId: string,
): void {
  for (const call of calls) {
    if (!call.id || !call.name || !toolsByName.has(call.name)) {
      throw new ApplicationError("AGENT_TOOL_NOT_ALLOWED", "AI provider requested a tool outside the MCP allowlist", {
        traceId,
        toolName: call.name,
      });
    }
  }
}

function serializeToolResult(execution: AgentToolExecution): string {
  return JSON.stringify(execution.ok
    ? { ok: true, result: execution.result }
    : { ok: false, error: execution.error });
}

function normalizeMaxRounds(value: number | undefined): number {
  if (value === undefined) return DEFAULT_MAX_TOOL_ROUNDS;
  if (!Number.isInteger(value) || value < 1 || value > 32) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "maxToolRounds must be an integer between 1 and 32");
  }
  return value;
}

function estimateMessageTokens(messages: readonly AgentMessage[]): number {
  let chars = 0;
  for (const message of messages) {
    if ("content" in message) {
      chars += typeof message.content === "string"
        ? message.content.length
        : JSON.stringify(message.content).length;
    }
    if (message.role === "assistant" && message.toolCalls) chars += JSON.stringify(message.toolCalls).length;
  }
  return Math.ceil(chars / 4);
}

function formatMessagesForSummary(messages: readonly AgentMessage[]): string {
  return messages.map((message) => {
    if (message.role === "tool") return `Tool ${message.name}: ${clampText(message.content, 800)}`;
    if (message.role === "assistant") {
      const calls = message.toolCalls?.map((call) => call.name).join(", ");
      return `Assistant: ${message.content ?? ""}${calls ? `\nTool calls: ${calls}` : ""}`.trim();
    }
    const content = typeof message.content === "string"
      ? message.content
      : message.content.map((part) => part.type === "text" ? part.text : `[${part.type}: ${part.name ?? "attachment"}]`).join("\n");
    return `${message.role === "user" ? "User" : "System"}: ${content}`;
  }).join("\n");
}

function positiveInteger(value: number | undefined): value is number {
  return Number.isInteger(value) && (value ?? 0) > 0;
}

function clampText(value: string, maxChars: number): string {
  const trimmed = value.trim();
  const max = Math.max(100, maxChars);
  return trimmed.length <= max ? trimmed : trimmed.slice(0, max);
}
