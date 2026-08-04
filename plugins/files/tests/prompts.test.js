import { describe, expect, it } from "vitest";
import { FILES_PROMPTS, getFilesPrompt } from "../mcp/prompts.js";

describe("Files MCP prompts", () => {
  it("publishes a howto prompt with the Files constraints", () => {
    expect(FILES_PROMPTS).toEqual([
      expect.objectContaining({ name: "howto" }),
      expect.objectContaining({ name: "explore-workflow" }),
    ]);
    const prompt = getFilesPrompt("howto");
    const text = prompt.messages[0].content.text;
    expect(text).toContain("files_read");
    expect(text).toContain("files_patch");
    expect(text).toContain("files_exists");
    expect(text).toContain("files_touch");
    expect(text).toContain("OS filesystem root");
  });

  it("publishes an explore-workflow prompt with the recommended sequence", () => {
    const prompt = getFilesPrompt("explore-workflow");
    const text = prompt.messages[0].content.text;
    expect(text).toContain("files_tree");
    expect(text).toContain("files_search");
    expect(text).toContain("files_grep");
    expect(text).toContain("files_patch");
    expect(text).toContain("preview=true");
    expect(text).toContain("exclude");
  });

  it("rejects unknown prompts", () => {
    expect(() => getFilesPrompt("missing")).toThrow("Unknown prompt");
  });
});
