import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const shellUxCss = readFileSync(new URL("../src/renderer/styles/shell-ux.css", import.meta.url), "utf8");

describe("agent attention strip layout", () => {
  it("collapses recorded decisions like the task strip", () => {
    expect(shellUxCss).toMatch(/\.agent-attention-stack:not\(\.has-pending\) \.agent-attention-list\s*\{[\s\S]*?display:\s*none;/);
    expect(shellUxCss).toMatch(/\.agent-attention-stack:not\(\.has-pending\) \.agent-attention-head\s*\{[\s\S]*?margin-bottom:\s*0;/);
    expect(shellUxCss).not.toMatch(/\.agent-attention-list \.agent-ask-card\.is-sealed/);
  });
});
