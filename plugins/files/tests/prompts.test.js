import { describe, expect, it } from "vitest";
import { FILES_PROMPTS, getFilesPrompt } from "../mcp/prompts.js";

describe("Files MCP prompts", () => {
  it("publishes a howto prompt with the Files constraints", () => {
    expect(FILES_PROMPTS).toEqual([
      expect.objectContaining({ name: "howto" }),
    ]);
    const prompt = getFilesPrompt("howto");
    const text = prompt.messages[0].content.text;
    expect(text).toContain("files_read");
    expect(text).toContain("files_patch");
    expect(text).toContain("not the OS filesystem root");
  });

  it("rejects unknown prompts", () => {
    expect(() => getFilesPrompt("missing")).toThrow("Unknown prompt");
  });
});
