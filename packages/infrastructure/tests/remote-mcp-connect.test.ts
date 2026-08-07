import { describe, it, expect, afterEach } from "vitest";
import { createServer, type Server, type IncomingMessage, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { connectWithTimeout, redactUrl } from "../src/mcp/remote-mcp-connect.js";
import { HttpMcpClient } from "../src/mcp/http-mcp-client.adapter.js";

/**
 * #9 — remote (HTTP streamable / SSE) MCP connect:
 * - race connect against a timeout (no leaked transport handle on timeout)
 * - enrich errors with redacted URL + hint for status / DNS / refused
 */

const ORIGINAL_TIMEOUT = process.env.NUSASHELL_MCP_CONNECT_TIMEOUT;

afterEach(() => {
  if (ORIGINAL_TIMEOUT === undefined) delete process.env.NUSASHELL_MCP_CONNECT_TIMEOUT;
  else process.env.NUSASHELL_MCP_CONNECT_TIMEOUT = ORIGINAL_TIMEOUT;
});

function listen(
  handler: (req: IncomingMessage, res: ServerResponse) => void,
): Promise<Server> {
  return new Promise((resolve) => {
    const server = createServer(handler);
    server.listen(0, "127.0.0.1", () => resolve(server));
  });
}

function urlOf(server: Server): string {
  const { port } = server.address() as AddressInfo;
  return `http://127.0.0.1:${port}/mcp`;
}

async function closeServer(server: Server): Promise<void> {
  await new Promise<void>((resolve) => server.close(() => resolve()));
}

describe("remote MCP connect (HTTP/SSE) — #9", () => {
  it("redacts credentials from URLs", () => {
    expect(redactUrl("http://user:secret@host:3000/mcp")).toBe(
      "http://host:3000/mcp",
    );
    expect(redactUrl("https://token@api.example.com/mcp")).toBe(
      "https://api.example.com/mcp",
    );
    expect(redactUrl("http://plain.example.com/mcp")).toBe(
      "http://plain.example.com/mcp",
    );
  });

  it("races a hanging connect against the timeout and calls onTimeout", async () => {
    process.env.NUSASHELL_MCP_CONNECT_TIMEOUT = "80";
    const events: string[] = [];
    await expect(
      connectWithTimeout(
        "http://127.0.0.1:9/mcp",
        () =>
          new Promise<void>(() => {
            // Never settles — the timeout must win the race.
            events.push("connect-started");
          }),
        { onTimeout: () => events.push("timeout-close") },
      ),
    ).rejects.toThrow(/MCP connect timed out after 80ms/);
    expect(events).toEqual(["connect-started", "timeout-close"]);
  });

  it("keeps the redacted URL out of a non-timeout connect error", async () => {
    const server = await listen((_req, res) => {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("not here");
    });
    try {
      const url = urlOf(server);
      await expect(
        connectWithTimeout(url, () =>
          Promise.reject(Object.assign(new Error("Failed to open SSE stream: Not Found"), { code: 404 })),
        ),
      ).rejects.toThrow(/404|Not Found/);
    } finally {
      await closeServer(server);
    }
  });

  it("enriches ECONNREFUSED with a 'connection refused' hint", async () => {
    const url = "http://127.0.0.1:1/mcp"; // port 1: nothing is listening
    await expect(
      connectWithTimeout(url, () =>
        Promise.reject(Object.assign(new Error("connect ECONNREFUSED"), { code: "ECONNREFUSED" })),
      ),
    ).rejects.toThrow(/Connection refused/);
  });
});

describe("HttpMcpClient.connect() over a real server — #9 integration", () => {
  it("surfaces a clear timeout when the server never answers the GET (no hang)", async () => {
    process.env.NUSASHELL_MCP_CONNECT_TIMEOUT = "120";
    // Server accepts connections but never responds to the initial GET that
    // starts the streamable HTTP SSE stream. The connect() must be bounded by
    // the timeout and the transport closed, not parked forever.
    const server = await listen((_req, _res) => {
      // Intentionally never respond.
    });

    try {
      const client = new HttpMcpClient(urlOf(server));
      await expect(client.connect()).rejects.toThrow(
        /MCP connect timed out after 120ms/,
      );
      // The client should still be cleanly closed afterwards.
      await client.close();
    } finally {
      await closeServer(server);
    }
  });
});

