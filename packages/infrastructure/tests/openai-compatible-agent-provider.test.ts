import { describe, expect, it, vi } from "vitest";
import { OpenAiCompatibleAgentProvider } from "../src/index.js";

describe("OpenAiCompatibleAgentProvider", () => {
  it("maps MCP schemas and parses a native function tool call", async () => {
    const fetchFn = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({
      model: "gpt-test",
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
    });

    expect(fetchFn).toHaveBeenCalledWith("https://provider.example/v1/chat/completions", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({ authorization: "Bearer secret-key" }),
    }));
    expect(JSON.parse(String(fetchFn.mock.calls[0]?.[1]?.body))).toMatchObject({
      model: "gpt-test",
      tool_choice: "auto",
      tools: [{ type: "function", function: { name: "mcp_notes_create_123" } }],
    });
    expect(result).toEqual({
      toolCalls: [{ id: "call-1", name: "mcp_notes_create_123", args: { title: "Roadmap" } }],
      model: "gpt-test",
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
});
