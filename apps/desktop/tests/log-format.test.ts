import { describe, expect, it } from "vitest";
import { formatLogArguments } from "../src/main/log-format.js";

describe("main-process log formatting", () => {
  it("preserves nested Error details received from the backend logger", () => {
    const error = Object.assign(new Error("spawn node ENOENT"), { code: "ENOENT" });

    const message = formatLogArguments([{ err: error }, "Plugin startup failed"]);

    expect(message).toContain("spawn node ENOENT");
    expect(message).toContain("ENOENT");
    expect(message).toContain("Plugin startup failed");
  });

  it("keeps error details when an Error cause is circular", () => {
    const error = new Error("plugin process crashed");
    error.cause = error;

    const message = formatLogArguments([{ err: error }]);

    expect(message).toContain("plugin process crashed");
    expect(message).toContain("[Circular]");
  });

  it("interpolates %s/%d placeholders in message-first pino shape", () => {
    const message = formatLogArguments([
      "Agent MCP tool failed traceId=%s tool=%s round=%d",
      "e6364134-92f9-406e-89f7-6c538d290d30",
      "mcp_enable",
      2,
    ]);

    expect(message).toBe("Agent MCP tool failed traceId=e6364134-92f9-406e-89f7-6c538d290d30 tool=mcp_enable round=2");
    expect(message).not.toContain("%s");
    expect(message).not.toContain("%d");
  });

  it("interpolates placeholders in bindings-first pino shape", () => {
    const message = formatLogArguments([
      { pluginId: "nusashell.notes" },
      "Plugin startup failed traceId=%s",
      "abc-123",
    ]);

    expect(message).toContain('"pluginId":"nusashell.notes"');
    expect(message).toContain("traceId=abc-123");
    expect(message).not.toContain("%s");
  });

  it("leaves leftover placeholders when fewer args than % tokens", () => {
    const message = formatLogArguments(["traceId=%s tool=%s round=%d", "abc-123"]);

    expect(message).toBe("traceId=abc-123 tool=%s round=%d");
  });
});
