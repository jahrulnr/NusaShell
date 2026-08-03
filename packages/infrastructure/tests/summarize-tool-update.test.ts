import { describe, expect, it } from "vitest";
import { summarizeToolUpdate } from "../src/acp/acp-json-rpc-client.js";

describe("summarizeToolUpdate", () => {
  it("prefers diff path from Cursor edit updates", () => {
    expect(summarizeToolUpdate({
      content: [{
        type: "diff",
        path: "/tmp/acp-cursor-workspace/smoke-marker.txt",
        oldText: "-- /dev/null",
        newText: "++ nusa",
      }],
    })).toBe("/tmp/acp-cursor-workspace/smoke-marker.txt");
  });

  it("uses stdout from rawOutput execute updates", () => {
    expect(summarizeToolUpdate({
      rawOutput: { exitCode: 0, stdout: "memory-gate: pass\n", stderr: "" },
    })).toBe("memory-gate: pass");
  });

  it("falls back to stderr when stdout empty", () => {
    expect(summarizeToolUpdate({
      rawOutput: { exitCode: 127, stdout: "", stderr: "command not found\n" },
    })).toBe("command not found");
  });
});
