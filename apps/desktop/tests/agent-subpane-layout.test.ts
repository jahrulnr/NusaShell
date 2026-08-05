import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const agentCss = readFileSync(new URL("../src/renderer/styles/agent.css", import.meta.url), "utf8");

describe("subagent pane markdown layout", () => {
  it("gives tables their own horizontal scroll area", () => {
    expect(agentCss).toMatch(/\.agent-subpane-text \.agent-markdown-table-scroll\s*\{[\s\S]*?max-width:\s*100%;[\s\S]*?overflow-x:\s*auto;/);
    expect(agentCss).toMatch(/\.agent-subpane-text \.agent-markdown-table-scroll\s*\{[\s\S]*?min-width:\s*0;/);
    expect(agentCss).toMatch(/\.agent-subpane-text \.agent-markdown-table-scroll > table\s*\{[\s\S]*?width:\s*max-content;[\s\S]*?max-width:\s*none;/);
  });
});
