import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const shellUxCss = readFileSync(new URL("../src/renderer/styles/shell-ux.css", import.meta.url), "utf8");
const agentCss = readFileSync(new URL("../src/renderer/styles/agent.css", import.meta.url), "utf8");
const agentMarkup = readFileSync(new URL("../src/renderer/index.html", import.meta.url), "utf8");
const conversationController = readFileSync(new URL("../src/renderer/agent-conversation-controller.js", import.meta.url), "utf8");

describe("agent model picker layout", () => {
  it("does not clip the popup at the composer's minimum width", () => {
    expect(shellUxCss).toMatch(/\.agent-composer-stack\s*\{[\s\S]*?overflow:\s*visible;/);
    expect(shellUxCss).toMatch(/\.agent-composer-stack\s*\{[\s\S]*?z-index:\s*\d+;/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?z-index:\s*26;/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?width:\s*min\(560px,\s*calc\(100vw - 32px\)\);/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?left:\s*auto;[\s\S]*?right:\s*0;/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?max-width:\s*calc\(100vw - 32px\);/);
    expect(agentCss).toMatch(/\.agent-model-name\s*\{[\s\S]*?white-space:\s*normal;[\s\S]*?overflow-wrap:\s*anywhere;/);
  });

  it("keeps room diagnostics and markdown tables readable when the room is narrow", () => {
    expect(shellUxCss).toMatch(/\.agent-room-info-metric,\s*\.agent-room-info-id\s*\{[\s\S]*?flex:\s*0 0 auto;/);
    expect(shellUxCss).toMatch(/\.agent-message\.assistant \.agent-bubble th,\s*\.agent-message\.assistant \.agent-bubble td\s*\{[\s\S]*?word-break:\s*normal;/);
    expect(shellUxCss).toMatch(/\.agent-message\.assistant \.agent-bubble th,\s*\.agent-message\.assistant \.agent-bubble td\s*\{[\s\S]*?overflow-wrap:\s*break-word;/);
  });

  it("keeps room diagnostics compact behind an accessible popover", () => {
    expect(agentMarkup).toMatch(/class="agent-room-info-trigger"[^>]*aria-controls="agent-room-info-popover"/);
    expect(agentMarkup).toMatch(/class="agent-room-info-trigger"[^>]*aria-label="Room info"[^>]*title="Room info"/);
    expect(agentMarkup).not.toMatch(/class="agent-room-info-trigger-label"/);
    expect(agentMarkup).toMatch(/id="agent-room-info-popover"[^>]*role="dialog"[^>]*hidden/);
    expect(shellUxCss).toMatch(/\.agent-room-info\s*\{[\s\S]*?position:\s*absolute;/);
    expect(shellUxCss).toMatch(/\.agent-room-info-popover\s*\{[\s\S]*?position:\s*absolute;/);
    expect(conversationController).toMatch(/toggleRoomInfo\(/);
    expect(conversationController).toMatch(/agent-room-info-trigger/);
  });

  it("throttles live Mermaid preview to completed fences", () => {
    expect(conversationController).toMatch(/scheduleStreamingCanvasEnhancement\(streamState, ownerConversationId\)/);
    expect(conversationController).toMatch(/candidate\.complete && candidate\.kind === "mermaid"/);
    expect(conversationController).toMatch(/window\.setTimeout\(\(\) => \{/);
    expect(conversationController).toMatch(/}, 350\);/);
    expect(conversationController).toMatch(/canvasRenderCache/);
  });
});
