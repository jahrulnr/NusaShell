import { describe, it, expect, vi, beforeEach } from "vitest";
import { BackgroundReviewScheduler, DEFAULT_REVIEW_SETTINGS } from "../src/agent/services/background-review-scheduler.js";
import type { ReviewStateStorePort, ReviewState } from "../src/agent/ports/review-state-store.port.js";
import type { PromptLoaderPort, ReviewPromptKind } from "../src/agent/ports/prompt-loader.port.js";
import type { AgentProviderRegistryPort, AgentProvider, AgentMessage } from "../src/agent/ports/agent-provider.port.js";
import type { AgentTurnResult, AgentTurnRunner } from "../src/agent/services/agent-turn-runner.js";
import type { ReviewAgentToolGateway } from "../src/agent/services/review-agent-tool-gateway.js";
import type { EventDispatcher } from "../src/events/event-dispatcher.js";

function makeResult(overrides: Partial<AgentTurnResult> = {}): AgentTurnResult {
  return {
    traceId: "trace-1",
    text: "done",
    rounds: 3,
    toolCalls: [],
    ...overrides,
  };
}

function makeFakeStateStore(initial: ReviewState = { turnsSinceMemory: 0, toolRoundsSinceSkill: 0 }): ReviewStateStorePort {
  let state = initial;
  return {
    async load() { return state; },
    async save(next: ReviewState) { state = next; },
  };
}

function makeFakePromptLoader(): PromptLoaderPort {
  const prompts: Record<ReviewPromptKind, string> = {
    memory: "memory review prompt",
    skill: "skill review prompt",
    combined: "combined review prompt",
  };
  return {
    async loadPrompts() { return []; },
    async loadCompactPrompt() { return undefined; },
    async loadReviewPrompt(kind: ReviewPromptKind) { return prompts[kind]; },
  };
}

function makeFakeProviderRegistry(provider?: AgentProvider): AgentProviderRegistryPort {
  return {
    get: vi.fn(() => provider),
    list: vi.fn(() => provider ? [provider] : []),
    set: vi.fn(),
    delete: vi.fn(),
  };
}

function makeFakeRunner(result: AgentTurnResult): AgentTurnRunner {
  return {
    run: vi.fn(async () => result),
  } as unknown as AgentTurnRunner;
}

describe("BackgroundReviewScheduler", () => {
  let scheduler: BackgroundReviewScheduler;
  let stateStore: ReviewStateStorePort;
  let promptLoader: PromptLoaderPort;
  let providerRegistry: AgentProviderRegistryPort;
  let reviewGateway: ReviewAgentToolGateway;
  let eventDispatcher: EventDispatcher;
  let runnerFactory: ReturnType<typeof vi.fn>;
  let fakeProvider: AgentProvider;

  beforeEach(() => {
    stateStore = makeFakeStateStore();
    promptLoader = makeFakePromptLoader();
    fakeProvider = {} as AgentProvider;
    providerRegistry = makeFakeProviderRegistry(fakeProvider);
    reviewGateway = {} as ReviewAgentToolGateway;
    eventDispatcher = {
      publish: vi.fn(async () => {}),
      on: vi.fn(),
      onAny: vi.fn(),
      publishAll: vi.fn(),
    } as unknown as EventDispatcher;

    runnerFactory = vi.fn(() => makeFakeRunner(makeResult({
      toolCalls: [
        { callId: "c1", name: "memory", args: {}, ok: true, result: "saved", durationMs: 10 },
      ],
    })));

    scheduler = new BackgroundReviewScheduler({
      stateStore,
      promptLoader,
      providerRegistry,
      reviewGateway,
      runnerFactory,
      defaultProviderId: "test-provider",
      eventDispatcher,
    });
  });

  it("uses default settings", () => {
    expect(scheduler.getSettings()).toEqual(DEFAULT_REVIEW_SETTINGS);
  });

  it("does not spawn when disabled", async () => {
    scheduler.configure({ enabled: false });
    await scheduler.tick(makeResult());
    expect(runnerFactory).not.toHaveBeenCalled();
  });

  it("increments counters but does not spawn below threshold", async () => {
    scheduler.configure({ memoryEveryNTurns: 5, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 3 }));
    expect(runnerFactory).not.toHaveBeenCalled();
    const state = await stateStore.load();
    expect(state.turnsSinceMemory).toBe(1);
    expect(state.toolRoundsSinceSkill).toBe(3);
  });

  it("spawns memory review when turnsSinceMemory crosses threshold", async () => {
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 0 }));
    expect(runnerFactory).toHaveBeenCalledTimes(1);
    const state = await stateStore.load();
    expect(state.turnsSinceMemory).toBe(0);
  });

  it("spawns skill review when toolRoundsSinceSkill crosses threshold", async () => {
    scheduler.configure({ memoryEveryNTurns: 100, skillEveryNToolRounds: 5 });
    await scheduler.tick(makeResult({ rounds: 5 }));
    expect(runnerFactory).toHaveBeenCalledTimes(1);
    const state = await stateStore.load();
    expect(state.toolRoundsSinceSkill).toBe(0);
  });

  it("spawns combined review when both thresholds cross", async () => {
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 1 });
    await scheduler.tick(makeResult({ rounds: 3 }));
    expect(runnerFactory).toHaveBeenCalledTimes(1);
  });

  it("publishes agent.learning_updated event when mutations detected", async () => {
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 0 }));
    // Wait for fire-and-forget spawn
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(eventDispatcher.publish).toHaveBeenCalledTimes(1);
    const event = (eventDispatcher.publish as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(event.type).toBe("agent.learning_updated");
    expect(event.kinds).toContain("memory");
  });

  it("does not publish event when no mutations detected", async () => {
    runnerFactory = vi.fn(() => makeFakeRunner(makeResult({
      toolCalls: [],
    })));
    scheduler = new BackgroundReviewScheduler({
      stateStore,
      promptLoader,
      providerRegistry,
      reviewGateway,
      runnerFactory,
      defaultProviderId: "test-provider",
      eventDispatcher,
    });
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 0 }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(eventDispatcher.publish).not.toHaveBeenCalled();
  });

  it("does not spawn when review already in flight", async () => {
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 0 }));
    // Second tick should be skipped because reviewInFlight is true
    // (the fake runner resolves synchronously, but the flag is set before)
    // Actually since our fake runner resolves immediately, reviewInFlight
    // will be false by the time the second tick runs. So let's test with
    // a slow runner instead.
    let resolveRun: () => void;
    const slowResult = makeResult();
    runnerFactory = vi.fn(() => ({
      run: vi.fn(() => new Promise<AgentTurnResult>((resolve) => {
        resolveRun = () => resolve(slowResult);
      })),
    }) as unknown as AgentTurnRunner);
    scheduler = new BackgroundReviewScheduler({
      stateStore,
      promptLoader,
      providerRegistry,
      reviewGateway,
      runnerFactory,
      defaultProviderId: "test-provider",
      eventDispatcher,
    });
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 0 }));
    // While the first review is still pending, tick again
    await scheduler.tick(makeResult({ rounds: 0 }));
    expect(runnerFactory).toHaveBeenCalledTimes(1);
    resolveRun!();
    await new Promise((resolve) => setTimeout(resolve, 50));
  });

  it("handles missing provider gracefully", async () => {
    providerRegistry = makeFakeProviderRegistry(undefined);
    scheduler = new BackgroundReviewScheduler({
      stateStore,
      promptLoader,
      providerRegistry,
      reviewGateway,
      runnerFactory,
      defaultProviderId: "missing",
      eventDispatcher,
    });
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100 });
    await scheduler.tick(makeResult({ rounds: 0 }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(runnerFactory).not.toHaveBeenCalled();
  });

  it("builds transcript from messages snapshot", async () => {
    const messages: AgentMessage[] = [
      { role: "user", content: "hello" },
      { role: "assistant", content: "hi there" },
    ];
    scheduler.configure({ memoryEveryNTurns: 1, skillEveryNToolRounds: 100, transcriptTailMessages: 40 });
    await scheduler.tick(makeResult({ rounds: 0, messages }));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(runnerFactory).toHaveBeenCalledTimes(1);
    const runArg = (runnerFactory.mock.calls[0][0] as { maxToolRounds: number }).maxToolRounds;
    expect(runArg).toBe(6);
  });
});
