// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";

function installDom() {
  document.body.innerHTML = `
    <aside id="agent-subpane" hidden>
      <div id="agent-subpane-overlay" hidden></div>
      <span id="agent-subpane-badge"></span>
      <span id="agent-subpane-title"></span>
      <span id="agent-subpane-status"></span>
      <button id="agent-subpane-close"></button>
      <div id="agent-subpane-body"></div>
    </aside>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(() => ({ matches: true })) as typeof window.matchMedia;
}

describe("AgentConversationController — subagent stream pane", () => {
  beforeEach(() => installDom());

  it("preserves live text when a running pane is closed and remounted", () => {
    const controller = new AgentConversationController({} as never);
    const run = {
      runId: "trace-1",
      providerId: "cursor",
      prompt: "Fix it",
      status: "running",
      steps: [],
    };

    controller.activeSubagentRun = run;
    controller.resetSubagentStreamState([], run.runId);
    controller.appendSubpaneText("Live output");
    controller.closeSubpaneDrawerUi();
    controller.mountSubpane(run, { open: true });

    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Live output");
    expect(controller.subagentStreamState?.textContent).toBe("Live output");
  });

  it("renders sealed stream steps when the run ends", async () => {
    const updateSubagentRunStatus = vi.fn().mockResolvedValue({ id: "conv-1" });
    const controller = new AgentConversationController({
      shell: { agentConversations: { updateSubagentRunStatus } },
    } as never);
    controller.conversation = { id: "conv-1", messages: [] } as never;
    controller.activeSubagentRun = {
      runId: "trace-2",
      providerId: "cursor",
      prompt: "Fix it",
      status: "running",
      steps: [],
    } as never;
    controller.resetSubagentStreamState([], "trace-2");
    controller.appendSubpaneText("Completed output");

    controller.handleSubagentRunEnded({ runId: "trace-2", ok: true, summary: "Done" });
    await Promise.resolve();

    expect(document.querySelector("#agent-subpane-body")?.textContent).toContain("Completed output");
    expect(updateSubagentRunStatus).toHaveBeenCalledWith(
      "conv-1",
      "trace-2",
      "ok",
      expect.objectContaining({ summary: "Done", steps: [{ type: "text", content: "Completed output" }] }),
    );
  });
});
