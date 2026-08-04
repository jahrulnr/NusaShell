import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type {
  AgentMessage,
  AgentToolExecution,
  AgentTurnResult,
  AgentTurnStep,
  RunAgentTurnInput,
  AgentTurnRunnerDeps,
} from "./agent-turn-types.js";
import {
  assertTurnActive,
  buildTurnPartial,
  emptyUsage,
  addUsage,
  hasUsage,
  hasTurnProgress,
  estimateMessageTokens,
  normalizeMaxRounds,
  normalizeSoftRecover,
  normalizeConcurrentToolCalls,
  repeatedToolDecision,
  rethrowWithTurnPartial,
} from "./agent-turn-utils.js";
import { ContextCompactor } from "./agent-context-compaction.js";
import { ToolExecutionPolicy } from "./agent-tool-execution-policy.js";

export type {
  RunAgentTurnInput,
  AgentContextUpdate,
  AgentToolExecution,
  AgentTurnStep,
  AgentTurnResult,
  AgentCompactionCheckpoint,
  AgentTurnPartial,
  AgentContextOptions,
  AgentTurnRunnerDeps,
} from "./agent-turn-types.js";

/**
 * Provider-agnostic, bounded agent loop. The MCP gateway is the only path for
 * executing a model-requested tool; providers receive schemas, never clients.
 *
 * Delegates to two focused sub-modules:
 * - `ContextCompactor` — summarizes old messages when context exceeds budget
 * - `ToolExecutionPolicy` — dispatches tool batches with bounded parallelism
 *
 * The facade keeps the public API stable; callers see no change.
 */
export class AgentTurnRunner {
  private readonly defaultMaxToolRounds: number;
  private readonly defaultMaxRepeatedToolCalls: number;
  private readonly softRecoverAttempts: number;
  private readonly maxConcurrentToolCalls: number;
  private readonly compactor: ContextCompactor;
  private readonly toolPolicy: ToolExecutionPolicy;

  constructor(private readonly deps: AgentTurnRunnerDeps) {
    this.defaultMaxToolRounds = normalizeMaxRounds(deps.defaultMaxToolRounds);
    this.defaultMaxRepeatedToolCalls = deps.defaultMaxRepeatedToolCalls ?? 50;
    this.softRecoverAttempts = normalizeSoftRecover(deps.softRecoverAttempts);
    this.maxConcurrentToolCalls = normalizeConcurrentToolCalls(deps.maxConcurrentToolCalls);
    this.compactor = new ContextCompactor(deps.provider, deps.context, deps.compactPrompt, deps.logger);
    this.toolPolicy = new ToolExecutionPolicy(deps.toolGateway, this.maxConcurrentToolCalls, deps.logger);
  }

  async run(input: RunAgentTurnInput): Promise<AgentTurnResult> {
    if (input.messages.length === 0) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "At least one message is required");
    }

    const traceId = input.traceId ?? randomUUID();
    this.deps.toolGateway.beginTurn?.(traceId, {
      ...(input.interactive !== undefined ? { interactive: input.interactive } : {}),
      ...(input.workspace !== undefined ? { workspace: input.workspace } : {}),
    });
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
    const compacted = await this.compactor.compact(input, traceId);
    const messages: AgentMessage[] = [...compacted.messages];
    const toolCalls: AgentToolExecution[] = [];
    const repeatedCalls = new Map<string, number>();
    const usage = emptyUsage();
    let model: string | undefined;
    let providerId: string | undefined;
    let api: "chat" | "responses" | "messages" | undefined;
    let reasoning: string | undefined;
    const steps: AgentTurnStep[] = [];
    let emptyResponseNudged = false;
    let softRecoverUsed = 0;

    this.deps.logger?.info("Agent turn started traceId=%s provider=%s", traceId, this.deps.provider.id);
    const publishContext = () => {
      input.onContextUpdate?.({
        estimatedTokens: estimateMessageTokens(messages),
        ...(hasUsage(usage) ? { usage: { ...usage } } : {}),
      });
    };
    publishContext();

    // Tracks the in-flight provider/tool round so mid-turn failures (allowlist,
    // listTools, 4xx/5xx after soft recover, etc.) can attach a resume snapshot.
    let activeRound = 0;
    try {
      for (let round = 1; round <= maxToolRounds; round += 1) {
        activeRound = round;
        assertTurnActive(input.signal, traceId);
        const tools = await this.deps.toolGateway.listTools(input.pluginIds, traceId);
        assertTurnActive(input.signal, traceId);
        const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));
        // Pre-sample gate (Codex pre_sampling): re-estimate before each
        // provider.complete and shrink if token budget exceeded. This catches
        // growth from prior tool rounds without waiting for the next turn.
        this.compactor.shrink(messages, input.modelCapabilities, input.model);
        let response;
        for (;;) {
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
              ...(input.onReasoningDelta ? { onReasoningDelta: input.onReasoningDelta } : {}),
            });
            break;
          } catch (error) {
            if (input.signal?.aborted) {
              throw new ApplicationError("AGENT_TURN_CANCELLED", "Agent turn cancelled", { traceId });
            }
            // Soft recover covers transient provider failures (incl. exhausted
            // HTTP 5xx / retriable 4xx budgets) when tool work already exists.
            if (softRecoverUsed < this.softRecoverAttempts && hasTurnProgress(toolCalls, steps, messages)) {
              softRecoverUsed += 1;
              this.deps.logger?.warn(
                "Agent soft recover %d/%d traceId=%s provider=%s round=%d",
                softRecoverUsed, this.softRecoverAttempts, traceId, this.deps.provider.id, round,
              );
              continue;
            }
            const cause = error instanceof Error ? error.message : String(error);
            this.deps.logger?.error("Agent provider failed traceId=%s provider=%s error=%s", traceId, this.deps.provider.id, cause);
            const details: Record<string, unknown> = {
              providerId: this.deps.provider.id,
              traceId,
              cause,
            };
            if (hasTurnProgress(toolCalls, steps, messages)) {
              details.partial = buildTurnPartial(
                traceId, round - 1, toolCalls, steps, messages,
                model, providerId, api, reasoning, usage,
              );
            }
            throw new ApplicationError("AGENT_PROVIDER_FAILED", `AI provider request failed: ${cause}`, details);
          }
        }
        model = response.model ?? model;
        providerId = response.providerId ?? providerId;
        api = response.api ?? api;
        reasoning = response.reasoning ?? reasoning;
        const stepModel = response.model;
        const stepProviderId = response.providerId;
        if (response.reasoning?.trim()) {
          steps.push({ type: "reasoning", content: response.reasoning.trim(), ...(stepModel ? { model: stepModel } : {}), ...(stepProviderId ? { providerId: stepProviderId } : {}) });
          input.onStepsChanged?.(steps);
        }
        addUsage(usage, response.usage);
        const requestedCalls = response.toolCalls ?? [];
        publishContext();

        if (requestedCalls.length === 0) {
          let text = response.text?.trim();
          if (!text) {
            this.deps.logger?.warn("Agent provider returned an empty response traceId=%s provider=%s round=%d", traceId, this.deps.provider.id, round);
            if (!emptyResponseNudged && round < maxToolRounds) {
              emptyResponseNudged = true;
              this.deps.logger?.info("Agent nudged: empty response, requesting text or tool call traceId=%s round=%d", traceId, round);
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
          steps.push({ type: "text", content: text, ...(stepModel ? { model: stepModel } : {}), ...(stepProviderId ? { providerId: stepProviderId } : {}) });
          input.onStepsChanged?.(steps);
          return {
            traceId,
            text,
            rounds: round,
            toolCalls,
            steps,
            messages,
            ...(model ? { model } : {}),
            ...(providerId ? { providerId } : {}),
            ...(api ? { api } : {}),
            ...(reasoning ? { reasoning } : {}),
            ...(hasUsage(usage) ? { usage } : {}),
            ...(compacted.checkpoint ? { compaction: compacted.checkpoint } : {}),
          };
        }

        const duplicate = repeatedToolDecision(requestedCalls, repeatedCalls, this.defaultMaxRepeatedToolCalls);
        if (duplicate === "stop") {
          this.deps.logger?.warn("Agent stopped: repeated tool call limit (%d) reached traceId=%s", this.defaultMaxRepeatedToolCalls, traceId);
          return {
            traceId,
            text: `The agent stopped because the model repeated the same tool call ${this.defaultMaxRepeatedToolCalls} times.`,
            rounds: round,
            toolCalls,
            steps,
            messages,
            ...(model ? { model } : {}),
            ...(providerId ? { providerId } : {}),
            ...(api ? { api } : {}),
            ...(reasoning ? { reasoning } : {}),
            ...(hasUsage(usage) ? { usage } : {}),
            ...(compacted.checkpoint ? { compaction: compacted.checkpoint } : {}),
          };
        }
        if (duplicate === "nudge") {
          this.deps.logger?.info("Agent nudged: repeated tool call detected traceId=%s", traceId);
          messages.push(
            { role: "assistant", ...(response.text ? { content: response.text } : {}), ...(response.reasoning?.trim() ? { reasoning: response.reasoning.trim() } : {}), toolCalls: requestedCalls },
            {
              role: "system",
              content: "You are repeating the same tool call with identical arguments. Use the previous tool result, change the arguments, or answer the user without repeating it.",
            },
          );
          continue;
        }
        messages.push({ role: "assistant", ...(response.text ? { content: response.text } : {}), ...(response.reasoning?.trim() ? { reasoning: response.reasoning.trim() } : {}), toolCalls: requestedCalls });
        // Keep provider order for the round: reasoning (already pushed) → text → tools.
        // Streaming UIs also append by delta arrival; do not reorder text after tools.
        if (response.text?.trim()) {
          steps.push({ type: "text", content: response.text.trim(), ...(stepModel ? { model: stepModel } : {}), ...(stepProviderId ? { providerId: stepProviderId } : {}) });
          input.onStepsChanged?.(steps);
        }
        publishContext();

        const roundExecutions: AgentToolExecution[] = [];
        await this.toolPolicy.executeBatch(requestedCalls, {
          traceId,
          round,
          toolsByName,
          ...(input.signal ? { signal: input.signal } : {}),
          ...(input.onToolCallStart ? { onToolCallStart: input.onToolCallStart } : {}),
          ...(input.onToolCallEnd ? { onToolCallEnd: input.onToolCallEnd } : {}),
        }, toolCalls, roundExecutions, messages);
        // MidTurn gate (Codex post_sampling roll-over): after tool results are
        // appended, shrink if the live messages array exceeds the token budget
        // so the next provider.complete payload stays under the window.
        this.compactor.shrink(messages, input.modelCapabilities, input.model);
        publishContext();
        if (roundExecutions.length > 0) {
          steps.push({ type: "tool_calls", calls: [...roundExecutions], ...(stepModel ? { model: stepModel } : {}), ...(stepProviderId ? { providerId: stepProviderId } : {}) });
          input.onStepsChanged?.(steps);
        }
      }

      this.deps.logger?.warn("Agent turn reached tool-round limit traceId=%s provider=%s limit=%d", traceId, this.deps.provider.id, maxToolRounds);
      return {
        traceId,
        text: "The agent reached the maximum tool rounds before producing a final answer.",
        rounds: maxToolRounds,
        toolCalls,
        steps,
        messages,
        ...(model ? { model } : {}),
        ...(providerId ? { providerId } : {}),
        ...(api ? { api } : {}),
        ...(reasoning ? { reasoning } : {}),
        ...(hasUsage(usage) ? { usage } : {}),
        ...(compacted.checkpoint ? { compaction: compacted.checkpoint } : {}),
      };
    } catch (error) {
      // Provider soft-recover exhaustion already attaches partial. This catch
      // covers allowlist rejection, listTools failures, user cancel after tool
      // progress, and other mid-turn ApplicationErrors so Retry can resume
      // instead of restarting from scratch.
      if (!hasTurnProgress(toolCalls, steps, messages)) throw error;
      rethrowWithTurnPartial(
        error,
        buildTurnPartial(
          traceId,
          Math.max(0, activeRound - 1),
          toolCalls,
          steps,
          messages,
          model,
          providerId,
          api,
          reasoning,
          usage,
        ),
      );
    }
  }
}
