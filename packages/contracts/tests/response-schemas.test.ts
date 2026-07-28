import { describe, expect, it } from "vitest";
import {
  SuccessResponseSchema,
  ErrorResponseSchema,
  ResponseSchema,
  PluginStartResultSchema,
  PluginListResultSchema,
  ToolCallResultSchema,
} from "../src/index.js";

describe("Response schemas", () => {
  it("parses a success response", () => {
    const result = SuccessResponseSchema.safeParse({
      kind: "response",
      id: "req_001",
      ok: true,
      result: { pluginId: "com.example.notes", state: "running" },
    });
    expect(result.success).toBe(true);
  });

  it("parses an error response", () => {
    const result = ErrorResponseSchema.safeParse({
      kind: "response",
      id: "req_001",
      ok: false,
      error: { code: "PLUGIN_NOT_FOUND", message: "Plugin not found" },
    });
    expect(result.success).toBe(true);
  });

  it("discriminates by ok field", () => {
    const success = ResponseSchema.safeParse({
      kind: "response",
      id: "req_001",
      ok: true,
      result: {},
    });
    expect(success.success).toBe(true);

    const error = ResponseSchema.safeParse({
      kind: "response",
      id: "req_001",
      ok: false,
      error: { code: "INTERNAL_ERROR", message: "fail" },
    });
    expect(error.success).toBe(true);
  });

  it("parses plugin.start result", () => {
    const result = PluginStartResultSchema.safeParse({
      pluginId: "com.example.notes",
      state: "running",
    });
    expect(result.success).toBe(true);
  });

  it("parses plugin.list result", () => {
    const result = PluginListResultSchema.safeParse({
      plugins: [
        { pluginId: "com.example.a", name: "A", version: "1.0.0", icon: "📝", installPath: "/plugins/a", state: "idle", enabled: true, autostart: false },
        { pluginId: "com.example.b", name: "B", version: "2.0.0", icon: "🤖", installPath: "/plugins/b", state: "running", enabled: true, autostart: true },
      ],
    });
    expect(result.success).toBe(true);
  });

  it("parses tool.call result", () => {
    const result = ToolCallResultSchema.safeParse({
      requestId: "req-uuid-001",
      result: { text: "hello" },
    });
    expect(result.success).toBe(true);
  });

  it("rejects invalid plugin state", () => {
    const result = PluginStartResultSchema.safeParse({
      pluginId: "com.example.notes",
      state: "unknown",
    });
    expect(result.success).toBe(false);
  });
});
