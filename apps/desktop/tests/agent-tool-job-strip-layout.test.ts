import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const shellUxCss = readFileSync(new URL("../src/renderer/styles/shell-ux.css", import.meta.url), "utf8");
const agentMarkup = readFileSync(new URL("../src/renderer/index.html", import.meta.url), "utf8");

describe("background tool job strip layout", () => {
  it("keeps active jobs compact and readable above the composer", () => {
    expect(agentMarkup).toMatch(/id="agent-tool-job-list"[^>]*role="list"[^>]*aria-live="off"/);
    expect(shellUxCss).toMatch(/\.agent-tool-job-card-head\s*\{[\s\S]*?display:\s*grid;/);
    expect(shellUxCss).toMatch(/\.agent-tool-job-card-actions\s*\{[\s\S]*?display:\s*inline-flex;/);
    expect(shellUxCss).toMatch(/\.agent-tool-job-card-name\s*\{[\s\S]*?text-overflow:\s*ellipsis;/);
    expect(shellUxCss).toMatch(/\.agent-tool-job-card-tail\s*\{[\s\S]*?max-height:\s*72px;/);
    expect(shellUxCss).toMatch(/\.agent-tool-job-card-name\s*\{[\s\S]*?word-break:\s*normal;/);
  });
});
