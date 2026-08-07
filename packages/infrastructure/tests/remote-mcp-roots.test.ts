import { describe, it, expect } from "vitest";
import { HttpMcpClient } from "../src/mcp/http-mcp-client.adapter.js";
import { SseMcpClient } from "../src/mcp/sse-mcp-client.adapter.js";
import type { McpClientPort } from "@nusashell/application";

/**
 * #10 — remote (HTTP streamable / SSE) MCP clients must expose workspace-root
 * support equal to stdio (setRoots / notifyRootsChanged / rootsRequested),
 * advertise the `roots` capability so servers can query roots, and answer a
 * `roots/list` request with the configured roots.
 */

describe("remote MCP client workspace roots (#10)", () => {
  it("exposes setRoots/notifyRootsChanged/rootsRequested on HttpMcpClient", () => {
    const client: McpClientPort = new HttpMcpClient("http://127.0.0.1:1/mcp");
    expect(typeof (client as { setRoots?: unknown }).setRoots).toBe("function");
    expect(typeof (client as { notifyRootsChanged?: unknown }).notifyRootsChanged).toBe("function");
    expect(typeof (client as { rootsRequested?: unknown }).rootsRequested).toBe("function");
  });

  it("exposes setRoots/notifyRootsChanged/rootsRequested on SseMcpClient", () => {
    const client: McpClientPort = new SseMcpClient("http://127.0.0.1:1/sse");
    expect(typeof (client as { setRoots?: unknown }).setRoots).toBe("function");
    expect(typeof (client as { notifyRootsChanged?: unknown }).notifyRootsChanged).toBe("function");
    expect(typeof (client as { rootsRequested?: unknown }).rootsRequested).toBe("function");
  });

  it("rootsRequested() is false before any server roots/list, and setRoots does not flip it", () => {
    const client = new HttpMcpClient("http://127.0.0.1:1/mcp") as McpClientPort & {
      setRoots: (roots: readonly unknown[]) => void;
      rootsRequested: () => boolean;
    };
    expect(client.rootsRequested()).toBe(false);
    client.setRoots([{ uri: "file:///workspace" }]);
    // Setting roots locally must not itself count as a server request.
    expect(client.rootsRequested()).toBe(false);
  });

  it("notifyRootsChanged does not throw when not connected", async () => {
    const client = new SseMcpClient("http://127.0.0.1:1/sse") as McpClientPort & {
      notifyRootsChanged: () => Promise<void>;
      setRoots: (roots: readonly unknown[]) => void;
    };
    client.setRoots([{ uri: "file:///workspace" }]);
    await client.notifyRootsChanged();
  });
});
