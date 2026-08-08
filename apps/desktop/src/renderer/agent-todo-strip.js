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

const TOGGLE_ARIA_LABEL = "Task checklist";

export class AgentTodoStrip {
  constructor({ conversationId, onDelete }) {
    this.conversationId = conversationId;
    this.onDelete = onDelete ?? ((id) => deleteTodos(conversationId, [id]));
    this.items = [];
    this.collapsed = true;
    /** When true and the checklist is empty, keep a reserved strip (no layout jump mid-turn). */
    this.turnActive = false;
    this.receivedLiveUpdate = false;
    this.disposed = false;
    this.disposer = null;
    this.toggleButton = null;
    this.toggleHandler = null;
  }

  mount() {
    this.disposed = false;
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
        if (!this.disposed && !this.receivedLiveUpdate) this.render(items);
      })
      .catch(() => {
        // The event subscription remains authoritative when the backend query
        // is unavailable (for example while the host is reconnecting).
      });
  }

  dispose() {
    this.disposed = true;
    this.disposer?.();
    this.disposer = null;
    this.toggleButton?.removeEventListener("click", this.toggleHandler);
    this.toggleButton = null;
    this.toggleHandler = null;
  }

  /**
   * Reflect whether this conversation has an in-flight agent turn so an empty
   * checklist can reserve space instead of collapsing the composer stack.
   * @param {boolean} active
   */
  setTurnActive(active) {
    const next = Boolean(active);
    if (this.turnActive === next) return;
    this.turnActive = next;
    this.render(this.items);
  }

  bindToggle() {
    const toggle = document.getElementById("agent-todo-strip-toggle");
    if (!toggle) return;
    this.toggleButton = toggle;
    this.syncToggleName();
    this.syncCollapsedUi();
    this.toggleHandler = () => {
      this.collapsed = !this.collapsed;
      this.syncCollapsedUi();
    };
    toggle.addEventListener("click", this.toggleHandler);
  }

  syncToggleName() {
    const toggle = document.getElementById("agent-todo-strip-toggle");
    if (!toggle) return;
    // Stable accessible name — count lives in visible text only (#63).
    toggle.setAttribute("aria-label", TOGGLE_ARIA_LABEL);
  }

  syncCollapsedUi() {
    const toggle = document.getElementById("agent-todo-strip-toggle");
    const list = document.getElementById("agent-todo-strip-list");
    const strip = document.getElementById("agent-todo-strip");
    if (toggle) {
      this.syncToggleName();
      toggle.setAttribute("aria-expanded", String(!this.collapsed));
    }
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

    this.syncToggleName();
    this.ensureLiveRegions(count, meta);

    if (this.items.length === 0) {
      list.textContent = "";
      if (count) count.textContent = "0 Tasks";
      if (meta) {
        meta.textContent = this.turnActive ? "No tasks yet" : "";
        delete meta.dataset.done;
      }
      // Idle + empty: hide. Active turn + empty: reserve space (#63).
      strip.hidden = !this.turnActive;
      this.syncCollapsedUi();
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

  ensureLiveRegions(count, meta) {
    if (count) {
      count.setAttribute("aria-live", "polite");
      count.setAttribute("aria-atomic", "true");
    }
    if (meta) {
      meta.setAttribute("aria-live", "polite");
      meta.setAttribute("aria-atomic", "true");
    }
    const list = document.getElementById("agent-todo-strip-list");
    if (list) {
      list.removeAttribute("aria-live");
    }
  }
}
