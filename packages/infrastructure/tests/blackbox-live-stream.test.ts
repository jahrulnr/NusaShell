import { describe, expect, it, vi } from "vitest";
import { OpenAiCompatibleAgentProvider } from "../src/ai/openai-compatible-agent-provider.js";

/**
 * Blackbox Messages API streaming — stubbed.
 *
 * This suite was previously a LIVE integration test that called
 * api.blackbox.ai with a real BLACKBOX_API_KEY (skipped otherwise), which made
 * it flaky on network and spent real tokens. The provider surface it exercised
 * (Messages API text/thinking streaming + tool_use via input_json_delta) is
 * fully covered deterministically here using an injected `fetchFn` that serves
 * a canned Anthropic-style SSE stream — no network, no key, no cost. The same
 * scenarios are also covered generically in
 * `openai-compatible-agent-provider.test.ts`.
 */
const baseUrl = "https://api.blackbox.ai/v1";
const model = "blackboxai/moonshotai/kimi-k3";

/** Build a `fetch` stub that returns a canned SSE body for the Messages API. */
function stubFetch(sseBody: string): typeof fetch {
  return vi.fn<typeof fetch>().mockResolvedValue(
    new Response(sseBody, {
      status: 200,
      headers: { "content-type": "text/event-stream" },
    }),
  );
}

/** Anthropic-style Messages SSE that streams thinking + a short text reply. */
function thinkAndSaySse(reasoning: string, text: string): string {
  const events: string[] = [
    'event: message_start',
    'data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":5}}}',
    "",
    'event: content_block_start',
    `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
    "",
  ];
  for (const chunk of reasoning.split(" ")) {
    events.push('event: content_block_delta', `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"${chunk} "}}`, "");
  }
  events.push(
    'event: content_block_stop',
    'data: {"type":"content_block_stop","index":0}',
    "",
    'event: content_block_start',
    'data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}',
    "",
  );
  for (const chunk of text.split(" ")) {
    events.push('event: content_block_delta', `data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"${chunk} "}}`, "");
  }
  events.push(
    'event: content_block_stop',
    'data: {"type":"content_block_stop","index":1}',
    "",
    'event: message_delta',
    'data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}',
    "",
    'event: message_stop',
    'data: {"type":"message_stop"}',
    "",
  );
  return events.join("\n");
}

describe("Blackbox Messages API streaming (stubbed)", () => {
  it("streams thinking + text deltas live via onReasoningDelta/onTextDelta", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      id: "blackbox",
      api: "messages",
      baseUrl,
      apiKey: "test-key",
      stream: true,
      timeoutMs: 30_000,
      fetchFn: stubFetch(
        thinkAndSaySse("Hello there", "Hello world"),
      ),
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

    // Deltas must fire live (the bug: Messages API previously never streamed).
    expect(reasoningDeltas.length).toBeGreaterThan(0);
    expect(textDeltas.length).toBeGreaterThan(0);
    expect(reasoningDeltas.join("").trim()).toBe("Hello there");
    expect(textDeltas.join("").trim()).toBe("Hello world");

    // Reassembled text matches (stream chunks are quoted with trailing space).
    expect(result.text!.trim()).toBe("Hello world");
    expect(result.text!.toLowerCase()).toContain("hello");
    expect(result.reasoning!.trim()).toBe("Hello there");
    expect(result.reasoning!.length).toBeGreaterThan(0);

    expect(result.api).toBe("messages");
    expect(result.model).toBeTruthy();
  });

  it("streams tool_use via input_json_delta", async () => {
    const sse = [
      'event: message_start',
      'data: {"type":"message_start","message":{"model":"m"}}',
      "",
      'event: content_block_start',
      'data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"search"}}',
      "",
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\\"q\\":"}}',
      "",
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\\"notes\\"}"}}',
      "",
      'event: content_block_stop',
      'data: {"type":"content_block_stop","index":0}',
      "",
      'event: message_delta',
      'data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}',
      "",
      'event: message_stop',
      'data: {"type":"message_stop"}',
      "",
    ].join("\n");

    const provider = new OpenAiCompatibleAgentProvider({
      id: "blackbox",
      api: "messages",
      baseUrl,
      apiKey: "test-key",
      stream: true,
      timeoutMs: 30_000,
      fetchFn: stubFetch(sse),
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
          properties: { q: { type: "string" } },
          required: ["q"],
        },
      }],
      model,
    });

    expect(result.toolCalls).toBeDefined();
    expect(result.toolCalls!.length).toBeGreaterThan(0);
    expect(result.toolCalls![0]!.name).toBe("search");
    expect(result.toolCalls![0]!.args).toHaveProperty("q");
    expect(typeof result.toolCalls![0]!.args.q).toBe("string");
    expect((result.toolCalls![0]!.args.q as string).length).toBeGreaterThan(0);
  });
});
