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

function installDom() {
  document.body.innerHTML = `
    <div id="agent-tool-job-strip" hidden>
      <div class="agent-tool-job-strip-head">
        <span class="agent-tool-job-strip-title">Background jobs</span>
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

  it("onEnded marks the job with the final status", () => {
    const strip = new AgentToolJobStrip({ conversationId: "conv-1" });
    strip.mount();
    emit("agent.tool_job_started", { handleId: "h1", conversationId: "conv-1", toolName: "t" });
    emit("agent.tool_job_ended", { handleId: "h1", conversationId: "conv-1", ok: true, reason: "completed" });
    const card = document.querySelector(".agent-tool-job-card") as HTMLElement;
    expect(card.dataset.status).toBe("ok");
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
