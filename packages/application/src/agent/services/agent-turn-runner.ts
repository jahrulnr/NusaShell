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
  hasResumableProgress,
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
    /** Latest memento checkpoint (pre-turn and/or mid-turn). */
    let compactionCheckpoint = compacted.checkpoint;
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
    // Live-streamed text/reasoning buffers. Reset when a provider.complete
    // attempt succeeds (full response accepted). On mid-stream failure, these
    // carry already-painted paragraphs into buildTurnPartial so the UI/seal
    // path does not lose them.
    let liveText = "";
    let liveReasoning = "";

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
    const roundsUnlimited = maxToolRounds === 0;
    try {
      for (let round = 1; roundsUnlimited || round <= maxToolRounds; round += 1) {
        activeRound = round;
        assertTurnActive(input.signal, traceId);
        const tools = await this.deps.toolGateway.listTools(input.pluginIds, traceId);
        assertTurnActive(input.signal, traceId);
        const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));
        // Pre-sample gate: light shrink only (trim tool leftovers). Full
        // mid-turn memento runs after tools settle so FC/FCO pairs are never
        // half-open across a compact boundary.
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
              // Always wrap onTextDelta/onReasoningDelta so live-streamed
              // text is captured into liveText/liveReasoning even when the
              // caller did not provide a delta callback. On success the
              // buffers are cleared; on failure they feed buildTurnPartial.
              onTextDelta: (delta: string) => {
                liveText += delta;
                input.onTextDelta?.(delta);
              },
              onReasoningDelta: (delta: string) => {
                liveReasoning += delta;
                input.onReasoningDelta?.(delta);
              },
            });
            // Success: clear live buffers for the next attempt/round.
            liveText = "";
            liveReasoning = "";
            break;
          } catch (error) {
            if (input.signal?.aborted) {
              const cancelDetails: Record<string, unknown> = { traceId };
              if (hasResumableProgress(toolCalls, steps, liveText, liveReasoning)) {
                cancelDetails.partial = buildTurnPartial(
                  traceId, round - 1, toolCalls, steps, messages,
                  model, providerId, api, reasoning, usage,
                  liveText, liveReasoning,
                );
              }
              throw new ApplicationError("AGENT_TURN_CANCELLED", "Agent turn cancelled", cancelDetails);
            }
            // Soft recover covers transient provider failures (incl. exhausted
            // HTTP 5xx / retriable 4xx budgets) when in-turn tool work exists
            // and the failed sample painted no live text/reasoning yet.
            // Pure-text fails and mid-stream cuts after paint do NOT soft-
            // recover (avoids rewrite of already-shown prose); they throw
            // with partial so the UI can offer Continue / Resume.
            const paintedLive = Boolean(liveText.trim()) || Boolean(liveReasoning.trim());
            if (
              softRecoverUsed < this.softRecoverAttempts
              && hasTurnProgress(toolCalls, steps)
              && !paintedLive
            ) {
              softRecoverUsed += 1;
              liveText = "";
              liveReasoning = "";
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
            if (hasResumableProgress(toolCalls, steps, liveText, liveReasoning)) {
              details.partial = buildTurnPartial(
                traceId, round - 1, toolCalls, steps, messages,
                model, providerId, api, reasoning, usage,
                liveText, liveReasoning,
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
            if (!emptyResponseNudged && (roundsUnlimited || round < maxToolRounds)) {
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
            ...(compactionCheckpoint ? { compaction: compactionCheckpoint } : {}),
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
            ...(compactionCheckpoint ? { compaction: compactionCheckpoint } : {}),
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
        // MidTurn memento (Codex post-tool roll-over): after tool results settle,
        // if still over budget, replace history with users + summary and drop
        // the tool graph. Old tool_call ids are intentionally invalid for the
        // next sample — the model continues from the handoff, not open pairs.
        // Shrink remains a light residual clamp after memento (or when under
        // the budget threshold so compact() no-ops).
        if (this.compactor.isOverBudget(messages, input.modelCapabilities, input.model)) {
          const midTurn = await this.compactor.compact(
            {
              messages,
              pluginIds: input.pluginIds,
              ...(input.model ? { model: input.model } : {}),
              ...(input.effort ? { effort: input.effort } : {}),
              ...(input.modelCapabilities ? { modelCapabilities: input.modelCapabilities } : {}),
              ...(input.signal ? { signal: input.signal } : {}),
            },
            traceId,
          );
          if (midTurn.checkpoint) {
            messages.splice(0, messages.length, ...midTurn.messages);
            compactionCheckpoint = midTurn.checkpoint;
            this.deps.logger?.info(
              "Agent mid-turn memento compaction traceId=%s round=%d via=%s retainedUsers=%d",
              traceId,
              round,
              midTurn.checkpoint.via,
              midTurn.checkpoint.retainedUserMessages?.length ?? 0,
            );
          }
        }
        this.compactor.shrink(messages, input.modelCapabilities, input.model);
        publishContext();
        if (roundExecutions.length > 0) {
          steps.push({ type: "tool_calls", calls: [...roundExecutions], ...(stepModel ? { model: stepModel } : {}), ...(stepProviderId ? { providerId: stepProviderId } : {}) });
          input.onStepsChanged?.(steps);
        }
      }

      // When unlimited (maxToolRounds === 0) the for-loop never falls through
      // here — it only exits via a final answer, cancel, or unrecoverable
      // error inside the loop body. This guard is defense-in-depth.
      if (roundsUnlimited) {
        throw new ApplicationError(
          "AGENT_MAX_TOOL_ROUNDS",
          "Agent exited the unlimited tool-round loop without a final answer",
          {
            traceId,
            limit: 0,
            partial: buildTurnPartial(
              traceId,
              activeRound,
              toolCalls,
              steps,
              messages,
            ),
          },
        );
      }
      this.deps.logger?.warn("Agent turn reached tool-round limit traceId=%s provider=%s limit=%d", traceId, this.deps.provider.id, maxToolRounds);
      // Surface as interrupted + resumable: returning a success answer used to
      // seal a completed assistant, so "lanjut" became a brand-new turn that
      // compacted the work into an empty "fresh session" handoff.
      throw new ApplicationError(
        "AGENT_MAX_TOOL_ROUNDS",
        `Agent reached the maximum tool rounds (${maxToolRounds}) before producing a final answer`,
        {
          traceId,
          limit: maxToolRounds,
          partial: buildTurnPartial(
            traceId,
            maxToolRounds,
            toolCalls,
            steps,
            messages,
            model,
            providerId,
            api,
            reasoning,
            usage,
          ),
        },
      );
    } catch (error) {
      // Provider soft-recover exhaustion already attaches partial. This catch
      // covers allowlist rejection, listTools failures, user cancel after tool
      // or text progress, and other mid-turn ApplicationErrors so Retry/Continue
      // can resume instead of restarting from scratch.
      if (!hasResumableProgress(toolCalls, steps, liveText, liveReasoning)) throw error;
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
          liveText,
          liveReasoning,
        ),
      );
    }
  }
}
