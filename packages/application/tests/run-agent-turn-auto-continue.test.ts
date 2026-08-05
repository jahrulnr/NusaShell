import { describe, expect, it } from "vitest";
import {
  RunAgentTurnHandler,
  InMemoryConversationTodoPort,
  type AgentProvider,
  type AgentProviderRequest,
  type AgentProviderResult,
  type AgentToolGateway,
  type PromptLoaderPort,
  type AgentPrompt,
  type ReviewPromptKind,
} from "../src/index.js";

class ScriptedProvider implements AgentProvider {
  readonly id = "scripted";
  readonly requests: AgentProviderRequest[] = [];
  constructor(private readonly responses: readonly AgentProviderResult[]) {}

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    this.requests.push(request);
    const response = this.responses[this.requests.length - 1];
    if (!response) throw new Error("No scripted provider response");
    return response;
  }
}

class FakeToolGateway implements AgentToolGateway {
  beginTurn() {}
  endTurn() {}
  cancelTurn() {}
  async listTools() { return []; }
  async execute() { return { ok: true }; }
}

class FakePromptLoader implements PromptLoaderPort {
  readonly loadedContinue: string[] = [];
  constructor(private readonly continuePrompt = "Continue pursuing open CURRENT TASKS.") {}
  async loadPrompts(): Promise<readonly AgentPrompt[]> { return []; }
  async loadCompactPrompt() { return undefined; }
  async loadSubagentPrompt() { return undefined; }
  async loadContinuePrompt(): Promise<string | undefined> {
    this.loadedContinue.push(this.continuePrompt);
    return this.continuePrompt;
  }
  async loadReviewPrompt(_kind: ReviewPromptKind): Promise<string> { return ""; }
}

function makeRegistry(provider: AgentProvider) {
  return { get: (id: string) => (id === provider.id ? provider : undefined), list: () => [provider] };
}

const RUNTIME = {
  strategy: "failover" as const,
  totalAttemptBudget: 1,
  maxToolRounds: 1,
  maxRepeatedToolCalls: 1,
  softRecoverAttempts: 0,
  maxConcurrentToolCalls: 1,
  maxAutoContinues: 10,
};

/**
 * Build a handler with only the deps this suite cares about (todo port +
 * optional prompt loader). All other positional ctor params default to
 * undefined so the count stays correct.
 */
function buildHandler(opts: { provider: AgentProvider; todos: InMemoryConversationTodoPort; promptLoader?: PromptLoaderPort }) {
  // Positions: providers, toolGateway, defaultProviderId, runtime, logger,
  // coordinator, onTextDelta, onReasoningDelta, onToolCallStart, onToolCallEnd,
  // onContextUpdate, promptLoader, userPrompt, memoryStore, onTurnComplete,
  // onTurnEnd, onTurnStarted, onTurnSuperseded, runtimeOsProbe, activeTurns,
  // onTurnProgress, subagentPort, todoPort, skillRegistry.
  return new RunAgentTurnHandler(
    makeRegistry(opts.provider), // 1
    new FakeToolGateway(),       // 2
    "scripted",                  // 3
    RUNTIME,                     // 4
    undefined,                   // 5 logger
    undefined,                   // 6 coordinator
    undefined,                   // 7 onTextDelta
    undefined,                   // 8 onReasoningDelta
    undefined,                   // 9 onToolCallStart
    undefined,                   // 10 onToolCallEnd
    undefined,                   // 11 onContextUpdate
    opts.promptLoader,           // 12 promptLoader
    undefined,                   // 13 userPrompt
    undefined,                   // 14 memoryStore
    undefined,                   // 15 onTurnComplete
    undefined,                   // 16 onTurnEnd
    undefined,                   // 17 onTurnStarted
    undefined,                   // 18 onTurnSuperseded
    undefined,                   // 19 runtimeOsProbe
    undefined,                   // 20 activeTurns
    undefined,                   // 21 onTurnProgress
    undefined,                   // 22 subagentPort
    opts.todos,                  // 23 todoPort
  );
}

describe("RunAgentTurnHandler auto-continue", () => {
  it("attaches shouldContinue=true on a successful turn with open todos", async () => {
    const provider = new ScriptedProvider([{ text: "done one task" }]);
    const todos = new InMemoryConversationTodoPort();
    todos.set("conv-1", [
      { id: "1", content: "first", status: "completed" },
      { id: "2", content: "second", status: "in_progress" },
    ]);
    const handler = buildHandler({ provider, todos });
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-ok",
      conversationId: "conv-1",
      messages: [{ role: "user", content: "go" }],
      pluginIds: [],
    });
    expect(result.autoContinue).toMatchObject({ shouldContinue: true, openTodoCount: 1, continuesUsed: 0, reason: "continue" });
  });

  it("omits autoContinue when no conversation is bound", async () => {
    const provider = new ScriptedProvider([{ text: "hi" }]);
    const todos = new InMemoryConversationTodoPort();
    const handler = buildHandler({ provider, todos });
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-noconv",
      messages: [{ role: "user", content: "hi" }],
      pluginIds: [],
    });
    expect(result.autoContinue).toBeUndefined();
  });

  it("injects the continue prompt when autoContinueIndex > 0", async () => {
    const provider = new ScriptedProvider([{ text: "continuing" }]);
    const todos = new InMemoryConversationTodoPort();
    todos.set("conv-2", [{ id: "1", content: "open", status: "pending" }]);
    const promptLoader = new FakePromptLoader();
    const handler = buildHandler({ provider, todos, promptLoader });
    const result = await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-cont",
      conversationId: "conv-2",
      autoContinueIndex: 1,
      messages: [{ role: "user", content: "earlier" }],
      pluginIds: [],
    });
    expect(result.text).toBe("continuing");
    expect(promptLoader.loadedContinue).toHaveLength(1);
    const sent = provider.requests[0]?.messages ?? [];
    expect(sent.some((m) => m.role === "user" && typeof m.content === "string" && m.content.includes("Continue pursuing open CURRENT TASKS."))).toBe(true);
    expect(result.autoContinue).toMatchObject({ shouldContinue: true, continuesUsed: 1 });
  });

  it("does not load the continue prompt when autoContinueIndex is 0", async () => {
    const provider = new ScriptedProvider([{ text: "first" }]);
    const todos = new InMemoryConversationTodoPort();
    todos.set("conv-3", [{ id: "1", content: "open", status: "pending" }]);
    const promptLoader = new FakePromptLoader();
    const handler = buildHandler({ provider, todos, promptLoader });
    await handler.handle({
      kind: "run-agent-turn",
      traceId: "trace-first",
      conversationId: "conv-3",
      messages: [{ role: "user", content: "go" }],
      pluginIds: [],
    });
    expect(promptLoader.loadedContinue).toHaveLength(0);
  });
});
