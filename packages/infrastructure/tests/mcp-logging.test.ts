import type { Client } from "@modelcontextprotocol/sdk/client/index.js";
import type { Logger } from "pino";
import { describe, expect, it, vi } from "vitest";
import { registerMcpLogging } from "../src/mcp/mcp-logging.js";

describe("MCP protocol logging", () => {
  it("routes server notifications by severity and redacts bounded data", () => {
    let handler: ((notification: {
      params: { level: "warning"; logger?: string; data: unknown };
    }) => void) | undefined;
    const client = {
      setNotificationHandler: vi.fn((_schema, callback) => {
        handler = callback;
      }),
    } as unknown as Client;
    const logger = {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    } as unknown as Logger;

    registerMcpLogging(client, logger, "com.example.notes");
    handler?.({
      params: {
        level: "warning",
        logger: "notes",
        data: { api_key: "secret-value", message: "x".repeat(5000) },
      },
    });

    expect(logger.warn).toHaveBeenCalledOnce();
    const fields = (logger.warn as ReturnType<typeof vi.fn>).mock.calls[0]?.[0] as {
      message: string;
      source: string;
    };
    expect(fields.source).toBe("com.example.notes");
    expect(fields.message).not.toContain("secret-value");
    expect(fields.message).toContain("[REDACTED]");
    expect(fields.message.length).toBeLessThanOrEqual(4000);
  });
});
