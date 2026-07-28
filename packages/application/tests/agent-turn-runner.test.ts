import { describe, expect, it } from "vitest";
import {
  AgentTurnRunner,
  type AgentProvider,
  type AgentProviderRequest,
  type AgentProviderResult,
  type AgentToolGateway,
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
  readonly calls: Array<{ name: string; args: Readonly<Record<string, unknown>> }> = [];

  async listTools() {
    return [{
      name: "notes.create",
      description: "Create a note",
      inputSchema: { type: "object", properties: { title: { type: "string" } } },
    }];
  }

  async execute(name: string, args: Readonly<Record<string, unknown>>) {
    this.calls.push({ name, args });
    if (name === "notes.create") return { id: "note-1" };
    throw new Error(`Unexpected tool ${name}`);
  }
}

describe("AgentTurnRunner", () => {
  it("returns a text-only result in one provider round", async () => {
    const provider = new ScriptedProvider([{ text: "Hello from the agent" }]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    const result = await runner.run({
      messages: [{ role: "user", content: "Say hello" }],
      pluginIds: [],
    });

    expect(result.text).toBe("Hello from the agent");
    expect(result.rounds).toBe(1);
    expect(result.toolCalls).toEqual([]);
  });

  it("executes only an exposed MCP tool and returns its result to the next model round", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "Roadmap" } }] },
      { text: "The note is ready." },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    const result = await runner.run({
      messages: [{ role: "user", content: "Create a roadmap note" }],
      pluginIds: ["notes"],
    });

    expect(result.text).toBe("The note is ready.");
    expect(tools.calls).toEqual([{ name: "notes.create", args: { title: "Roadmap" } }]);
    expect(provider.requests[1]?.messages.at(-1)).toEqual({
      role: "tool",
      toolCallId: "call-1",
      name: "notes.create",
      content: JSON.stringify({ ok: true, result: { id: "note-1" } }),
    });
  });

  it("does not execute a tool that is outside the MCP allowlist", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "filesystem.delete", args: { path: "/tmp/a" } }] },
    ]);
    const tools = new FakeToolGateway();
    const runner = new AgentTurnRunner({ provider, toolGateway: tools });

    await expect(runner.run({
      messages: [{ role: "user", content: "Delete a file" }],
      pluginIds: ["notes"],
    })).rejects.toMatchObject({ code: "AGENT_TOOL_NOT_ALLOWED" });
    expect(tools.calls).toEqual([]);
  });

  it("stops when the provider exceeds the tool-round limit", async () => {
    const provider = new ScriptedProvider([
      { toolCalls: [{ id: "call-1", name: "notes.create", args: { title: "One" } }] },
      { toolCalls: [{ id: "call-2", name: "notes.create", args: { title: "Two" } }] },
    ]);
    const runner = new AgentTurnRunner({ provider, toolGateway: new FakeToolGateway() });

    await expect(runner.run({
      messages: [{ role: "user", content: "Keep creating notes" }],
      pluginIds: ["notes"],
      maxToolRounds: 1,
    })).rejects.toMatchObject({ code: "AGENT_MAX_TOOL_ROUNDS" });
  });
});
