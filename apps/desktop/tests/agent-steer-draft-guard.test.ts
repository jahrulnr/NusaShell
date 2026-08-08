// @vitest-environment jsdom
/**
 * Ticket #69 — Completion steering must not overwrite an unsent composer draft
 * (or steal the textarea during IME composition). Empty-composer auto-continue
 * remains unchanged.
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <form id="agent-form">
      <textarea id="agent-input" rows="1"></textarea>
      <div id="agent-attachments"></div>
      <button id="agent-send-btn" type="submit"></button>
      <button id="agent-stop-btn" hidden></button>
      <span id="agent-provider-status"></span>
    </form>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
}

function makeController() {
  const log = vi.fn();
  const controller = new AgentConversationController({
    shell: { agentConversations: {} },
    getActiveModel: () => null,
    log,
  } as never);
  controller.conversation = { id: "c1", messages: [] } as never;
  controller.activeId = "c1";
  return { controller, log };
}

describe("AgentConversationController — completion steer draft guard (ticket #69)", () => {
  beforeEach(() => installDom());

  it("preserves user draft and does not submit when composer has unsent content", async () => {
    const { controller, log } = makeController();
    const submit = vi.spyOn(controller, "submit").mockResolvedValue(undefined as never);
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    input.value = "user draft";

    await controller.steerTurn("[Background job completed]\n- tool: ok");

    expect(input.value).toBe("user draft");
    expect(submit).not.toHaveBeenCalled();
    expect(log).toHaveBeenCalledWith(
      expect.stringMatching(/cancelled|draft|IME/i),
    );
  });

  it("sets input and submits when composer is empty", async () => {
    const { controller } = makeController();
    const summary = "[Background job completed]\n- tool: ok\n\nContinue.";
    const submit = vi.spyOn(controller, "submit").mockResolvedValue(undefined as never);
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    input.value = "";

    await controller.steerTurn(summary);

    expect(input.value).toBe(summary);
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it("does not overwrite or submit while IME composition is in progress", async () => {
    const { controller, log } = makeController();
    const submit = vi.spyOn(controller, "submit").mockResolvedValue(undefined as never);
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;
    // Partial candidate may still be empty string; IME flag alone must block.
    input.value = "";
    controller.inputComposing = true;

    await controller.steerTurn("[Background job completed]");

    expect(input.value).toBe("");
    expect(submit).not.toHaveBeenCalled();
    expect(log).toHaveBeenCalledWith(
      expect.stringMatching(/cancelled|draft|IME/i),
    );
  });

  it("isComposerBlockingSteer is true for draft or IME, false for empty idle composer", () => {
    const { controller } = makeController();
    const input = document.querySelector<HTMLTextAreaElement>("#agent-input")!;

    input.value = "";
    controller.inputComposing = false;
    expect(controller.isComposerBlockingSteer()).toBe(false);

    input.value = "  typed  ";
    expect(controller.isComposerBlockingSteer()).toBe(true);

    input.value = "";
    controller.inputComposing = true;
    expect(controller.isComposerBlockingSteer()).toBe(true);
  });
});
