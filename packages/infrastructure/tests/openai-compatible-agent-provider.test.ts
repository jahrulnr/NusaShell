import { describe, expect, it, vi } from "vitest";
import { OpenAiCompatibleAgentProvider } from "../src/index.js";
import { RuntimeHydrationBuilder } from "@nusashell/application";

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
      modelCapabilities: {
        supportedEfforts: ["low", "medium", "high"],
        reasoningSupported: true,
      },
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

  it("marks only the stable system prefix for Anthropic prompt caching", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "claude-sonnet-4",
      content: [{ type: "text", text: "ok" }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "claude",
      api: "messages",
      baseUrl: "https://api.anthropic.com/v1",
      apiKey: "anthropic-key",
      fetchFn,
    });

    await provider.complete({
      traceId: "trace-cache",
      round: 1,
      messages: [
        { role: "system", content: "stable instructions" },
        { role: "system", content: "stable tools" },
        { role: "system", content: "dynamic workspace" },
        { role: "user", content: "hello" },
      ],
      tools: [],
      model: "claude-sonnet-4",
      promptCache: { mode: "auto", ttl: "1h", stableSystemMessages: 2 },
    });

    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    expect(body.system).toEqual([
      { type: "text", text: "stable instructions" },
      { type: "text", text: "stable tools", cache_control: { type: "ephemeral", ttl: "1h" } },
      { type: "text", text: "dynamic workspace" },
    ]);
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

  it("does not send a cache routing key when caching is disabled", async () => {
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
      traceId: "trace-cache-off",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "model",
      promptCache: { mode: "off", key: "conversation-key" },
    });

    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).not.toHaveProperty("prompt_cache_key");
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
          function: { name: "mcp_create_123", arguments: '{"title":"Roadmap"}' },
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
        name: "mcp_create_123",
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
      thinking: { type: "enabled" },
      reasoning_effort: "high",
      reasoning: { effort: "high" },
      tool_choice: "auto",
      tools: [{ type: "function", function: { name: "mcp_create_123" } }],
    });
    expect(result).toEqual({
      toolCalls: [{ id: "call-1", name: "mcp_create_123", args: { title: "Roadmap" } }],
      model: "picked-model",
      providerId: "openai-compatible",
      api: "chat",
    });
  });

  it("recovers fenced tool arguments with a trailing comma", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      model: "gpt-test",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        choices: [{ message: {
          tool_calls: [{ id: "call-1", function: { name: "mcp_create_123", arguments: "```json\n{\"title\":\"Roadmap\",}\n```" } }],
        } }],
      }), { status: 200 })),
    });

    await expect(provider.complete({
      traceId: "trace-1",
      round: 1,
      messages: [{ role: "user", content: "Create a note" }],
      tools: [],
    })).resolves.toMatchObject({
      toolCalls: [{ id: "call-1", name: "mcp_create_123", args: { title: "Roadmap" } }],
    });
  });

  it("returns an invalid tool-call marker instead of failing the whole provider response", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      model: "gpt-test",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        choices: [{ message: {
          tool_calls: [{ id: "call-1", function: { name: "mcp_create_123", arguments: "not-json" } }],
        } }],
      }), { status: 200 })),
    });

    await expect(provider.complete({
      traceId: "trace-1",
      round: 1,
      messages: [{ role: "user", content: "Create a note" }],
      tools: [],
    })).resolves.toMatchObject({
      toolCalls: [{
        id: "call-1",
        name: "mcp_create_123",
        args: {},
        argumentError: {
          code: "TOOL_ARGUMENTS_INVALID_JSON",
        },
      }],
    });
  });

  it("returns an invalid tool-call marker from the Responses API too", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      api: "responses",
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      model: "gpt-test",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        output: [{
          type: "function_call",
          call_id: "call-response-invalid",
          name: "mcp_create_123",
          arguments: "{title: 'Roadmap'}",
        }],
      }), { status: 200 })),
    });

    await expect(provider.complete({
      traceId: "trace-1",
      round: 1,
      messages: [{ role: "user", content: "Create a note" }],
      tools: [],
    })).resolves.toMatchObject({
      toolCalls: [{
        id: "call-response-invalid",
        name: "mcp_create_123",
        args: {},
        argumentError: { code: "TOOL_ARGUMENTS_INVALID_JSON" },
      }],
    });
  });

  it("returns an invalid tool-call marker from the Messages API", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      api: "messages",
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
      model: "claude-test",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        content: [{ type: "tool_use", id: "call-message-invalid", name: "mcp_create_123", input: ["not-an-object"] }],
      }), { status: 200 })),
    });

    await expect(provider.complete({
      traceId: "trace-1",
      round: 1,
      messages: [{ role: "user", content: "Create a note" }],
      tools: [],
    })).resolves.toMatchObject({
      toolCalls: [{
        id: "call-message-invalid",
        name: "mcp_create_123",
        args: {},
        argumentError: { code: "TOOL_ARGUMENTS_INVALID_JSON" },
      }],
    });
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

  it("treats billing exhaustion on HTTP 429 as a permanent failure", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(
      JSON.stringify({ error: { code: "1113", message: "Insufficient balance or no resource package. Please recharge." } }),
      { status: 429 },
    ));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://api.z.ai/api/paas/v4",
      apiKey: "secret",
      fetchFn,
      retry: { attemptBudget: 4, baseDelayMs: 1, maxDelayMs: 2, jitter: 0 },
    });

    await expect(provider.complete({
      traceId: "trace-billing",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "glm-5.2",
    })).rejects.toMatchObject({
      name: "AgentProviderHttpError",
      status: 429,
      transient: false,
    });
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
      thinking: { type: "enabled" },
      reasoning_effort: "high",
    });
    expect(body).not.toHaveProperty("tools");
    expect(body).not.toHaveProperty("tool_choice");
  });

  it("omits thinking/reasoning_effort in auto when catalog has no effort levels", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "glm-5.2",
      choices: [{ message: { content: "ok", reasoning_content: "plan" } }],
    }), { status: 200 }));
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://api.z.ai/api/paas/v4",
      apiKey: "secret",
      fetchFn,
    });

    const result = await provider.complete({
      traceId: "trace-glm",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "glm-5.2",
      effort: "auto",
      modelCapabilities: {
        reasoningSupported: false,
        supportedEfforts: [],
        defaultEffort: "auto",
      },
    });

    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    expect(body).not.toHaveProperty("thinking");
    expect(body).not.toHaveProperty("reasoning_effort");
    expect(body).not.toHaveProperty("reasoning");
    // Provider may still stream reasoning_content even when we did not request effort.
    expect(result).toMatchObject({ text: "ok", reasoning: "plan" });
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

  it("streams OpenRouter reasoning_details summaries as reasoning, not assistant text", async () => {
    const reasoningDeltas: string[] = [];
    const textDeltas: string[] = [];
    const sse = [
      'data: {"model":"m","choices":[{"delta":{"reasoning_details":[{"type":"reasoning.summary","summary":"Inspecting the tool result. "}]}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"I need one more check."}]}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"content":"Done."},"finish_reason":"stop"}]}',
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
      traceId: "trace-reasoning-details",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onTextDelta: (delta) => { textDeltas.push(delta); },
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    expect(reasoningDeltas).toEqual(["Inspecting the tool result. ", "I need one more check."]);
    expect(textDeltas).toEqual(["Done."]);
    expect(result).toMatchObject({ reasoning: "Inspecting the tool result. I need one more check.", text: "Done." });
  });

  it("maps non-stream OpenRouter reasoning_details without exposing encrypted blocks", async () => {
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
        model: "m",
        choices: [{
          message: {
            content: "Done.",
            reasoning_details: [
              { type: "reasoning.summary", summary: "Checked the result. " },
              { type: "reasoning.text", text: "The path is valid." },
              { type: "reasoning.encrypted", data: "opaque" },
            ],
          },
          finish_reason: "stop",
        }],
      }), { status: 200 })),
      stream: false,
    });

    const result = await provider.complete({
      traceId: "trace-non-stream-reasoning-details",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
    });

    expect(result).toMatchObject({ reasoning: "Checked the result. The path is valid.", text: "Done." });
    expect(result.reasoning).not.toContain("opaque");
  });

  it("removes known model control tokens from streamed and final assistant text", async () => {
    const textDeltas: string[] = [];
    const sse = [
      'data: {"model":"m","choices":[{"delta":{"content":"<|begin_of_sentence|>Visible"}}]}',
      "",
      'data: {"model":"m","choices":[{"delta":{"content":" answer<|end_of_sentence|>"},"finish_reason":"stop"}]}',
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
      traceId: "trace-control-token",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "m",
      onTextDelta: (delta) => { textDeltas.push(delta); },
    });

    expect(textDeltas).toEqual(["Visible", " answer"]);
    expect(result).toMatchObject({ text: "Visible answer" });
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

  it("omits tool_choice for local providers that reject it (ollama/llamacpp)", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "llama3.2",
      choices: [{ message: { content: "ok" } }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "ollama",
      api: "chat",
      baseUrl: "http://127.0.0.1:11434/v1",
      fetchFn,
      omitToolChoice: true,
    });

    await provider.complete({
      traceId: "trace-ollama",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [{ name: "tool_search", inputSchema: { type: "object" } }],
      model: "llama3.2",
    });

    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    expect(body.tools).toHaveLength(1);
    expect(body).not.toHaveProperty("tool_choice");
  });

  it("sends tool_choice by default for standard OpenAI-compatible providers", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "model",
      choices: [{ message: { content: "ok" } }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "openrouter",
      api: "chat",
      baseUrl: "https://openrouter.ai/api/v1",
      fetchFn,
    });

    await provider.complete({
      traceId: "trace-default",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [{ name: "tool_search", inputSchema: { type: "object" } }],
      model: "model",
    });

    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    expect(body.tool_choice).toBe("auto");
  });

  it("streams Messages API (Anthropic) text + thinking deltas live", async () => {
    const textDeltas: string[] = [];
    const reasoningDeltas: string[] = [];
    const sse = [
      'event: message_start',
      'data: {"type":"message_start","message":{"model":"claude-sonnet-4","usage":{"input_tokens":10}}}',
      "",
      'event: content_block_start',
      'data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"Let me"}}',
      "",
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me"}}',
      "",
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" think"}}',
      "",
      'event: content_block_stop',
      'data: {"type":"content_block_stop","index":0}',
      "",
      'event: content_block_start',
      'data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}',
      "",
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hel"}}',
      "",
      'event: content_block_delta',
      'data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"lo"}}',
      "",
      'event: content_block_stop',
      'data: {"type":"content_block_stop","index":1}',
      "",
      'event: message_delta',
      'data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}',
      "",
      'event: message_stop',
      'data: {"type":"message_stop"}',
      "",
    ].join("\n");
    const provider = new OpenAiCompatibleAgentProvider({
      id: "claude",
      api: "messages",
      baseUrl: "https://api.anthropic.com/v1",
      apiKey: "key",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-messages-stream",
      round: 1,
      messages: [{ role: "user", content: "Hello" }],
      tools: [],
      model: "claude-sonnet-4",
      onTextDelta: (delta) => { textDeltas.push(delta); },
      onReasoningDelta: (delta) => { reasoningDeltas.push(delta); },
    });

    // Live deltas fire during streaming (the bug: previously no streaming
    // for Messages API, so deltas never fired and thinking only appeared
    // after the turn completed).
    expect(reasoningDeltas).toEqual(["Let me", " think"]);
    expect(textDeltas).toEqual(["Hel", "lo"]);
    expect(result).toMatchObject({
      text: "Hello",
      reasoning: "Let me think",
      model: "claude-sonnet-4",
      api: "messages",
    });
  });

  it("streams Messages API tool_use (input_json_delta)", async () => {
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
      id: "p",
      api: "messages",
      baseUrl: "https://api.example/v1",
      apiKey: "k",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(sse, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
    });

    const result = await provider.complete({
      traceId: "trace-messages-tool-stream",
      round: 1,
      messages: [{ role: "user", content: "Search" }],
      tools: [{ name: "search", inputSchema: { type: "object" } }],
      model: "m",
    });

    expect(result.toolCalls).toEqual([{ id: "call_1", name: "search", args: { q: "notes" } }]);
  });

  // --- Idle stream timeout (Cycle 3) ---

  /** Build a ReadableStream that emits SSE chunks on a schedule. Each entry
   * is { data, delayMs } — the stream waits delayMs before pushing data. */
  function scheduledSseStream(chunks: readonly { data: string; delayMs: number }[]): ReadableStream<Uint8Array> {
    const encoder = new TextEncoder();
    return new ReadableStream({
      async start(controller) {
        for (const chunk of chunks) {
          if (chunk.delayMs > 0) await new Promise((r) => setTimeout(r, chunk.delayMs));
          controller.enqueue(encoder.encode(chunk.data));
        }
        controller.close();
      },
    });
  }

  it("idle timeout: stream stalls longer than timeoutMs → fails", async () => {
    // Emit one chunk quickly, then stall > timeoutMs before the next.
    const timeoutMs = 50;
    const stream = scheduledSseStream([
      { data: 'data: {"id":"c1","model":"m","choices":[{"delta":{"content":"Hi"}}]}\n\n', delayMs: 0 },
      { data: 'data: {"id":"c1","model":"m","choices":[{"delta":{"content":"there"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n', delayMs: timeoutMs * 4 },
    ]);
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(stream, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
      timeoutMs,
    });

    const error = await provider.complete({
      traceId: "trace-idle",
      round: 1,
      messages: [{ role: "user", content: "Hi" }],
      tools: [],
      model: "m",
    }).catch((e) => e);

    expect(error).toBeInstanceOf(Error);
    expect(String(error.message ?? error)).toContain("timed out");
  });

  it("idle timeout: continuous chunks spanning > timeoutMs total still succeeds", async () => {
    // Proves wall-clock was removed: total stream time > timeoutMs, but each
    // chunk arrives well under the idle threshold.
    const timeoutMs = 80;
    const chunkData = 'data: {"id":"c1","model":"m","choices":[{"delta":{"content":"x"}}]}\n\n';
    const doneData = 'data: {"id":"c1","model":"m","choices":[{"delta":{"content":""},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n';
    // 5 chunks at 30ms each = 150ms total (> 80ms timeout), but each gap < 80ms.
    const chunks = Array.from({ length: 5 }, () => ({ data: chunkData, delayMs: 30 }));
    chunks.push({ data: doneData, delayMs: 30 });
    const stream = scheduledSseStream(chunks);
    const provider = new OpenAiCompatibleAgentProvider({
      baseUrl: "https://provider.example/v1",
      fetchFn: vi.fn<typeof fetch>().mockResolvedValue(new Response(stream, {
        status: 200,
        headers: { "content-type": "text/event-stream" },
      })),
      stream: true,
      timeoutMs,
    });

    const result = await provider.complete({
      traceId: "trace-long-stream",
      round: 1,
      messages: [{ role: "user", content: "Write" }],
      tools: [],
      model: "m",
    });

    expect(result.text).toBe("xxxxx");
  });

  // --- Malformed 200 response classification ---

  it("classifies a 200 response with no choices as a non-transient AgentProviderHttpError with body snippet", async () => {
    const malformedBody = JSON.stringify({ id: "x", model: "m", choices: [], error: { message: "model not available" } });
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(malformedBody, {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "blackbox",
      baseUrl: "https://provider.example/v1",
      apiKey: "k",
      fetchFn,
      stream: false,
    });

    const error = await provider.complete({
      traceId: "trace-empty-choices",
      round: 1,
      messages: [{ role: "user", content: "Hi" }],
      tools: [],
      model: "m",
    }).catch((e) => e);

    expect(error).toBeInstanceOf(Error);
    expect(error.name).toBe("AgentProviderHttpError");
    expect(error.transient).toBe(false);
    expect(String(error.message)).toContain("model not available");
  });

  it("classifies a 200 response with a non-array choices field as a non-transient AgentProviderHttpError", async () => {
    const malformedBody = JSON.stringify({ id: "x", model: "m", choices: null });
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(malformedBody, {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "blackbox",
      baseUrl: "https://provider.example/v1",
      apiKey: "k",
      fetchFn,
      stream: false,
    });

    const error = await provider.complete({
      traceId: "trace-null-choices",
      round: 1,
      messages: [{ role: "user", content: "Hi" }],
      tools: [],
      model: "m",
    }).catch((e) => e);

    expect(error.name).toBe("AgentProviderHttpError");
    expect(error.transient).toBe(false);
  });
});

describe("RuntimeHydrationBuilder serializes the synthetic transcript on every wire strategy (REV2)", () => {
  async function buildTranscript() {
    const builder = new RuntimeHydrationBuilder({
      mcpLive: {
        running: [{ pluginId: "nusashell.files" }],
        tools: [{
          providerName: "mcp_nusashell_files_read",
          pluginId: "nusashell.files",
          toolName: "read",
          description: "Read a file",
          inputSchema: { type: "object", properties: { path: { type: "string" } } },
        }],
      },
    });
    return builder.build({ nonce: "test" });
  }

  it("chat: keeps 4 tool_calls + 4 tool results, user first (Option B)", async () => {
    const { messages } = await buildTranscript();
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "gpt",
      choices: [{ message: { role: "assistant", content: "ok" }, finish_reason: "stop" }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "chat", api: "chat", baseUrl: "https://x/v1", apiKey: "k", fetchFn, stream: false,
    });
    await provider.complete({
      traceId: "t", round: 1,
      messages: [{ role: "user", content: "hi" }, ...messages],
      tools: [{ name: "mcp_list", inputSchema: { type: "object" } }],
      model: "gpt",
    });
    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    const wireMessages = body.messages;
    expect(wireMessages[0].role).toBe("user");
    const assistant = wireMessages.find((m: { role: string }) => m.role === "assistant");
    expect(assistant.tool_calls.length).toBe(5);
    const toolResults = wireMessages.filter((m: { role: string }) => m.role === "tool");
    expect(toolResults.length).toBe(5);
    expect(toolResults.every((r: { tool_call_id: string }) => r.tool_call_id.startsWith("hydrate:test:"))).toBe(true);
  });

  it("responses: keeps 4 function_call + 4 function_call_output, user first (Option B)", async () => {
    const { messages } = await buildTranscript();
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "gpt", output: [{ type: "message", content: [{ type: "output_text", text: "ok" }] }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "responses", api: "responses", baseUrl: "https://x/v1", apiKey: "k", fetchFn, stream: false,
    });
    await provider.complete({
      traceId: "t", round: 1,
      messages: [{ role: "user", content: "hi" }, ...messages],
      tools: [{ name: "mcp_list", inputSchema: { type: "object" } }],
      model: "gpt",
    });
    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    const input = body.input;
    const fnCalls = input.filter((i: { type: string }) => i.type === "function_call");
    const fnOutputs = input.filter((i: { type: string }) => i.type === "function_call_output");
    expect(input[0].role).toBe("user");
    expect(fnCalls.length).toBe(5);
    expect(fnOutputs.length).toBe(5);
    expect(fnCalls.every((c: { call_id: string }) => c.call_id.startsWith("hydrate:test:"))).toBe(true);
  });

  it("messages: keeps 4 tool_use + 4 tool_result, user first (Option B)", async () => {
    const { messages } = await buildTranscript();
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "claude", content: [{ type: "text", text: "ok" }],
    }), { status: 200, headers: { "content-type": "application/json" } }));
    const provider = new OpenAiCompatibleAgentProvider({
      id: "claude", api: "messages", baseUrl: "https://anthropic/v1", apiKey: "k", fetchFn, stream: false,
    });
    await provider.complete({
      traceId: "t", round: 1,
      messages: [{ role: "user", content: "hi" }, ...messages],
      tools: [{ name: "mcp_list", inputSchema: { type: "object" } }],
      model: "claude",
    });
    const body = JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body));
    const wireMessages: Array<{ role: string; content?: unknown }> = body.messages;
    // Option B: first real user message carries the initial user turn.
    const first = wireMessages[0];
    expect(first?.role).toBe("user");
    const assistant = wireMessages.find((m) => m.role === "assistant") as { content: Array<{ type: string; id?: string }> } | undefined;
    const toolUses = (assistant?.content ?? []).filter((b) => b.type === "tool_use");
    expect(toolUses.length).toBe(5);
    const toolResults = wireMessages.filter((m) =>
      m.role === "user" && m.content && Array.isArray(m.content) && (m.content as Array<{ type: string }>).some((b) => b.type === "tool_result"),
    );
    expect(toolResults.length).toBe(5);
    expect(toolUses.every((u) => u.id ? u.id.startsWith("hydrate:test:") : false)).toBe(true);
  });
});
