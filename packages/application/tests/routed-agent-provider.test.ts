import { describe, expect, it } from "vitest";
import {
  RoutedAgentProvider,
  type AgentProvider,
  type AgentProviderRequest,
  type AgentProviderResult,
} from "../src/index.js";

class Provider implements AgentProvider {
  readonly requests: AgentProviderRequest[] = [];

  constructor(
    readonly id: string,
    private readonly script: Array<AgentProviderResult | Error>,
  ) {}

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    this.requests.push(request);
    const next = this.script.shift();
    if (next instanceof Error) throw next;
    if (!next) throw new Error("No scripted result");
    return next;
  }
}

describe("RoutedAgentProvider", () => {
  it("fails over on transient errors and pins the successful provider for later rounds", async () => {
    const transient = Object.assign(new Error("temporary"), { transient: true });
    const first = new Provider("first", [transient]);
    const second = new Provider("second", [{ text: "recovered" }, { text: "pinned" }]);
    const routed = new RoutedAgentProvider({
      providers: [first, second],
      preferredProviderId: "first",
      strategy: "failover",
      totalAttemptBudget: 4,
    });
    const request = turnRequest("selected-model");

    expect(await routed.complete(request)).toMatchObject({ text: "recovered", providerId: "second" });
    expect(await routed.complete(request)).toMatchObject({ text: "pinned", providerId: "second" });
    expect(first.requests).toHaveLength(1);
    expect(second.requests[0]?.model).toBeUndefined();
    expect(second.requests[1]?.model).toBeUndefined();
  });

  it("does not fail over after a non-transient provider error", async () => {
    const first = new Provider("first", [new Error("invalid request")]);
    const second = new Provider("second", [{ text: "must not run" }]);
    const routed = new RoutedAgentProvider({
      providers: [first, second],
      preferredProviderId: "first",
      strategy: "failover",
      totalAttemptBudget: 4,
    });

    await expect(routed.complete(turnRequest())).rejects.toThrow("invalid request");
    expect(second.requests).toHaveLength(0);
  });

  it("does not fail over when preferred provider hits a billing/quota permanent failure", async () => {
    const billing = Object.assign(
      new Error('Provider returned HTTP 429: {"error":{"code":"1113","message":"Insufficient balance"}}'),
      { transient: false, status: 429 },
    );
    const first = new Provider("z-ai", [billing]);
    const second = new Provider("blackbox", [{ text: "must not run" }]);
    const warnings: string[] = [];
    const routed = new RoutedAgentProvider({
      providers: [first, second],
      preferredProviderId: "z-ai",
      strategy: "failover",
      totalAttemptBudget: 4,
      logger: { warn: (msg) => { warnings.push(String(msg)); }, info: () => undefined, error: () => undefined, debug: () => undefined },
    });

    await expect(routed.complete(turnRequest("glm-5.2"))).rejects.toThrow("Insufficient balance");
    expect(second.requests).toHaveLength(0);
    expect(warnings).toHaveLength(0);
  });

  it("uses only the preferred provider in switch mode", async () => {
    const transient = Object.assign(new Error("temporary"), { transient: true });
    const first = new Provider("first", [transient]);
    const second = new Provider("second", [{ text: "must not run" }]);
    const routed = new RoutedAgentProvider({
      providers: [first, second],
      preferredProviderId: "first",
      strategy: "switch",
      totalAttemptBudget: 4,
    });

    await expect(routed.complete(turnRequest())).rejects.toThrow("temporary");
    expect(second.requests).toHaveLength(0);
  });

  it("honors one global attempt budget across provider failover", async () => {
    const transient = Object.assign(new Error("temporary"), { transient: true });
    const first = new Provider("first", [transient]);
    const second = new Provider("second", [{ text: "over budget" }]);
    const routed = new RoutedAgentProvider({
      providers: [first, second],
      preferredProviderId: "first",
      strategy: "failover",
      totalAttemptBudget: 1,
    });

    await expect(routed.complete(turnRequest())).rejects.toThrow("temporary");
    expect(second.requests).toHaveLength(0);
  });

  it("tries another provider when a provider pinned by an earlier round becomes transiently unavailable", async () => {
    const transient = Object.assign(new Error("temporary"), { transient: true });
    const first = new Provider("first", [{ text: "round one" }, transient]);
    const second = new Provider("second", [{ text: "round two fallback" }]);
    const routed = new RoutedAgentProvider({
      providers: [first, second],
      preferredProviderId: "first",
      strategy: "failover",
      totalAttemptBudget: 4,
    });

    await expect(routed.complete(turnRequest("selected"))).resolves.toMatchObject({ providerId: "first" });
    await expect(routed.complete(turnRequest("selected"))).resolves.toMatchObject({
      text: "round two fallback",
      providerId: "second",
    });
    expect(second.requests[0]?.model).toBeUndefined();
  });
});

function turnRequest(model?: string): AgentProviderRequest {
  return {
    traceId: "trace-router",
    round: 1,
    messages: [{ role: "user", content: "Hello" }],
    tools: [],
    ...(model ? { model } : {}),
  };
}
