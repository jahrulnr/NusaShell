import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const shellUxCss = readFileSync(new URL("../src/renderer/styles/shell-ux.css", import.meta.url), "utf8");
const agentCss = readFileSync(new URL("../src/renderer/styles/agent.css", import.meta.url), "utf8");
const agentMarkup = readFileSync(new URL("../src/renderer/index.html", import.meta.url), "utf8");
const conversationController = readFileSync(new URL("../src/renderer/agent-conversation-controller.js", import.meta.url), "utf8");

describe("agent model picker layout", () => {
  it("lays out the model picker as a fixed-position popover, not a clipped anchor", () => {
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?position:\s*fixed;/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?z-index:\s*26;/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?width:\s*min\(560px,\s*calc\(100vw - 32px\)\);/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?max-width:\s*calc\(100vw - 32px\);/);
    expect(agentMarkup).not.toMatch(/agent-model-menu[^>]*position:/);
  });

  it("keeps the picker inside the viewport with smart vertical flip and horizontal clamp", () => {
    // position:fixed removes the old CSS-only vertical (bottom:100%) and
    // horizontal (right:0) anchoring that tied the popover to the trigger corner.
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?bottom:\s*auto;/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?left:\s*auto;?/);
    expect(agentCss).toMatch(/\.agent-model-menu\s*\{[\s\S]*?right:\s*auto;?/);
  });

  it("keeps room diagnostics and markdown tables readable when the room is narrow", () => {
    expect(shellUxCss).toMatch(/\.agent-room-info-metric,\s*\.agent-room-info-id\s*\{[\s\S]*?flex:\s*0 0 auto;/);
    expect(shellUxCss).toMatch(/\.agent-message\.assistant \.agent-bubble th,\s*\.agent-message\.assistant \.agent-bubble td\s*\{[\s\S]*?word-break:\s*normal;/);
    expect(shellUxCss).toMatch(/\.agent-message\.assistant \.agent-bubble th,\s*\.agent-message\.assistant \.agent-bubble td\s*\{[\s\S]*?overflow-wrap:\s*break-word;/);
  });

  it("keeps room diagnostics compact behind an accessible popover", () => {
    expect(agentMarkup).toMatch(/class="agent-room-info-trigger"[^>]*aria-controls="agent-room-info-popover"/);
    expect(agentMarkup).toMatch(/class="agent-room-info-trigger"[^>]*aria-label="Room info"[^>]*title="Room info"/);
    expect(agentMarkup).toMatch(/id="agent-room-info-popover"[^>]*role="dialog"[^>]*hidden/);
    expect(conversationController).toMatch(/toggleRoomInfo\(/);
    expect(conversationController).toMatch(/agent-room-info-trigger/);
  });
});
