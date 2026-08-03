import { describe, expect, it } from "vitest";
import {
  CANVAS_ARTIFACT_MAX_SOURCE_BYTES,
  canvasArtifactId,
  canvasKindForLang,
  extractCanvasCandidates,
} from "../src/renderer/agent-canvas-detect.js";

describe("extractCanvasCandidates", () => {
  it("returns nothing for markdown without canvas fences", () => {
    expect(extractCanvasCandidates("Just prose and a `js` code span.")).toEqual([]);
    expect(extractCanvasCandidates("```js\nconsole.log(1)\n```")).toEqual([]);
  });

  it("extracts a single svg fence with fenceIndex 0", () => {
    const md = "Here is a diagram:\n\n```svg\n<svg><circle/></svg>\n```\n";
    const candidates = extractCanvasCandidates(md);
    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({
      kind: "svg",
      fenceIndex: 0,
      source: "<svg><circle/></svg>\n",
      tooLarge: false,
    });
  });

  it("treats htm as html and is case-insensitive about the lang", () => {
    const md = "```HTM\n<div>hi</div>\n```";
    const candidates = extractCanvasCandidates(md);
    expect(candidates).toHaveLength(1);
    expect(candidates[0]).toMatchObject({ kind: "html", lang: "HTM" });
  });

  it("indexes only canvas fences in document order across mixed fences", () => {
    const md = [
      "Intro text.",
      "```js\nconst x = 1\n```",
      "```mermaid\nflowchart LR\n  A-->B\n```",
      "More text.",
      "```svg\n<svg/>\n```",
      "```html\n<p>hi</p>\n```",
    ].join("\n\n");
    const candidates = extractCanvasCandidates(md);
    expect(candidates.map((c) => `${c.kind}@${c.fenceIndex}`)).toEqual([
      "mermaid@0",
      "svg@1",
      "html@2",
    ]);
  });

  it("handles an unterminated fence at end of input by taking the rest", () => {
    const md = "```svg\n<svg/>\n";
    const candidates = extractCanvasCandidates(md);
    expect(candidates).toHaveLength(1);
    expect(candidates[0].source).toBe("<svg/>\n");
  });

  it("ignores backticks that are not at line start", () => {
    const md = "Use ```svg``` inline, not as a fence.";
    expect(extractCanvasCandidates(md)).toEqual([]);
  });

  it("flags a candidate tooLarge when the body exceeds the 512KB cap", () => {
    const big = "x".repeat(CANVAS_ARTIFACT_MAX_SOURCE_BYTES + 1);
    const md = `\`\`\`svg\n${big}\n\`\`\``;
    const candidates = extractCanvasCandidates(md);
    expect(candidates).toHaveLength(1);
    expect(candidates[0].tooLarge).toBe(true);
  });

  it("canvasKindForLang resolves html/htm/svg/mermaid and rejects others", () => {
    expect(canvasKindForLang("html")).toBe("html");
    expect(canvasKindForLang("htm")).toBe("html");
    expect(canvasKindForLang("svg")).toBe("svg");
    expect(canvasKindForLang("mermaid")).toBe("mermaid");
    expect(canvasKindForLang("Mermaid")).toBe("mermaid");
    expect(canvasKindForLang("js")).toBeNull();
    expect(canvasKindForLang("")).toBeNull();
  });

  it("canvasArtifactId is stable across reloads", () => {
    expect(canvasArtifactId("conv-1", "3", 0)).toBe("conv-1:3:0");
    expect(canvasArtifactId("conv-1", "3", 0)).toBe(canvasArtifactId("conv-1", "3", 0));
  });
});
