import { randomUUID } from "node:crypto";
import type { AgentProviderRegistryPort, ReasoningEffort } from "../../agent/ports/agent-provider.port.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import {
  AgentTurnRunner,
  type AgentContextOptions,
  type AgentTurnResult,
} from "../../agent/services/agent-turn-runner.js";
import { InProcessAgentTurnWorker, type AgentTurnWorker } from "../../agent/services/in-process-agent-turn-worker.js";
import {
  RoutedAgentProvider,
  type AgentProviderStrategy,
} from "../../agent/services/routed-agent-provider.js";
import type { JobAgentToolGateway } from "./job-agent-tool-gateway.js";
import type { JobMode } from "../job-model.js";

export interface JobAgentExecutorSettings {
  readonly maxToolRounds: number;
  readonly maxRepeatedToolCalls: number;
  readonly strategy: AgentProviderStrategy;
  readonly totalAttemptBudget: number;
  readonly inactivityTimeoutSeconds: number; // 0 = unlimited
  readonly context?: AgentContextOptions;
  readonly compactPrompt?: string;
}

export const DEFAULT_JOB_EXECUTOR_SETTINGS: JobAgentExecutorSettings = {
  maxToolRounds: 8,
  maxRepeatedToolCalls: 50,
  strategy: "failover",
  totalAttemptBudget: 4,
  inactivityTimeoutSeconds: 600,
};

export interface JobAgentExecutorDeps {
  readonly providerRegistry: AgentProviderRegistryPort;
  readonly toolGateway: JobAgentToolGateway;
  readonly defaultProviderId: string;
  readonly logger?: LoggerPort;
  readonly now?: () => Date;
}

export interface JobAgentRunOptions {
  readonly providerId?: string;
  readonly model?: string;
  readonly effort?: ReasoningEffort;
}

export interface JobExecutionResult {
  readonly traceId: string;
  readonly status: "ok" | "error";
  readonly summary: string;
  readonly error?: string;
}

const JOB_SYSTEM_PROMPT =
  "You are running as a scheduled job inside NusaShell. Complete the task " +
  "autonomously using the available MCP plugin tools and docs. Do not ask " +
  "for clarification. Keep the final answer concise. You cannot modify " +
  "memory or skills from a job.";

/**
 * Headless agent turn executor for scheduled jobs. Builds a fresh
 * `AgentTurnRunner` per run (own traceId, no streaming callbacks, no memory
 * injection, restricted tool gateway) and applies an inactivity watchdog.
 */
export class JobAgentExecutor {
  constructor(private readonly deps: JobAgentExecutorDeps) {}

  async runAgent(
    prompt: string,
    settings: JobAgentExecutorSettings,
    externalSignal?: AbortSignal,
    options?: JobAgentRunOptions,
  ): Promise<JobExecutionResult> {
    const providerId = options?.providerId ?? this.deps.defaultProviderId;
    const provider = this.deps.providerRegistry.get(providerId);
    if (!provider) {
      return {
        traceId: randomUUID(),
        status: "error",
        summary: "AI provider not configured",
        error: `provider not found: ${providerId}`,
      };
    }

    const routed = new RoutedAgentProvider({
      providers: this.deps.providerRegistry.list(),
      preferredProviderId: provider.id,
      strategy: settings.strategy,
      totalAttemptBudget: settings.totalAttemptBudget,
      ...(this.deps.logger ? { logger: this.deps.logger } : {}),
    });

    const runner = new AgentTurnRunner({
      provider: routed,
      toolGateway: this.deps.toolGateway,
      defaultMaxToolRounds: settings.maxToolRounds,
      defaultMaxRepeatedToolCalls: settings.maxRepeatedToolCalls,
      ...(this.deps.logger ? { logger: this.deps.logger } : {}),
      ...(settings.context ? { context: settings.context } : {}),
      ...(settings.compactPrompt ? { compactPrompt: settings.compactPrompt } : {}),
    });
    const worker: AgentTurnWorker = new InProcessAgentTurnWorker((input) => runner.run(input));
    const traceId = randomUUID();

    const controller = new AbortController();
    // Bridge an external cancel signal (from the scheduler) to the internal
    // controller so either inactivity-timeout or user-cancel aborts the turn.
    if (externalSignal) {
      if (externalSignal.aborted) controller.abort();
      else externalSignal.addEventListener("abort", () => controller.abort(), { once: true });
    }
    const watchdog = startInactivityWatchdog(
      controller,
      settings.inactivityTimeoutSeconds,
      this.deps.logger,
      traceId,
    );

    try {
      const result = await worker.run({
        messages: [
          { role: "system", content: JOB_SYSTEM_PROMPT },
          { role: "user", content: prompt },
        ],
        pluginIds: [],
        traceId,
        maxToolRounds: settings.maxToolRounds,
        signal: controller.signal,
        ...(options?.model ? { model: options.model } : {}),
        ...(options?.effort ? { effort: options.effort } : {}),
      });
      return toJobResult(result);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.deps.logger?.warn("job agent turn failed traceId=%s: %s", traceId, message);
      return {
        traceId,
        status: "error",
        summary: message,
        error: message,
      };
    } finally {
      watchdog.clear();
    }
  }
}

function startInactivityWatchdog(
  controller: AbortController,
  timeoutSeconds: number,
  logger: LoggerPort | undefined,
  traceId: string,
): { clear(): void } {
  if (timeoutSeconds <= 0) return { clear() {} };
  const timer = setTimeout(() => {
    logger?.warn("job agent turn inactivity timeout traceId=%s after=%ds", traceId, timeoutSeconds);
    controller.abort();
  }, timeoutSeconds * 1000);
  return { clear() { clearTimeout(timer); } };
}

function toJobResult(result: AgentTurnResult): JobExecutionResult {
  const text = (result.text ?? "").trim();
  const failed = result.toolCalls.some((call) => !call.ok) && text.length === 0;
  const status: "ok" | "error" = failed ? "error" : "ok";
  const summary = text.length > 0 ? text.slice(0, 8000) : `Job ran ${result.rounds} round(s), ${result.toolCalls.length} tool call(s)`;
  return {
    traceId: result.traceId,
    status,
    summary,
  };
}

export function describeMode(mode: JobMode): string {
  switch (mode.type) {
    case "agent":
      return `agent: ${mode.prompt.slice(0, 80)}`;
    case "tool":
      return `tool: ${mode.pluginId}/${mode.toolName}`;
  }
}
