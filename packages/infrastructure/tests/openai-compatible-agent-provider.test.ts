import { describe, expect, it, vi } from "vitest";
import { OpenAiCompatibleAgentProvider } from "../src/index.js";

describe("OpenAiCompatibleAgentProvider", () => {
  it("maps tool calls through the OpenAI Responses API", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "gpt-5",
      output: [
        { type: "message", content: [{ type: "output_text", text: "Checking notes." }] },
        { type: "function_call", call_id: "call-response", name: "tool_search", arguments: "{\"query\":\"notes\"}" },
      ],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "openai",
      api: "responses",
      baseUrl: "https://api.openai.com/v1",
      apiKey: "secret-key",
      fetchFn,
    });

    const result = await provider.complete({
      traceId: "trace-responses",
      round: 1,
      messages: [{ role: "user", content: "Find notes" }],
      tools: [{ name: "tool_search", inputSchema: { type: "object" } }],
      model: "gpt-5",
      effort: "high",
    });

    expect(fetchFn).toHaveBeenCalledWith("https://api.openai.com/v1/responses", expect.anything());
    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).toMatchObject({
      model: "gpt-5",
      reasoning: { effort: "high" },
      tools: [{ type: "function", name: "tool_search" }],
    });
    expect(result).toEqual({
      text: "Checking notes.",
      toolCalls: [{ id: "call-response", name: "tool_search", args: { query: "notes" } }],
      model: "gpt-5",
      providerId: "openai",
      api: "responses",
    });
  });

  it("maps tool calls through the Anthropic Messages API", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "claude-sonnet-4",
      content: [
        { type: "text", text: "Checking notes." },
        { type: "tool_use", id: "call-message", name: "tool_search", input: { query: "notes" } },
      ],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "claude",
      api: "messages",
      baseUrl: "https://api.anthropic.com/v1",
      apiKey: "anthropic-key",
      fetchFn,
    });

    const result = await provider.complete({
      traceId: "trace-messages",
      round: 1,
      messages: [{ role: "system", content: "Be concise" }, { role: "user", content: "Find notes" }],
      tools: [{ name: "tool_search", inputSchema: { type: "object" } }],
      model: "claude-sonnet-4",
    });

    expect(fetchFn).toHaveBeenCalledWith("https://api.anthropic.com/v1/messages", expect.objectContaining({
      headers: expect.objectContaining({ "x-api-key": "anthropic-key", "anthropic-version": "2023-06-01" }),
    }));
    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).toMatchObject({
      model: "claude-sonnet-4",
      system: "Be concise",
      tools: [{ name: "tool_search" }],
    });
    expect(result).toEqual({
      text: "Checking notes.",
      toolCalls: [{ id: "call-message", name: "tool_search", args: { query: "notes" } }],
      model: "claude-sonnet-4",
      providerId: "claude",
      api: "messages",
    });
  });

  it("allows an omitted default model when each turn supplies one", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "turn-model",
      choices: [{ message: { content: "ok" } }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      fetchFn,
    });

    await provider.complete({
      traceId: "trace-optional-default",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "turn-model",
    });

    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).toMatchObject({
      model: "turn-model",
    });
  });

  it("omits authorization for local gateways that do not require a key", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "local-model",
      choices: [{ message: { content: "ok" } }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "http://127.0.0.1:20128/v1",
      fetchFn,
    });

    await provider.complete({
      traceId: "trace-local",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "local-model",
    });

    expect(fetchFn.mock.calls[0]?.[1]?.headers).not.toHaveProperty("authorization");
  });

  it("omits reasoning effort when the picker uses automatic mode", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "model",
      choices: [{ message: { content: "ok" } }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      fetchFn,
    });

    await provider.complete({
      traceId: "trace-auto-effort",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "model",
      effort: "auto",
    });

    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).not.toHaveProperty("reasoning_effort");
  });

  it("explains that a model must be selected before a turn when no default exists", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      fetchFn: vi.fn<typeof fetch>(),
    });

    await expect(provider.complete({
      traceId: "trace-missing-model",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
    })).rejects.toThrow("Select a model");
  });

  it("maps MCP schemas and parses a native function tool call", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "picked-model",
      choices: [{ message: {
        content: null,
        tool_calls: [{
          id: "call-1",
          type: "function",
          function: { name: "mcp_notes_create_123", arguments: '{"title":"Roadmap"}' },
        }],
      } }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1/",
      apiKey: "secret-key",
      model: "gpt-test",
      fetchFn,
    });

    const result = await provider.complete({
      traceId: "trace-1",
      round: 1,
      messages: [{ role: "user", content: "Create a note" }],
      tools: [{
        name: "mcp_notes_create_123",
        description: "Create a note",
        inputSchema: { type: "object", properties: { title: { type: "string" } } },
      }],
      model: "gpt-test",
      effort: "high",
      modelCapabilities: {
        reasoningSupported: true,
        supportedEfforts: ["high"],
      },
    });

    expect(fetchFn).toHaveBeenCalledWith("https://provider.example/v1/chat/completions", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ authorization: "Bearer secret-key" }),
    }));
    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).toMatchObject({
      model: "gpt-test",
      reasoning_effort: "high",
      reasoning: { effort: "high" },
      tool_choice: "auto",
      tools: [{ type: "function", function: { name: "mcp_notes_create_123" } }],
    });
    expect(result).toEqual({
      toolCalls: [{ id: "call-1", name: "mcp_notes_create_123", args: { title: "Roadmap" } }],
      model: "picked-model",
      providerId: "openai-compatible",
      api: "chat",
    });
  });

  it("rejects malformed tool arguments before they can reach the MCP gateway", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      model: "gpt-test",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        choices: [{ message: {
          tool_calls: [{ id: "call-1", function: { name: "mcp_notes_create_123", arguments: "not-json" } }],
        } }],
      }), { status: 200 })),
    });

    await expect(provider.complete({
      traceId: "trace-1",
      round: 1,
      messages: [{ role: "user", content: "Create a note" }],
      tools: [],
    })).rejects.toThrow("invalid JSON tool arguments");
  });

  it("retries transient HTTP failures with Retry-After inside a bounded attempt budget", async () => {
    const waits: number[] = [];
    const fetchFn = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response("slow down", { status: 429, headers: { "retry-after": "2" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        model: "retry-model",
        choices: [{ message: { content: "recovered" } }],
      }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret",
      fetchFn,
      retry: {
        attemptBudget: 3,
        baseDelayMs: 100,
        maxDelayMs: 5000,
        jitter: 0,
        sleep: async (delayMs) => { waits.push(delayMs); },
      },
    });

    const result = await provider.complete({
      traceId: "trace-retry",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "retry-model",
    });

    expect(fetchFn).toHaveBeenCalledTimes(2);
    expect(waits).toEqual([2000]);
    expect(result.text).toBe("recovered");
  });

  it("does not retry non-transient provider failures", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response("invalid", { status: 400 }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret",
      fetchFn,
      retry: { attemptBudget: 4, baseDelayMs: 1, maxDelayMs: 2, jitter: 0 },
    });

    await expect(provider.complete({
      traceId: "trace-no-retry",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "retry-model",
    })).rejects.toThrow("HTTP 400");
    expect(fetchFn).toHaveBeenCalledTimes(1);
  });

  it("retries a 4xx image rejection once without image parts", async () => {
    const fetchFn = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response("image input is not supported", { status: 400 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        model: "text-only-model",
        choices: [{ message: { content: "I received the text." } }],
      }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret",
      fetchFn,
    });

    const result = await provider.complete({
      traceId: "trace-image-fallback",
      round: 1,
      model: "text-only-model",
      tools: [],
      messages: [{
        role: "user",
        content: [
          { type: "text", text: "What is in this image?" },
          { type: "image", name: "photo.png", dataUrl: "data:image/png;base64,YQ==" },
        ],
      }],
    });

    expect(fetchFn).toHaveBeenCalledTimes(2);
    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).toMatchObject({
      messages: [{ content: [
        { type: "text", text: "What is in this image?" },
        { type: "image_url" },
      ] }],
    });
    expect(JSON.parse(String(fetchFn.mock.calls[1]?.[1]?.body))).toMatchObject({
      messages: [{ content: [{ type: "text", text: "What is in this image?" }] }],
    });
    expect(result.text).toBe("I received the text.");
  });

  it("uses runtime model policy for effort, tools, and max output", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "reasoner",
      choices: [{ message: { content: "ok" } }],
    }), { status: 200 }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret",
      fetchFn,
    });

    await provider.complete({
      traceId: "trace-policy",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [{ name: "tool_search" }],
      model: "reasoner",
      effort: "xhigh",
      modelCapabilities: {
        maxOutput: 16_000,
        supportedEfforts: ["low", "medium", "high"],
        defaultEffort: "medium",
        reasoningSupported: true,
        supportsTools: false,
      },
    });

    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    expect(body).toMatchObject({
      max_tokens: 16_000,
      reasoning_effort: "high",
    });
    expect(body).not.toHaveProperty("tools");
    expect(body).not.toHaveProperty("tool_choice");
  });

  it("maps image data URLs to Chat and Responses content parts", async () => {
    const requests: Array<Record<string, unknown>> = [];
    const fetchFn = vi.fn<typeof fetch>().mockImplementation(async (_url, init) => {
      requests.push(JSON.parse(String(init?.body)));
      return new Response(JSON.stringify({
        model: "vision-model",
        choices: [{ message: { content: "seen" } }],
      }), { status: 200 });
    });
    const chat = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn,
    });
    await chat.complete({
      traceId: "trace-image-chat",
      round: 1,
      messages: [{
        role: "user",
        content: [
          { type: "text", text: "Describe this" },
          { type: "image", dataUrl: "data:image/png;base64,AAAA", name: "sample.png" },
        ],
      }],
      tools: [],
      model: "vision-model",
      modelCapabilities: { inputModes: ["text", "image"] },
    });

    const responsesFetch = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "vision-model",
      output: [{ type: "message", content: [{ type: "output_text", text: "seen" }] }],
    }), { status: 200 }));
    const responses = new OpenAiCompatibleAgentProvider({
      api: "responses",
      baseUrl: "https://provider.example/v1",
      fetchFn: responsesFetch,
    });
    await responses.complete({
      traceId: "trace-image-responses",
      round: 1,
      messages: [{
        role: "user",
        content: [
          { type: "text", text: "Describe this" },
          { type: "image", dataUrl: "data:image/png;base64,AAAA" },
        ],
      }],
      tools: [],
      model: "vision-model",
      modelCapabilities: { inputModes: ["text", "image"] },
    });

    expect(requests[0]).toMatchObject({
      messages: [{ role: "user", content: [
        { type: "text", text: "Describe this" },
        { type: "image_url", image_url: { url: "data:image/png;base64,AAAA" } },
      ] }],
    });
    expect(JSON.parse(String(responsesFetch.mock.calls[0]?.[1]?.body))).toMatchObject({
      input: [{ role: "user", content: [
        { type: "input_text", text: "Describe this" },
        { type: "input_image", image_url: "data:image/png;base64,AAAA" },
      ] }],
    });
  });

  it("parses reasoning, usage, content parts, and Responses chat fallbacks", async () => {
    const chatPayload = {
      object: "chat.completion",
      model: "proxy-model",
      choices: [{
        finish_reason: "stop",
        message: {
          content: [{ type: "text", text: "Visible answer" }],
          reasoning_content: "Private summary",
        },
      }],
      usage: {
        prompt_tokens: 10,
        completion_tokens: 7,
        prompt_tokens_details: { cached_tokens: 4 },
        completion_tokens_details: { reasoning_tokens: 3 },
      },
    };
    const provider = new OpenAiCompatibleAgentProvider({
      id: "proxy",
      api: "responses",
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(chatPayload), { status: 200 })),
    });

    const result = await provider.complete({
      traceId: "trace-fallback",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "proxy-model",
    });

    expect(result).toMatchObject({
      text: "Visible answer",
      reasoning: "Private summary",
      status: "stop",
      usage: {
        inputTokens: 10,
        outputTokens: 7,
        cachedInputTokens: 4,
        reasoningOutputTokens: 3,
      },
      providerId: "proxy",
      api: "responses",
    });
  });

  it("streams OpenAI SSE deltas and returns the assembled durable result", async () => {
    const deltas: string[] = [];
    const sse = [
      'data: {"id":"chat-1","model":"stream-model","choices":[{"delta":{"content":"Hel"}}]}',
      "",
      'data: {"id":"chat-1","model":"stream-model","choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-stream",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "stream-model",
      onTextDelta: (delta) => { deltas.push(delta); },
    });

    expect(deltas).toEqual(["Hel", "lo"]);
    expect(result).toMatchObject({ text: "Hello", model: "stream-model" });
  });

  it("streams reasoning_content deltas via onReasoningDelta", async () => {
    const reasoningDeltas: string[] = [];
    const sse = [
      'data: {"model":"m","choices":[{"delta":{"reasoning_content":"Let me think"}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"reasoning_content":" about this"}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"content":"Answer"},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-reasoning",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    expect(reasoningDeltas).toEqual(["Let me think", " about this"]);
    expect(result).toMatchObject({ text: "Answer", reasoning: "Let me think about this" });
  });

  it("streams reasoning from thinking field and thinking_content field", async () => {
    const reasoningDeltas: string[] = [];
    const sse = [
      'data: {"model":"m","choices":[{"delta":{"thinking":"hmm"}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"thinking_content":" let me see"}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-thinking",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    expect(reasoningDeltas).toEqual(["hmm", " let me see"]);
    expect(result).toMatchObject({ reasoning: "hmm let me see" });
  });

  it("streams reasoning from a separate event type", async () => {
    const reasoningDeltas: string[] = [];
    const sse = [
      "event: reasoning\ndata: {\"delta\":\"step 1\"}",
      "",
      "event: reasoning\ndata: {\"delta\":\" step 2\"}",
      "",
      'data: {"model":"m","choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-event-reasoning",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    expect(reasoningDeltas).toEqual(["step 1", " step 2"]);
    expect(result).toMatchObject({ reasoning: "step 1 step 2" });
  });

  it("streams reasoning from top-level field without choices", async () => {
    const reasoningDeltas: string[] = [];
    const sse = [
      'data: {"reasoning":"top-level thinking"}',
      "",
      'data: {"model":"m","choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-top-reasoning",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    expect(reasoningDeltas).toEqual(["top-level thinking"]);
    expect(result).toMatchObject({ reasoning: "top-level thinking" });
  });

  it("streams reasoning from array content blocks in delta", async () => {
    const reasoningDeltas: string[] = [];
    const sse = [
      'data: {"model":"m","choices":[{"delta":{"reasoning_content":[{"type":"text","text":"block "}]}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"reasoning_content":[{"type":"text","text":"thinking"}]}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"content":"result"},"finish_reason":"stop"}]}',
      "",
      "data: [DONE]",
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-array-reasoning",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    expect(reasoningDeltas).toEqual(["block ", "thinking"]);
    expect(result).toMatchObject({ reasoning: "block thinking" });
  });

  it("falls back once to JSON when a provider rejects streaming", async () => {
    const fetchFn = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        error: { message: "streaming is not supported" },
      }), { status: 400 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        model: "model",
        choices: [{ message: { content: "fallback" } }],
      }), { status: 200 }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn,
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-stream-fallback",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "model",
    });

    expect(fetchFn).toHaveBeenCalledTimes(2);
    expect(JSON.parse(String(fetchFn.mock.calls[1]?.[1]?.body))).toMatchObject({ stream: false });
    expect(result.text).toBe("fallback");
  });

  it("falls back from responses to chat when the responses endpoint is not supported", async () => {
    const fetchFn = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response("Not Found", { status: 404 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        model: "gateway-model",
        choices: [{ message: { content: "chat fallback ok" } }],
      }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "omniroute",
      api: "responses",
      baseUrl: "http://127.0.0.1:20128/v1",
      fetchFn,
    });

    const result = await provider.complete({
      traceId: "trace-responses-fallback",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "gateway-model",
    });

    expect(fetchFn).toHaveBeenCalledTimes(2);
    expect(fetchFn.mock.calls[0]?.[0]).toBe("http://127.0.0.1:20128/v1/responses");
    expect(fetchFn.mock.calls[1]?.[0]).toBe("http://127.0.0.1:20128/v1/chat/completions");
    expect(result).toMatchObject({ text: "chat fallback ok", providerId: "omniroute", api: "chat" });
  });

  it("aborts a request at the configured timeout", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      timeoutMs: 5,
      fetchFn: vi.fn<typeof fetch>().mockImplementation(async (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
        })),
    });

    await expect(provider.complete({
      traceId: "trace-timeout",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "model",
    })).rejects.toThrow(/timed out/i);
  });
});
