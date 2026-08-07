import { describe, expect, it } from "vitest";
import {
  successToolResult,
  errorToolResult,
  cancelledToolResult,
  timeoutToolResult,
  fromGatewayValue,
  fromThrownError,
  ingestMcpToolResult,
  projectModelToolResult,
  truncateToolResultText,
} from "../src/agent/services/agent-tool-result.js";

describe("AgentToolResult canonical model", () => {
  // --- Factories ---

  describe("successToolResult", () => {
    it("creates a success result with structured content", () => {
      const result = successToolResult("call-1", "mcp_nusashell_files_read", { path: "/a", content: "hi" });
      expect(result.callId).toBe("call-1");
      expect(result.toolName).toBe("mcp_nusashell_files_read");
      expect(result.status).toBe("success");
      expect(result.content).toEqual([{ type: "json", data: { path: "/a", content: "hi" } }]);
      expect(result.metadata.truncated).toBe(false);
      expect(result.metadata.dataIsUntrusted).toBe(true);
      expect(result.error).toBeUndefined();
    });

    it("creates a success result with text content", () => {
      const result = successToolResult("call-2", "mcp_nusashell_terminal_exec", "hello world\n");
      expect(result.status).toBe("success");
      expect(result.content).toEqual([{ type: "text", text: "hello world\n" }]);
      expect(result.metadata.dataIsUntrusted).toBe(true);
    });

    it("non-mcp tool has dataIsUntrusted=false", () => {
      const result = successToolResult("call-3", "tool_list", { count: 5 });
      expect(result.metadata.dataIsUntrusted).toBe(false);
    });
  });

  describe("errorToolResult", () => {
    it("creates an error result with code and message", () => {
      const result = errorToolResult("call-1", "mcp_nusashell_files_read", "ENOENT", "File not found");
      expect(result.status).toBe("error");
      expect(result.error).toEqual({ code: "ENOENT", message: "File not found", retryable: false });
      expect(result.content).toEqual([]);
    });

    it("marks retryable when specified", () => {
      const result = errorToolResult("call-1", "mcp_nusashell_mail_search", "TIMEOUT", "Mail timeout", true);
      expect(result.error?.retryable).toBe(true);
    });
  });

  describe("cancelledToolResult", () => {
    it("creates a cancelled result", () => {
      const result = cancelledToolResult("call-1", "mcp_nusashell_terminal_exec");
      expect(result.status).toBe("cancelled");
      expect(result.error?.code).toBe("TOOL_CANCELLED");
    });
  });

  describe("timeoutToolResult", () => {
    it("creates a timeout result", () => {
      const result = timeoutToolResult("call-1", "mcp_nusashell_terminal_exec");
      expect(result.status).toBe("timeout");
      expect(result.error?.code).toBe("TOOL_TIMEOUT");
    });
  });

  // --- fromGatewayValue (meta-tools) ---

  describe("fromGatewayValue", () => {
    it("wraps a plain object result from meta-tools", () => {
      const result = fromGatewayValue(
        { id: "call-1", name: "tool_list", args: { pluginId: "nusashell.notes" } },
        { pluginId: "nusashell.notes", count: 3, tools: [] },
      );
      expect(result.status).toBe("success");
      expect(result.content).toEqual([{ type: "json", data: { pluginId: "nusashell.notes", count: 3, tools: [] } }]);
      expect(result.metadata.dataIsUntrusted).toBe(false);
    });

    it("wraps a string result as text content", () => {
      const result = fromGatewayValue(
        { id: "call-2", name: "docs_search", args: { query: "howto" } },
        "Found 2 results",
      );
      expect(result.content).toEqual([{ type: "text", text: "Found 2 results" }]);
    });
  });

  // --- fromThrownError ---

  describe("fromThrownError", () => {
    it("maps abort signal to cancelled", () => {
      const result = fromThrownError(
        { id: "call-1", name: "mcp_nusashell_terminal_exec" },
        new Error("The operation was aborted"),
      );
      expect(result.status).toBe("cancelled");
      expect(result.error?.code).toBe("TOOL_CANCELLED");
    });

    it("maps timeout message to timeout", () => {
      const result = fromThrownError(
        { id: "call-2", name: "mcp_nusashell_terminal_exec" },
        new Error("Provider request timed out"),
      );
      expect(result.status).toBe("timeout");
      expect(result.error?.code).toBe("TOOL_TIMEOUT");
    });

    it("maps generic error to error status", () => {
      const result = fromThrownError(
        { id: "call-3", name: "mcp_nusashell_files_read" },
        new Error("ENOENT: file not found"),
      );
      expect(result.status).toBe("error");
      expect(result.error?.code).toBe("TOOL_FAILED");
    });
  });

  // --- ingestMcpToolResult ---

  describe("ingestMcpToolResult", () => {
    it("ingests MCP success with structuredContent", () => {
      const ingested = ingestMcpToolResult({
        content: [{ type: "text", text: '{"path":"/a"}' }],
        structuredContent: { path: "/a" },
      });
      expect(ingested.kind).toBe("ok");
      if (ingested.kind === "ok") {
        expect(ingested.structuredContent).toEqual({ path: "/a" });
        expect(ingested.content).toHaveLength(1);
      }
    });

    it("ingests MCP success with content only (no structuredContent)", () => {
      const ingested = ingestMcpToolResult({
        content: [{ type: "text", text: "Command output here" }],
      });
      expect(ingested.kind).toBe("ok");
      if (ingested.kind === "ok") {
        expect(ingested.structuredContent).toBeUndefined();
        expect(ingested.content[0]).toEqual({ type: "text", text: "Command output here" });
      }
    });

    it("ingests MCP isError as error kind (does NOT throw)", () => {
      const ingested = ingestMcpToolResult({
        isError: true,
        content: [{ type: "text", text: "Permission denied" }],
      });
      expect(ingested.kind).toBe("error");
      if (ingested.kind === "error") {
        expect(ingested.message).toBe("Permission denied");
        expect(ingested.content).toHaveLength(1);
      }
    });

    it("ingests empty MCP result as ok with empty content", () => {
      const ingested = ingestMcpToolResult({});
      expect(ingested.kind).toBe("ok");
      if (ingested.kind === "ok") {
        expect(ingested.content).toEqual([]);
        expect(ingested.structuredContent).toBeUndefined();
      }
    });
  });

  // --- projectModelToolResult ---

  describe("projectModelToolResult", () => {
    it("projects structured content as deterministic JSON", () => {
      const result = successToolResult("call-1", "mcp_nusashell_files_read", { path: "/a", content: "hi" });
      const projected = projectModelToolResult(result);
      expect(projected).toContain('"ok":true');
      expect(projected).toContain('"data"');
      expect(projected).toContain('"path":"/a"');
      expect(projected).toContain('"meta"');
      expect(projected).toContain('"truncated":false');
    });

    it("projects error as JSON error envelope", () => {
      const result = errorToolResult("call-1", "mcp_nusashell_files_read", "ENOENT", "File not found");
      const projected = projectModelToolResult(result);
      expect(projected).toContain('"ok":false');
      expect(projected).toContain('"error"');
      expect(projected).toContain('"code":"ENOENT"');
      expect(projected).toContain('"message":"File not found"');
    });

    it("projects text content as labeled text envelope", () => {
      const result = successToolResult("call-1", "mcp_nusashell_terminal_exec", "hello\nworld\n");
      const projected = projectModelToolResult(result);
      expect(projected).toContain("Status: success");
      expect(projected).toContain("hello\nworld");
    });

    it("projects cancelled as error envelope with TOOL_CANCELLED code", () => {
      const result = cancelledToolResult("call-1", "mcp_nusashell_terminal_exec");
      const projected = projectModelToolResult(result);
      expect(projected).toContain('"ok":false');
      expect(projected).toContain('"TOOL_CANCELLED"');
    });

    it("stores modelOutput on result after projection", () => {
      const result = successToolResult("call-1", "mcp_nusashell_files_read", { path: "/a" });
      const projected = projectModelToolResult(result);
      expect(result.modelOutput).toBe(projected);
    });
  });

  // --- truncateToolResultText ---

  describe("truncateToolResultText", () => {
    it("head+tail truncates long text with explicit omit marker", () => {
      const long = "A".repeat(5000);
      const truncated = truncateToolResultText(long, 200);
      expect(truncated.length).toBeLessThanOrEqual(200);
      expect(truncated).toContain("[omitted:");
      expect(truncated).toContain("A"); // head present
      expect(truncated.startsWith("A")).toBe(true);
    });

    it("does not truncate short text", () => {
      expect(truncateToolResultText("short", 200)).toBe("short");
    });

    it("head+tail preserves both start and end of text", () => {
      const text = "HEAD" + "x".repeat(500) + "TAIL";
      const truncated = truncateToolResultText(text, 100);
      expect(truncated).toContain("HEAD");
      expect(truncated).toContain("TAIL");
      expect(truncated).toContain("[omitted:");
    });
  });

  // --- Golden projection snapshots (stability) ---

  describe("golden projection snapshots", () => {
    it("structured success projection is stable", () => {
      const result = successToolResult("call-1", "mcp_nusashell_files_read", { path: "/a.txt", content: "hi" });
      expect(projectModelToolResult(result)).toBe(
        '{"ok":true,"data":{"path":"/a.txt","content":"hi"},"meta":{"truncated":false}}',
      );
    });

    it("error projection is stable", () => {
      const result = errorToolResult("call-1", "mcp_nusashell_files_read", "ENOENT", "File not found");
      expect(projectModelToolResult(result)).toBe(
        '{"ok":false,"error":{"code":"ENOENT","message":"File not found","retryable":false}}',
      );
    });

    it("cancelled projection is stable", () => {
      const result = cancelledToolResult("call-1", "mcp_nusashell_terminal_exec");
      expect(projectModelToolResult(result)).toBe(
        '{"ok":false,"error":{"code":"TOOL_CANCELLED","message":"Tool call was cancelled","retryable":false}}',
      );
    });

    it("timeout projection is stable", () => {
      const result = timeoutToolResult("call-1", "mcp_nusashell_terminal_exec");
      expect(projectModelToolResult(result)).toBe(
        '{"ok":false,"error":{"code":"TOOL_TIMEOUT","message":"Tool call timed out","retryable":true}}',
      );
    });

    it("text/command projection is stable", () => {
      const result = successToolResult("call-1", "mcp_nusashell_terminal_exec", "hello\nworld\n");
      const projected = projectModelToolResult(result);
      expect(projected).toBe("Status: success\n\nOutput:\nhello\nworld\n");
    });
  });

  // --- Size regression: projection vs raw ---

  describe("size regression", () => {
    it("structured projection is smaller than raw JSON.stringify for fat payloads", () => {
      const fat = { entries: Array.from({ length: 200 }, (_, i) => ({ path: `docs/item-${i}.json`, blob: "x".repeat(40) })) };
      const result = successToolResult("call-1", "mcp_nusashell_files_read", fat);
      const projected = projectModelToolResult(result);
      const raw = JSON.stringify({ ok: true, result: fat });
      // Projection includes meta overhead but should be in the same ballpark.
      expect(projected.length).toBeLessThan(raw.length + 100);
    });
  });
});
