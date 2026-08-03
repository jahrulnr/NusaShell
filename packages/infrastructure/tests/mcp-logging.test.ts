import type { Client } from "@modelcontextprotocol/sdk/client/index.js";
import type { Logger } from "pino";
import { describe, expect, it, vi } from "vitest";
import { boundMcpStderr, boundMcpText, registerMcpLogging } from "../src/mcp/mcp-logging.js";

describe("MCP protocol logging", () => {
  it("routes server notifications by severity and truncates bounded data", () => {
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

    registerMcpLogging(client, logger, "nusashell.notes");
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
    expect(fields.source).toBe("nusashell.notes");
    // No pattern scrubbing — secrets pass through verbatim (truncation only).
    expect(fields.message).toContain("secret-value");
    expect(fields.message).not.toContain("[REDACTED]");
    expect(fields.message.length).toBeLessThanOrEqual(4000);
  });
});

describe("boundMcpText", () => {
  it("truncates to 4000 chars", () => {
    expect(boundMcpText("x".repeat(5000)).length).toBe(4000);
    expect(boundMcpText("short")).toBe("short");
  });

  it("passes secrets and base64 through verbatim (no pattern scrubbing)", () => {
    expect(boundMcpText('{"token":"abc123"}')).toBe('{"token":"abc123"}');
    expect(boundMcpText('{"password":"hunter2"}')).toBe('{"password":"hunter2"}');
    expect(boundMcpText('{"tokenCount":1500}')).toBe('{"tokenCount":1500}');
    expect(boundMcpText("Bearer abc123def456ghi789jkl012mno345"))
      .toBe("Bearer abc123def456ghi789jkl012mno345");
    expect(boundMcpText("sk-1234567890abcdefghijklmnopqrstuvwxyz"))
      .toBe("sk-1234567890abcdefghijklmnopqrstuvwxyz");
  });
});

describe("boundMcpStderr", () => {
  it("preserves JSON-RPC frames and stack traces from crash dumps", () => {
    const stderr = [
      '[stdin]:1',
      '{"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"nusashell-backend","version":"0.0.2"}},"jsonrpc":"2.0","id":0}',
      ' ^',
      '',
      'SyntaxError: Unexpected token \':\'',
      '    at makeContextifyScript (node:internal/vm:185:14)',
      '    at evalScript (node:internal/process/execution:133:3)',
      'Node.js v20.18.3',
    ].join("\n");

    const out = boundMcpStderr(stderr);
    expect(out).toContain('"method":"initialize"');
    expect(out).toContain('"clientInfo"');
    expect(out).toContain("SyntaxError: Unexpected token");
    expect(out).toContain("node:internal/vm:185:14");
    expect(out).toContain("Node.js v20.18.3");
  });

  it("passes Bearer tokens and Authorization headers through verbatim", () => {
    const stderr = 'Authorization: Bearer abc123def456ghi789jkl012mno345';
    const out = boundMcpStderr(stderr);
    expect(out).toBe(stderr);
  });

  it("passes sk- prefixed API keys through verbatim", () => {
    const stderr = 'Using key: sk-1234567890abcdefghijklmnopqrstuvwxyz';
    const out = boundMcpStderr(stderr);
    expect(out).toBe(stderr);
  });

  it("passes non-secret key-value pairs through verbatim", () => {
    const stderr = '{"path":"/media/jahrulnr/storage/workspace/NusaShell/tmp/plan/foo.md","tokenCount":1500}';
    const out = boundMcpStderr(stderr);
    expect(out).toBe(stderr);
  });

  it("caps stderr at 8192 chars but preserves the start", () => {
    const stderr = "x".repeat(20000);
    const out = boundMcpStderr(stderr);
    expect(out.length).toBe(8192);
  });
});
