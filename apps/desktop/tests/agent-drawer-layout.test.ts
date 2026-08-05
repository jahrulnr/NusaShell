import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const agentCss = readFileSync(new URL("../src/renderer/styles/agent.css", import.meta.url), "utf8");

describe("agent drawer layout", () => {
  it("keeps both drawers and their backdrops below the NusaShell titlebar", () => {
    for (const selector of [".agent-canvas-overlay", ".agent-subpane-overlay"]) {
      const block = agentCss.match(new RegExp(`${selector}\\s*\\{([\\s\\S]*?)\\}`))?.[1] ?? "";
      expect(block).toMatch(/top:\s*56px;/);
      expect(block).toMatch(/right:\s*0;/);
      expect(block).toMatch(/bottom:\s*0;/);
      expect(block).toMatch(/left:\s*0;/);
    }

    for (const selector of [".agent-canvas", ".agent-subpane"]) {
      const block = agentCss.match(new RegExp(`${selector}\\s*\\{([\\s\\S]*?)\\}`))?.[1] ?? "";
      expect(block).toMatch(/top:\s*56px;/);
      expect(block).toMatch(/right:\s*0;/);
      expect(block).toMatch(/bottom:\s*0;/);
    }
  });

  it("treats drawers as keyboard modals and restores the opener focus", () => {
    expect(agentCss).toMatch(/\.agent-(canvas|subpane)\s*\{[\s\S]*?pointer-events:\s*none;/);
    expect(agentCss).toMatch(/\.agent-(canvas|subpane)\.is-open\s*\{[\s\S]*?pointer-events:\s*auto;/);
    expect(agentCss).toMatch(/\.agent-subpane/);
  });
});
