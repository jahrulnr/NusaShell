// Agent conversation todo strip — renders the agent-owned checklist as the top
// row of the composer stack (Cursor-style: › N Tasks · open progress). The agent
// owns the list via the `todo` meta-tool; the user can delete items and collapse
// the list without leaving the composer card.

import { subscribeTodoEvents } from "./turn-event-helper.js";
import { deleteTodos, getTodos } from "./agent-api.js";

const STATUS_GLYPH = {
  pending: "○",
  in_progress: "◐",
  completed: "●",
};

export class AgentTodoStrip {
  constructor({ conversationId, onDelete }) {
    this.conversationId = conversationId;
    this.onDelete = onDelete ?? ((id) => deleteTodos(conversationId, [id]));
    this.items = [];
    this.collapsed = true;
    this.receivedLiveUpdate = false;
    this.disposer = null;
  }

  mount() {
    this.disposer = subscribeTodoEvents({
      conversationId: this.conversationId,
      onTodoUpdated: (items) => {
        this.receivedLiveUpdate = true;
        this.render(items);
      },
    });
    this.bindToggle();
    void getTodos(this.conversationId)
      .then((items) => {
        if (!this.receivedLiveUpdate) this.render(items);
      })
      .catch(() => {
        // The event subscription remains authoritative when the backend query
        // is unavailable (for example while the host is reconnecting).
      });
  }

  dispose() {
    this.disposer?.();
    this.disposer = null;
  }

  bindToggle() {
    const toggle = document.getElementById("agent-todo-strip-toggle");
    if (!toggle || toggle.dataset.bound === "1") return;
    toggle.dataset.bound = "1";
    this.syncCollapsedUi();
    toggle.addEventListener("click", () => {
      this.collapsed = !this.collapsed;
      this.syncCollapsedUi();
    });
  }

  syncCollapsedUi() {
    const toggle = document.getElementById("agent-todo-strip-toggle");
    const list = document.getElementById("agent-todo-strip-list");
    const strip = document.getElementById("agent-todo-strip");
    if (toggle) toggle.setAttribute("aria-expanded", String(!this.collapsed));
    if (list) list.hidden = this.collapsed;
    if (strip) strip.dataset.expanded = this.collapsed ? "false" : "true";
  }

  render(items) {
    this.items = items ?? [];
    const strip = document.getElementById("agent-todo-strip");
    const list = document.getElementById("agent-todo-strip-list");
    const count = document.getElementById("agent-todo-strip-count");
    const meta = document.getElementById("agent-todo-strip-meta");
    if (!strip || !list) return;
    if (this.items.length === 0) {
      strip.hidden = true;
      list.textContent = "";
      if (count) count.textContent = "0 Tasks";
      if (meta) meta.textContent = "";
      return;
    }
    strip.hidden = false;
    this.syncCollapsedUi();
    const incomplete = this.items.filter((i) => i.status !== "completed").length;
    const total = this.items.length;
    if (count) count.textContent = `${total} Task${total === 1 ? "" : "s"}`;
    if (meta) {
      meta.textContent = incomplete === 0 ? "All done" : `${incomplete} open`;
      meta.dataset.done = incomplete === 0 ? "true" : "false";
    }
    list.textContent = "";
    for (const item of this.items) {
      const li = document.createElement("li");
      li.className = "agent-todo-item";
      li.dataset.status = item.status;
      li.dataset.id = item.id;
      const glyph = document.createElement("span");
      glyph.className = "agent-todo-glyph";
      glyph.textContent = STATUS_GLYPH[item.status] ?? "○";
      glyph.setAttribute("aria-hidden", "true");
      const content = document.createElement("span");
      content.className = "agent-todo-content";
      content.textContent = item.content;
      const del = document.createElement("button");
      del.className = "agent-todo-delete";
      del.type = "button";
      del.setAttribute("aria-label", `Delete task: ${item.content}`);
      del.title = "Delete task";
      del.textContent = "×";
      del.addEventListener("click", (event) => {
        event.stopPropagation();
        void this.onDelete(item.id);
      });
      li.append(glyph, content, del);
      list.append(li);
    }
  }
}
