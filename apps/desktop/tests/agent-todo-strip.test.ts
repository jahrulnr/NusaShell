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
  getTodos: vi.fn().mockResolvedValue([]),
}));

import { AgentTodoStrip } from "../src/renderer/agent-todo-strip.js";

function installDom() {
  document.body.innerHTML = `
    <div id="agent-todo-strip" hidden>
      <div class="agent-todo-strip-head">
        <button id="agent-todo-strip-toggle" aria-expanded="false" aria-controls="agent-todo-strip-list">
          <span class="agent-todo-strip-chevron">›</span>
          <span class="agent-todo-strip-count" id="agent-todo-strip-count"></span>
        </button>
        <span class="agent-todo-strip-meta" id="agent-todo-strip-meta"></span>
      </div>
      <ol id="agent-todo-strip-list" hidden></ol>
    </div>
  `;
}

function emitTodoUpdated(conversationId: string, items: unknown[]) {
  const handlers = eventHandlers.get("agent.todo_updated") ?? [];
  for (const handler of handlers) handler({ conversationId, items });
}

describe("AgentTodoStrip", () => {
  beforeEach(() => {
    installDom();
    eventHandlers.clear();
  });

  it("hides the strip when items is empty and no turn is active", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([]);
    const el = document.getElementById("agent-todo-strip") as HTMLElement;
    expect(el.hidden).toBe(true);
  });

  it("keeps a reserved empty strip while a turn is active (#63)", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.setTurnActive(true);
    strip.render([]);
    const el = document.getElementById("agent-todo-strip") as HTMLElement;
    const meta = document.getElementById("agent-todo-strip-meta") as HTMLElement;
    expect(el.hidden).toBe(false);
    expect(meta.textContent).toBe("No tasks yet");
  });

  it("exposes polite live regions on count/meta, not on the list (#63)", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([{ id: "1", content: "task", status: "pending" }]);
    const count = document.getElementById("agent-todo-strip-count") as HTMLElement;
    const meta = document.getElementById("agent-todo-strip-meta") as HTMLElement;
    const list = document.getElementById("agent-todo-strip-list") as HTMLElement;
    expect(count.getAttribute("aria-live")).toBe("polite");
    expect(meta.getAttribute("aria-live")).toBe("polite");
    expect(list.getAttribute("aria-live")).toBeNull();
  });

  it("keeps a stable toggle accessible name while aria-expanded toggles (#63/#66)", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([
      { id: "1", content: "first", status: "pending" },
      { id: "2", content: "second", status: "pending" },
    ]);
    const toggle = document.getElementById("agent-todo-strip-toggle") as HTMLButtonElement;
    expect(toggle.getAttribute("aria-label")).toBe("Task checklist");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    toggle.click();
    expect(toggle.getAttribute("aria-label")).toBe("Task checklist");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    strip.render([
      { id: "1", content: "first", status: "completed" },
      { id: "2", content: "second", status: "pending" },
      { id: "3", content: "third", status: "pending" },
    ]);
    expect(toggle.getAttribute("aria-label")).toBe("Task checklist");
    expect(document.getElementById("agent-todo-strip-count")?.textContent).toBe("3 Tasks");
  });

  it("renders items with status glyphs and shows the strip", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([
      { id: "1", content: "first", status: "pending" },
      { id: "2", content: "second", status: "in_progress" },
      { id: "3", content: "done", status: "completed" },
    ]);
    const el = document.getElementById("agent-todo-strip") as HTMLElement;
    expect(el.hidden).toBe(false);
    const items = document.querySelectorAll(".agent-todo-item");
    expect(items).toHaveLength(3);
    expect((items[0] as HTMLElement).dataset.status).toBe("pending");
    expect((items[0].querySelector(".agent-todo-glyph") as HTMLElement).textContent).toBe("○");
    expect((items[1].querySelector(".agent-todo-glyph") as HTMLElement).textContent).toBe("◐");
    expect((items[2].querySelector(".agent-todo-glyph") as HTMLElement).textContent).toBe("●");
  });

  it("shows total task count and open meta", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([
      { id: "1", content: "first", status: "pending" },
      { id: "2", content: "done", status: "completed" },
    ]);
    const count = document.getElementById("agent-todo-strip-count") as HTMLElement;
    const meta = document.getElementById("agent-todo-strip-meta") as HTMLElement;
    expect(count.textContent).toBe("2 Tasks");
    expect(meta.textContent).toBe("1 open");
  });

  it("calls onDelete when the delete button is clicked", () => {
    const deleted: string[] = [];
    const strip = new AgentTodoStrip({ conversationId: "conv-1", onDelete: (id) => deleted.push(id) });
    strip.mount();
    strip.render([{ id: "1", content: "task", status: "pending" }]);
    const delBtn = document.querySelector(".agent-todo-delete") as HTMLButtonElement;
    delBtn.click();
    expect(deleted).toEqual(["1"]);
  });

  it("subscribes to agent.todo_updated for its conversation and re-renders", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    emitTodoUpdated("conv-1", [{ id: "1", content: "from event", status: "in_progress" }]);
    const items = document.querySelectorAll(".agent-todo-item");
    expect(items).toHaveLength(1);
    expect((items[0].querySelector(".agent-todo-content") as HTMLElement).textContent).toBe("from event");
  });

  it("hydrates the current checklist when mounted after the update event", async () => {
    const { getTodos } = await import("../src/renderer/agent-api.js");
    vi.mocked(getTodos).mockResolvedValueOnce([
      { id: "1", content: "rehydrated task", status: "in_progress" },
    ]);
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });

    strip.mount();
    await Promise.resolve();

    expect(document.querySelectorAll(".agent-todo-item")).toHaveLength(1);
    expect(document.querySelector(".agent-todo-content")?.textContent).toBe("rehydrated task");
  });

  it("does not let a stale hydration snapshot erase a live update", async () => {
    const { getTodos } = await import("../src/renderer/agent-api.js");
    let resolveHydration: (items: unknown[]) => void = () => {};
    vi.mocked(getTodos).mockReturnValueOnce(new Promise((resolve) => {
      resolveHydration = resolve;
    }) as never);
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    emitTodoUpdated("conv-1", [{ id: "1", content: "live task", status: "in_progress" }]);
    resolveHydration([]);
    await Promise.resolve();

    expect(document.querySelector(".agent-todo-content")?.textContent).toBe("live task");
  });

  it("does not let hydration from a previous room overwrite the new room", async () => {
    const { getTodos } = await import("../src/renderer/agent-api.js");
    let resolveHydration: (items: unknown[]) => void = () => {};
    vi.mocked(getTodos).mockReturnValueOnce(new Promise((resolve) => {
      resolveHydration = resolve;
    }) as never);
    const first = new AgentTodoStrip({ conversationId: "conv-1" });
    first.mount();
    first.dispose();

    vi.mocked(getTodos).mockResolvedValueOnce([
      { id: "2", content: "room two", status: "pending" },
    ]);
    const second = new AgentTodoStrip({ conversationId: "conv-2" });
    second.mount();
    second.render([{ id: "2", content: "room two", status: "pending" }]);
    resolveHydration([{ id: "1", content: "stale room one", status: "pending" }]);
    await Promise.resolve();

    expect(document.querySelector(".agent-todo-content")?.textContent).toBe("room two");
  });

  it("ignores todo_updated events for other conversations", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    emitTodoUpdated("conv-other", [{ id: "1", content: "other", status: "pending" }]);
    const items = document.querySelectorAll(".agent-todo-item");
    expect(items).toHaveLength(0);
  });

  it("collapses and expands via the toggle button", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([{ id: "1", content: "task", status: "pending" }]);
    const toggle = document.getElementById("agent-todo-strip-toggle") as HTMLButtonElement;
    const list = document.getElementById("agent-todo-strip-list") as HTMLElement;
    expect(list.hidden).toBe(true);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    toggle.click();
    expect(list.hidden).toBe(false);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    toggle.click();
    expect(list.hidden).toBe(true);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
  });

  it("disposes the event subscription", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    expect(eventHandlers.get("agent.todo_updated")?.length ?? 0).toBe(1);
    strip.dispose();
    expect(eventHandlers.get("agent.todo_updated")?.length ?? 0).toBe(0);
  });

  it("keeps the strip expanded across a live update render (#66)", () => {
    const strip = new AgentTodoStrip({ conversationId: "conv-1" });
    strip.mount();
    strip.render([{ id: "1", content: "task", status: "pending" }]);
    const toggle = document.getElementById("agent-todo-strip-toggle") as HTMLButtonElement;
    const list = document.getElementById("agent-todo-strip-list") as HTMLElement;
    toggle.click(); // expand
    expect(list.hidden).toBe(false);

    // A live todo update triggers render(); it must not collapse the user's expansion.
    emitTodoUpdated("conv-1", [
      { id: "1", content: "task", status: "pending" },
      { id: "2", content: "task 2", status: "in_progress" },
    ]);

    expect(list.hidden).toBe(false);
    expect(document.querySelectorAll(".agent-todo-item")).toHaveLength(2);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
  });

  it("rebinds the collapse toggle after switching rooms", () => {
    const first = new AgentTodoStrip({ conversationId: "conv-1" });
    first.mount();
    first.render([{ id: "1", content: "room one", status: "pending" }]);
    first.dispose();

    const second = new AgentTodoStrip({ conversationId: "conv-2" });
    second.mount();
    second.render([{ id: "2", content: "room two", status: "pending" }]);
    (document.getElementById("agent-todo-strip-toggle") as HTMLButtonElement).click();

    expect(second.collapsed).toBe(false);
    expect(document.getElementById("agent-todo-strip-list")?.hidden).toBe(false);
  });
});
