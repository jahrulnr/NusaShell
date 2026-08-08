// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

const eventHandlers = new Map<string, Array<(payload: unknown) => void>>();

vi.mock("../src/renderer/ws-client.js", () => ({
  initWsClient: vi.fn(),
  connectWs: vi.fn(),
  sendRequest: vi.fn(),
  subscribe: vi.fn().mockResolvedValue(undefined),
  isConnected: vi.fn(() => true),
  onEvent: vi.fn((eventType: string, handler: (payload: unknown) => void) => {
    const list = eventHandlers.get(eventType) ?? [];
    list.push(handler);
    eventHandlers.set(eventType, list);
    return () => {
      const arr = eventHandlers.get(eventType);
      if (!arr) return;
      const idx = arr.indexOf(handler);
      if (idx >= 0) arr.splice(idx, 1);
    };
  }),
}));

vi.mock("../src/renderer/agent-api.js", () => ({
  runAgentTurn: vi.fn(),
  cancelAgentTurn: vi.fn(),
  answerAskQuestion: vi.fn(),
  getActiveTurn: vi.fn(),
  setTodos: vi.fn(),
  deleteTodos: vi.fn(),
  listToolJobs: vi.fn().mockResolvedValue([]),
  killToolJob: vi.fn(),
}));

import { AgentToolJobStrip } from "../src/renderer/agent-tool-job-strip.js";
import { listToolJobs } from "../src/renderer/agent-api.js";

function installDom() {
  document.body.innerHTML = `
    <div id="agent-tool-job-strip" hidden>
      <div class="agent-tool-job-strip-head">
        <button class="agent-tool-job-strip-toggle" id="agent-tool-job-strip-toggle" type="button" aria-expanded="false" aria-controls="agent-tool-job-list">
          <span class="agent-tool-job-strip-chevron" aria-hidden="true">›</span>
          <span class="agent-tool-job-strip-title">0 tool runs</span>
        </button>
        <span class="agent-tool-job-strip-meta" id="agent-tool-job-strip-meta"></span>
      </div>
      <div id="agent-tool-job-list"></div>
    </div>
  `;
}

function emit(eventType: string, payload: unknown) {
  const handlers = eventHandlers.get(eventType) ?? [];
  for (const handler of handlers) handler(payload);
}

describe("AgentToolJobStrip", () => {
  beforeEach(() => {
    installDom();
    eventHandlers.clear();
    vi.mocked(listToolJobs).mockReset();
    vi.mocked(listToolJobs).mockResolvedValue([]);
  });

  it("hides the strip when no jobs are active", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render();
    const el = document.getElementById("agent-tool-job-strip") as HTMLElement;
    expect(el.hidden).toBe(true);
    strip.dispose();
  });

  it("onStarted shows the strip and renders a running job card", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", {
      handleId: "h1",
      conversationId: "conv-1",
      toolName: "run_command",
      kind: "mcp",
    });
    const el = document.getElementById("agent-tool-job-strip") as HTMLElement;
    expect(el.hidden).toBe(false);
    const card = document.querySelector(".agent-tool-job-card") as HTMLElement;
    expect(card).toBeTruthy();
    expect(card.dataset.status).toBe("running");
    expect(card.dataset.handleId).toBe("h1");
    expect((card.querySelector(".agent-tool-job-card-name") as HTMLElement).textContent).toBe("run_command");
    expect(card.getAttribute("role")).toBe("listitem");
    expect(card.querySelector(".agent-tool-job-card-actions")).toBeTruthy();
    expect(document.querySelector(".agent-tool-job-strip-title")?.textContent).toBe("1 tool run");
    expect(document.getElementById("agent-tool-job-strip-meta")?.textContent).toBe("1 running");
    strip.dispose();
  });

  it("defaults collapsed and toggles the active job list", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", {
      handleId: "h1",
      conversationId: "conv-1",
      toolName: "run_command",
    });
    const toggle = document.getElementById("agent-tool-job-strip-toggle") as HTMLButtonElement;
    const list = document.getElementById("agent-tool-job-list") as HTMLElement;
    expect(list.hidden).toBe(true);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    toggle.click();
    expect(list.hidden).toBe(false);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    toggle.click();
    expect(list.hidden).toBe(true);
    strip.dispose();
  });

  it("keeps a live job when the initial rehydration snapshot is stale", async () => {
    let resolveList!: (jobs: unknown[]) => void;
    vi.mocked(listToolJobs).mockImplementationOnce(() => new Promise((resolve) => {
      resolveList = resolve;
    }));
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();

    emit("agent.tool_job_started", {
      handleId: "h-live",
      conversationId: "conv-1",
      toolName: "run_command",
      kind: "mcp",
    });
    resolveList([]);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(document.querySelector(".agent-tool-job-card")?.getAttribute("data-handle-id")).toBe("h-live");
    strip.dispose();
  });

  it("summarizes tool runs with the same count and meta hierarchy as tasks", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h1", conversationId: "conv-1", toolName: "t" });

    expect(document.querySelector(".agent-tool-job-strip-title")?.textContent).toBe("1 tool run");
    expect(document.getElementById("agent-tool-job-strip-meta")?.textContent).toBe("1 running");
    strip.dispose();
  });

  it("onUpdate updates the tail and status", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h1", conversationId: "conv-1", toolName: "t" });
    emit("agent.tool_job_update", { handleId: "h1", conversationId: "conv-1", status: "running", tail: "line 1\n", bytes: 7, streamSeq: 1 });
    const tail = document.querySelector(".agent-tool-job-card-tail") as HTMLElement;
    expect(tail).toBeTruthy();
    expect(tail.textContent).toContain("line 1");
    strip.dispose();
  });

  it("onEnded shows an ok badge then auto-removes the card (#68)", () => {
    vi.useFakeTimers();
    try {
      const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
      strip.mount();
      emit("agent.tool_job_started", { handleId: "h1", conversationId: "conv-1", toolName: "t" });
      emit("agent.tool_job_ended", { handleId: "h1", conversationId: "conv-1", ok: true, reason: "completed" });

      // Terminal "ok" state is shown (not hidden) so the user sees the result.
      const card = document.querySelector(".agent-tool-job-card") as HTMLElement;
      expect(card).toBeTruthy();
      expect(card.dataset.status).toBe("ok");
      expect((document.getElementById("agent-tool-job-strip") as HTMLElement).hidden).toBe(false);
      expect(card.querySelector(".agent-tool-job-card-badge")?.textContent).toBe("ok");

      // After the success delay it auto-removes.
      vi.advanceTimersByTime(8000);
      expect(document.querySelector(".agent-tool-job-card")).toBeNull();
      strip.dispose();
    } finally {
      vi.useRealTimers();
    }
  });

  it("expires a successful job restored from rehydration", async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(listToolJobs).mockResolvedValueOnce([{
        handleId: "h-restored",
        toolName: "run_command",
        kind: "mcp",
        status: "ok",
        tail: "done",
        endedAt: new Date().toISOString(),
      }]);
      const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
      strip.mount();
      await Promise.resolve();

      expect(document.querySelector(".agent-tool-job-card")?.getAttribute("data-handle-id")).toBe("h-restored");
      vi.advanceTimersByTime(8000);
      expect(document.querySelector(".agent-tool-job-card")).toBeNull();
      strip.dispose();
    } finally {
      vi.useRealTimers();
    }
  });

  it("onEnded fail keeps the card with an error block and Dismiss until dismissed (#68)", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h2", conversationId: "conv-1", toolName: "t" });
    emit("agent.tool_job_ended", {
      handleId: "h2",
      conversationId: "conv-1",
      ok: false,
      reason: "error",
      error: "boom: something failed",
    });

    const card = document.querySelector(".agent-tool-job-card") as HTMLElement;
    expect(card).toBeTruthy();
    expect(card.dataset.status).toBe("fail");
    const err = card.querySelector(".agent-tool-job-card-error") as HTMLElement;
    expect(err).toBeTruthy();
    expect(err.textContent).toContain("boom");
    // Failed jobs persist until dismissed — no auto-remove timer.
    const dismiss = card.querySelector(".agent-tool-job-card-dismiss") as HTMLButtonElement;
    expect(dismiss).toBeTruthy();

    dismiss.click();
    expect(document.querySelector(".agent-tool-job-card")).toBeNull();
    expect((document.getElementById("agent-tool-job-strip") as HTMLElement).hidden).toBe(true);
    strip.dispose();
  });

  it("onEnded killed shows a killed badge with Dismiss (#68)", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h3", conversationId: "conv-1", toolName: "t" });
    emit("agent.tool_job_ended", { handleId: "h3", conversationId: "conv-1", ok: false, reason: "killed" });

    const card = document.querySelector(".agent-tool-job-card") as HTMLElement;
    expect(card).toBeTruthy();
    expect(card.dataset.status).toBe("killed");
    expect(card.querySelector(".agent-tool-job-card-badge")?.textContent).toBe("killed");
    expect(card.querySelector(".agent-tool-job-card-dismiss")).toBeTruthy();
    strip.dispose();
  });

  it("calls onKill when the Stop button is clicked", () => {
    const killed: string[] = [];
    const strip = new AgentToolJobStrip({ conversationId: "conv-1", onKill: (id) => killed.push(id) });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h1", conversationId: "conv-1", toolName: "t" });
    const stop = document.querySelector(".agent-tool-job-card-stop") as HTMLButtonElement;
    stop.click();
    expect(killed).toEqual(["h1"]);
    strip.dispose();
  });

  it("ignores events for other conversations", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h1", conversationId: "conv-other", toolName: "t" });
    const cards = document.querySelectorAll(".agent-tool-job-card");
    expect(cards).toHaveLength(0);
    strip.dispose();
  });

  it("disposes the event subscription", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    const beforeCount = (eventHandlers.get("agent.tool_job_started")?.length ?? 0);
    expect(beforeCount).toBe(1);
    strip.dispose();
    const afterCount = (eventHandlers.get("agent.tool_job_started")?.length ?? 0);
    expect(afterCount).toBe(0);
  });
});
