import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const markup = readFileSync(new URL("../src/renderer/index.html", import.meta.url), "utf8");
const shellUxCss = readFileSync(new URL("../src/renderer/styles/shell-ux.css", import.meta.url), "utf8");
const controller = readFileSync(new URL("../src/renderer/agent-conversation-controller.js", import.meta.url), "utf8");

describe("agent responsive conversation navigation", () => {
  it("keeps room switching available when the conversation sidebar collapses", () => {
    expect(markup).toMatch(/id="agent-mobile-conversations-btn"/);
    expect(markup).toMatch(/id="agent-mobile-conversations-overlay"/);
    expect(shellUxCss).toMatch(/\.agent-mobile-conversations-btn\s*\{[\s\S]*?display:\s*none;/);
    expect(shellUxCss).toMatch(/@media \(max-width: 760px\) \{[\s\S]*?\.agent-mobile-conversations-btn\s*\{[\s\S]*?display:\s*inline-flex;/);
    expect(shellUxCss).toMatch(/\.agent-shell\.is-conversations-open \.agent-conversations\s*\{[\s\S]*?display:\s*flex;/);
    expect(controller).toMatch(/toggleMobileConversations\(/);
  });
});
