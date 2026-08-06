import { describe, expect, it } from "vitest";
import {
  JobAgentToolGateway,
  McpAgentToolGateway,
  type AgentProvider,
  type AgentProviderResult,
  type AgentToolDefinition,
  type AgentToolGateway,
} from "../src/index.js";

class FakeInnerGateway implements AgentToolGateway {
  readonly begun: string[] = [];
  readonly ended: string[] = [];
  readonly cancelled: string[] = [];
  readonly executed: Array<{ name: string; turnId: string }> = [];

  beginTurn(turnId: string) { this.begun.push(turnId); }
  endTurn(turnId: string) { this.ended.push(turnId); }
  cancelTurn(turnId: string) { this.cancelled.push(turnId); }

  async listTools(): Promise<readonly AgentToolDefinition[]> {
    return [
      { name: "mcp_list", description: "list" },
      { name: "docs_search", description: "search docs" },
      { name: "memory", description: "mutate memory" },
      { name: "skill_manage", description: "mutate skills" },
      { name: "skill_list", description: "list skills" },
      { name: "skill_read", description: "read skill" },
      { name: "skill_search", description: "search skills" },
      { name: "job", description: "manage jobs" },
      { name: "mcp_create", description: "granted plugin tool" },
      { name: "pipeline", description: "manage pipelines" },
      { name: "subagent", description: "spawn ACP subagent" },
      { name: "mcp_enable", description: "start a plugin" },
      { name: "mcp_disable", description: "stop a plugin" },
      { name: "mcp_unregister", description: "unregister a plugin" },
    ];
  }

  async execute(name: string, _args: Readonly<Record<string, unknown>>, _requestId: string, turnId: string): Promise<unknown> {
    this.executed.push({ name, turnId });
    return { ok: true, name };
  }
}

describe("JobAgentToolGateway", () => {
  it("filters denied tools from listTools", async () => {
    const inner = new FakeInnerGateway();
    const gateway = new JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    const tools = await gateway.listTools([], "turn-1");
    const names = tools.map((t) => t.name);
    expect(names).toContain("mcp_list");
    expect(names).toContain("docs_search");
    expect(names).toContain("mcp_create");
    expect(names).not.toContain("memory");
    expect(names).not.toContain("skill_manage");
    expect(names).not.toContain("skill_list");
    expect(names).not.toContain("skill_read");
    expect(names).not.toContain("skill_search");
    expect(names).not.toContain("job");
    expect(names).not.toContain("pipeline");
    expect(names).not.toContain("subagent");
    expect(names).not.toContain("mcp_enable");
    expect(names).not.toContain("mcp_disable");
    expect(names).not.toContain("mcp_unregister");
    expect(names).toContain("mcp_create");
  });

  it("denies executing memory, skill, and job tools", async () => {
    const inner = new FakeInnerGateway();
    const gateway = new JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    await expect(gateway.execute("memory", {}, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("skill_manage", {}, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("skill_list", {}, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("skill_read", {}, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("skill_search", {}, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("job", { action: "list" }, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("pipeline", { action: "list" }, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("subagent", { prompt: "x" }, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("mcp_enable", { pluginId: "nusashell.files" }, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("mcp_disable", { pluginId: "nusashell.notes" }, "r1", "t1")).rejects.toThrow(/not allowed/);
    await expect(gateway.execute("mcp_unregister", { pluginId: "x" }, "r1", "t1")).rejects.toThrow(/not allowed/);
    expect(inner.executed).toHaveLength(0);
  });

  it("allows executing mcp and docs tools", async () => {
    const inner = new FakeInnerGateway();
    const gateway = new JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    await gateway.execute("mcp_list", {}, "r1", "t1");
    await gateway.execute("docs_search", { query: "x" }, "r2", "t1");
    await gateway.execute("mcp_create", { title: "a" }, "r3", "t1");
    expect(inner.executed).toHaveLength(3);
  });

  it("delegates begin/end/cancelTurn", async () => {
    const inner = new FakeInnerGateway();
    const gateway = new JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    gateway.beginTurn("t1");
    await gateway.cancelTurn("t1");
    gateway.endTurn("t1");
    expect(inner.begun).toEqual(["t1"]);
    expect(inner.cancelled).toEqual(["t1"]);
    expect(inner.ended).toEqual(["t1"]);
  });
});

// ---- JobAgentExecutor ----

class ScriptedProvider implements AgentProvider {
  readonly id = "scripted";
  private queue: readonly AgentProviderResult[];
  constructor(responses: readonly AgentProviderResult[]) {
    this.queue = responses;
  }
  async complete(): Promise<AgentProviderResult> {
    const response = this.queue[0];
    this.queue = this.queue.slice(1);
    if (!response) throw new Error("No scripted response");
    return response;
  }
}

class FakeRegistry {
  private readonly providers: Map<string, AgentProvider>;
  constructor(providers: readonly AgentProvider[]) {
    this.providers = new Map(providers.map((p) => [p.id, p]));
  }
  get(id: string): AgentProvider | undefined { return this.providers.get(id); }
  list(): readonly AgentProvider[] { return [...this.providers.values()]; }
}

describe("JobAgentExecutor", () => {
  it("runs a headless turn with its own traceId and returns a summary", async () => {
    const { JobAgentExecutor, DEFAULT_JOB_EXECUTOR_SETTINGS } = await import("../src/job/services/job-agent-executor.js");
    const provider = new ScriptedProvider([{ text: "Job done", model: "scripted" }]);
    const inner = new FakeInnerGateway();
    const gateway = new (await import("../src/job/services/job-agent-tool-gateway.js")).JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    const executor = new JobAgentExecutor({
      providerRegistry: new FakeRegistry([provider]) as unknown as import("../src/index.js").AgentProviderRegistryPort,
      toolGateway: gateway,
      defaultProviderId: "scripted",
    });
    const result = await executor.runAgent("Summarize the inbox", {
      ...DEFAULT_JOB_EXECUTOR_SETTINGS,
      inactivityTimeoutSeconds: 0,
    });
    expect(result.status).toBe("ok");
    expect(result.summary).toBe("Job done");
    expect(result.traceId).toMatch(/^[0-9a-f-]{36}$/i);
  });

  it("returns an error result when the provider is missing", async () => {
    const { JobAgentExecutor, DEFAULT_JOB_EXECUTOR_SETTINGS } = await import("../src/job/services/job-agent-executor.js");
    const inner = new FakeInnerGateway();
    const gateway = new (await import("../src/job/services/job-agent-tool-gateway.js")).JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    const executor = new JobAgentExecutor({
      providerRegistry: new FakeRegistry([]) as unknown as import("../src/index.js").AgentProviderRegistryPort,
      toolGateway: gateway,
      defaultProviderId: "missing",
    });
    const result = await executor.runAgent("do something", DEFAULT_JOB_EXECUTOR_SETTINGS);
    expect(result.status).toBe("error");
    expect(result.error).toMatch(/provider not found/);
  });

  it("uses the options.providerId override instead of the default", async () => {
    const { JobAgentExecutor, DEFAULT_JOB_EXECUTOR_SETTINGS } = await import("../src/job/services/job-agent-executor.js");
    const override = new ScriptedProvider([{ text: "from override", model: "alt-model" }]);
    const defaultProvider = new ScriptedProvider([{ text: "from default", model: "scripted" }]);
    const inner = new FakeInnerGateway();
    const gateway = new (await import("../src/job/services/job-agent-tool-gateway.js")).JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    const executor = new JobAgentExecutor({
      providerRegistry: new FakeRegistry([defaultProvider, override]) as unknown as import("../src/index.js").AgentProviderRegistryPort,
      toolGateway: gateway,
      defaultProviderId: "scripted",
    });
    const result = await executor.runAgent(
      "Summarize",
      { ...DEFAULT_JOB_EXECUTOR_SETTINGS, inactivityTimeoutSeconds: 0 },
      undefined,
      { providerId: "scripted", model: "alt-model" },
    );
    expect(result.status).toBe("ok");
    expect(result.summary).toBe("from override");
  });

  it("returns an error when the override providerId is not registered", async () => {
    const { JobAgentExecutor, DEFAULT_JOB_EXECUTOR_SETTINGS } = await import("../src/job/services/job-agent-executor.js");
    const defaultProvider = new ScriptedProvider([{ text: "from default", model: "scripted" }]);
    const inner = new FakeInnerGateway();
    const gateway = new (await import("../src/job/services/job-agent-tool-gateway.js")).JobAgentToolGateway(inner as unknown as McpAgentToolGateway);
    const executor = new JobAgentExecutor({
      providerRegistry: new FakeRegistry([defaultProvider]) as unknown as import("../src/index.js").AgentProviderRegistryPort,
      toolGateway: gateway,
      defaultProviderId: "scripted",
    });
    const result = await executor.runAgent(
      "Summarize",
      DEFAULT_JOB_EXECUTOR_SETTINGS,
      undefined,
      { providerId: "nonexistent" },
    );
    expect(result.status).toBe("error");
    expect(result.error).toMatch(/provider not found: nonexistent/);
  });
});
