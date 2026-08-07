import { describe, expect, it } from "vitest";
import {
  tokenizeQuery,
  scoreTool,
  rankToolsByTokens,
  TOOL_SEARCH_MAX_MATCHES,
  TOOL_SEARCH_ZERO_HIT_HINT,
  type ToolDiscoveryHit,
} from "../src/agent/services/tool-discovery-match.js";

describe("tokenizeQuery", () => {
  it("splits on whitespace and lowercases", () => {
    expect(tokenizeQuery("Read File LIST")).toEqual(["read", "file", "list"]);
  });

  it("drops empty tokens from leading/trailing/double spaces", () => {
    expect(tokenizeQuery("  read   file  ")).toEqual(["read", "file"]);
  });

  it("returns [] for empty or whitespace-only query", () => {
    expect(tokenizeQuery("")).toEqual([]);
    expect(tokenizeQuery("   ")).toEqual([]);
  });
});

describe("scoreTool", () => {
  it("returns 0 when no token hits", () => {
    expect(scoreTool({ name: "read", description: "Read a file" }, ["xyz"])).toBe(0);
  });

  it("returns 0 for empty tokens (empty query)", () => {
    expect(scoreTool({ name: "read", description: "Read a file" }, [])).toBe(0);
  });

  it("scores name hits +3 per token", () => {
    expect(scoreTool({ name: "read" }, ["read"])).toBe(3);
    expect(scoreTool({ name: "read" }, ["read", "disk"])).toBe(3);
  });

  it("scores description hits +1 per token", () => {
    expect(scoreTool({ name: "x", description: "read a file from disk" }, ["read"])).toBe(1);
  });

  it("combines name + description scores", () => {
    // "list" in name (+3) + "list" in description (+1) = 4
    expect(scoreTool({ name: "list", description: "List files in a directory" }, ["list"])).toBe(4);
  });
});

describe("rankToolsByTokens", () => {
  const tools: ToolDiscoveryHit[] = [
    { name: "list", description: "List files in a directory" },
    { name: "read", description: "Read a file from disk" },
    { name: "exec", description: "Run a shell command" },
    { name: "create", description: "Create a note" },
  ];

  it("excludes zero-score tools", () => {
    const ranked = rankToolsByTokens(tools, ["file"]);
    const names = ranked.map((t) => t.name);
    expect(names).toContain("list");
    expect(names).toContain("read");
    expect(names).not.toContain("exec");
    expect(names).not.toContain("create");
  });

  it("sorts by score desc then name asc", () => {
    // "file" → list: desc+1 = 1; read: desc+1 = 1
    // Both score 1 → sorted by name asc: list, read
    const ranked = rankToolsByTokens(tools, ["file"]);
    expect(ranked[0]!.name).toBe("list");
    expect(ranked[1]!.name).toBe("read");
  });

  it("name-only hit ranks above description-only hit", () => {
    const mixed: ToolDiscoveryHit[] = [
      { name: "x_tool", description: "search for files" }, // desc only: +1
      { name: "files_y", description: "unrelated" }, // name only: +3
    ];
    const ranked = rankToolsByTokens(mixed, ["files"]);
    expect(ranked[0]!.name).toBe("files_y"); // score 3 > 1
    expect(ranked[1]!.name).toBe("x_tool");
  });

  it("returns [] for empty tokens", () => {
    expect(rankToolsByTokens(tools, [])).toEqual([]);
  });

  it("returns [] when no tools match", () => {
    expect(rankToolsByTokens(tools, ["zzznonexistent"])).toEqual([]);
  });
});

describe("constants", () => {
  it("TOOL_SEARCH_MAX_MATCHES is 20", () => {
    expect(TOOL_SEARCH_MAX_MATCHES).toBe(20);
  });

  it("TOOL_SEARCH_ZERO_HIT_HINT is non-empty and mentions success", () => {
    expect(TOOL_SEARCH_ZERO_HIT_HINT.length).toBeGreaterThan(0);
    expect(TOOL_SEARCH_ZERO_HIT_HINT).toContain("Empty matches are success");
  });
});
