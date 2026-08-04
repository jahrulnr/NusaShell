import { describe, expect, it } from "vitest";
import { OpenAiCompatibleAgentProvider } from "../src/ai/openai-compatible-agent-provider.js";

const apiKey = process.env.BLACKBOX_API_KEY;
const baseUrl = "https://api.blackbox.ai/v1";
const model = "blackboxai/moonshotai/kimi-k3";

const describeLive = apiKey ? describe : describe.skip;

describeLive("Blackbox API live streaming (Messages API)", () => {
  it("streams thinking + text deltas live via onReasoningDelta/onTextDelta", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      id: "blackbox",
      api: "messages",
      baseUrl,
      apiKey,
      stream: true,
      timeoutMs: 30_000,
    });

    const textDeltas: string[] = [];
    const reasoningDeltas: string[] = [];

    const result = await provider.complete({
      traceId: "live-stream-test",
      round: 1,
      messages: [{ role: "user", content: "Say hello in one word. Think briefly first." }],
      tools: [],
      model,
      onTextDelta: (delta) => { textDeltas.push(delta); },
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    // The bug: previously Messages API never streamed, so these arrays
    // would be empty and reasoning only appeared in result.reasoning after
    // the full response. Now deltas must fire live.
    expect(reasoningDeltas.length).toBeGreaterThan(0);
    expect(textDeltas.length).toBeGreaterThan(0);

    // Reassembled text matches
    const reassembledText = textDeltas.join("");
    expect(result.text).toBe(reassembledText);
    expect(result.text!.toLowerCase()).toContain("hello");

    // Reasoning was streamed live too
    const reassembledReasoning = reasoningDeltas.join("");
    expect(result.reasoning).toBe(reassembledReasoning);
    expect(result.reasoning!.length).toBeGreaterThan(0);

    expect(result.api).toBe("messages");
    expect(result.model).toBeTruthy();
  }, 60_000);

  it("streams tool_use via input_json_delta", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      id: "blackbox",
      api: "messages",
      baseUrl,
      apiKey,
      stream: true,
      timeoutMs: 30_000,
    });

    const result = await provider.complete({
      traceId: "live-tool-stream-test",
      round: 1,
      messages: [{ role: "user", content: "Use the search tool to find notes about testing" }],
      tools: [{
        name: "search",
        description: "Search for notes",
        inputSchema: {
          type: "object",
          properties: { query: { type: "string" } },
          required: ["query"],
        },
      }],
      model,
    });

    expect(result.toolCalls).toBeDefined();
    expect(result.toolCalls!.length).toBeGreaterThan(0);
    expect(result.toolCalls![0].name).toBe("search");
    expect(result.toolCalls![0].args).toHaveProperty("query");
    expect(typeof result.toolCalls![0].args.query).toBe("string");
    expect(result.toolCalls![0].args.query.length).toBeGreaterThan(0);
  }, 60_000);
});
