import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type {
  AgentMessage,
  AgentProvider,
  AgentToolCall,
} from "../ports/agent-provider.port.js";
import type { AgentToolGateway } from "../ports/agent-tool-gateway.port.js";

export interface RunAgentTurnInput {
  readonly messages: readonly AgentMessage[];
  readonly pluginIds: readonly string[];
  readonly maxToolRounds?: number;
  readonly traceId?: string;
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
}

export interface AgentTurnRunnerDeps {
  readonly provider: AgentProvider;
  readonly toolGateway: AgentToolGateway;
  readonly logger?: LoggerPort;
  readonly defaultMaxToolRounds?: number;
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
    const maxToolRounds = normalizeMaxRounds(input.maxToolRounds ?? this.defaultMaxToolRounds);
    const messages: AgentMessage[] = [...input.messages];
    const toolCalls: AgentToolExecution[] = [];
    let model: string | undefined;

    this.deps.logger?.info("Agent turn started traceId=%s provider=%s", traceId, this.deps.provider.id);

    for (let round = 1; round <= maxToolRounds; round += 1) {
      const tools = await this.deps.toolGateway.listTools(input.pluginIds);
      const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));
      let response;
      try {
        response = await this.deps.provider.complete({ traceId, round, messages, tools });
      } catch (error) {
        this.deps.logger?.error("Agent provider failed traceId=%s provider=%s", traceId, this.deps.provider.id);
        throw new ApplicationError("AGENT_PROVIDER_FAILED", "AI provider request failed", {
          providerId: this.deps.provider.id,
          traceId,
          cause: error instanceof Error ? error.message : String(error),
        });
      }
      model = response.model ?? model;
      const requestedCalls = response.toolCalls ?? [];

      if (requestedCalls.length === 0) {
        const text = response.text?.trim();
        if (!text) {
          throw new ApplicationError("AGENT_PROVIDER_FAILED", "AI provider returned neither text nor tool calls", {
            providerId: this.deps.provider.id,
            traceId,
          });
        }
        this.deps.logger?.info("Agent turn completed traceId=%s provider=%s rounds=%d", traceId, this.deps.provider.id, round);
        return { traceId, text, rounds: round, toolCalls, ...(model ? { model } : {}) };
      }

      validateRequestedTools(requestedCalls, toolsByName, traceId);
      messages.push({ role: "assistant", ...(response.text ? { content: response.text } : {}), toolCalls: requestedCalls });

      for (const call of requestedCalls) {
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
    throw new ApplicationError("AGENT_MAX_TOOL_ROUNDS", "AI provider exceeded the maximum tool rounds", {
      providerId: this.deps.provider.id,
      traceId,
      maxToolRounds,
    });
  }

  private async executeTool(call: AgentToolCall, traceId: string, round: number): Promise<AgentToolExecution> {
    this.deps.logger?.info("Agent MCP tool started traceId=%s tool=%s round=%d", traceId, call.name, round);
    try {
      const result = await this.deps.toolGateway.execute(call.name, call.args, call.id);
      this.deps.logger?.info("Agent MCP tool completed traceId=%s tool=%s round=%d", traceId, call.name, round);
      return { id: call.id, name: call.name, ok: true, result };
    } catch (error) {
      const message = error instanceof Error ? error.message : "Tool execution failed";
      this.deps.logger?.warn("Agent MCP tool failed traceId=%s tool=%s round=%d", traceId, call.name, round);
      return { id: call.id, name: call.name, ok: false, error: message };
    }
  }
}

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
