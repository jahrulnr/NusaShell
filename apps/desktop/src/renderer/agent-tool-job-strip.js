// Async tool job strip — renders running background tool jobs above the
// composer, below the todo strip. Each job card shows the tool name, status
// badge, live tail (from tool_job_update), and a Stop button that calls
// agent.tool_job_kill. The strip rehydrates from agent.tool_job_list on
// conversation open.

import { subscribeToolJobEvents } from "./turn-event-helper.js";
import { listToolJobs, killToolJob } from "./agent-api.js";

const STATUS_LABEL = {
  running: "running",
  ok: "ok",
  fail: "fail",
  killed: "killed",
};
const SUCCESS_VISIBILITY_MS = 8_000;

export class AgentToolJobStrip {
  constructor({ conversationId, onKill }) {
    this.conversationId = conversationId;
    this.onKill = onKill ?? ((handleId) => killToolJob(handleId));
    this.jobs = new Map();
    this.collapsed = true;
    this.disposed = false;
    this.disposer = null;
    this.toggleButton = null;
    this.toggleHandler = null;
    this.okExpiryTimers = new Map();
  }

  mount() {
    this.disposed = false;
    this.disposer = subscribeToolJobEvents({
      conversationId: this.conversationId,
      onJobStarted: (p) => this.onStarted(p),
      onJobUpdate: (p) => this.onUpdate(p),
      onJobEnded: (p) => this.onEnded(p),
    });
    this.bindToggle();
    void this.rehydrate();
  }

  dispose() {
    this.disposed = true;
    this.disposer?.();
    this.disposer = null;
    this.toggleButton?.removeEventListener("click", this.toggleHandler);
    this.toggleButton = null;
    this.toggleHandler = null;
    for (const timer of this.okExpiryTimers.values()) clearTimeout(timer);
    this.okExpiryTimers.clear();
    this.jobs.clear();
  }

  bindToggle() {
    const toggle = document.getElementById("agent-tool-job-strip-toggle");
    if (!toggle) return;
    this.toggleButton = toggle;
    this.syncCollapsedUi();
    this.toggleHandler = () => {
      this.collapsed = !this.collapsed;
      this.syncCollapsedUi();
    };
    toggle.addEventListener("click", this.toggleHandler);
  }

  syncCollapsedUi() {
    const toggle = document.getElementById("agent-tool-job-strip-toggle");
    const list = document.getElementById("agent-tool-job-list");
    const strip = document.getElementById("agent-tool-job-strip");
    if (toggle) toggle.setAttribute("aria-expanded", String(!this.collapsed));
    if (list) list.hidden = this.collapsed;
    if (strip) strip.dataset.expanded = this.collapsed ? "false" : "true";
  }

  async rehydrate() {
    try {
      const result = await listToolJobs(this.conversationId);
      if (this.disposed) return;
      const jobs = Array.isArray(result) ? result : (result?.jobs ?? []);
      for (const job of jobs) {
        const liveJob = this.jobs.get(job.handleId);
        // Events received while the request was in flight are newer than the
        // snapshot, so preserve their status/tail rather than replacing them.
        this.jobs.set(job.handleId, liveJob ? { ...job, ...liveJob } : job);
        if ((liveJob ?? job).status === "ok") this.scheduleOkExpiry(liveJob ?? job);
      }
      this.render();
    } catch {
      // Rehydrate is best-effort; live events will populate.
    }
  }

  onStarted(payload) {
    if (this.disposed) return;
    this.clearOkExpiry(payload.handleId);
    this.jobs.set(payload.handleId, {
      handleId: payload.handleId,
      toolName: payload.toolName,
      status: "running",
      tail: "",
      ...(payload.pluginId ? { pluginId: payload.pluginId } : {}),
    });
    this.render();
  }

  onUpdate(payload) {
    if (this.disposed) return;
    const job = this.jobs.get(payload.handleId);
    if (!job) return;
    job.status = payload.status;
    if (payload.tail !== undefined) job.tail = payload.tail;
    this.render();
  }

  onEnded(payload) {
    if (this.disposed) return;
    const job = this.jobs.get(payload.handleId);
    if (!job) return;
    job.status = payload.ok ? "ok" : payload.reason === "killed" ? "killed" : "fail";
    if (payload.error) job.error = payload.error;
    this.render();
    // Successful jobs auto-remove after a short delay so the user sees the
    // final "ok" state; failed/killed jobs persist until dismissed (#68).
    if (job.status === "ok") this.scheduleOkExpiry(job);
  }

  clearOkExpiry(handleId) {
    const timer = this.okExpiryTimers.get(handleId);
    if (timer) clearTimeout(timer);
    this.okExpiryTimers.delete(handleId);
  }

  scheduleOkExpiry(job) {
    if (this.okExpiryTimers.has(job.handleId)) return;
    const endedAt = Date.parse(job.endedAt ?? "");
    const elapsed = Number.isNaN(endedAt) ? 0 : Math.max(0, Date.now() - endedAt);
    const timer = setTimeout(() => {
      this.okExpiryTimers.delete(job.handleId);
      if (!this.disposed && this.jobs.get(job.handleId)?.status === "ok") {
        this.jobs.delete(job.handleId);
        this.render();
      }
    }, Math.max(0, SUCCESS_VISIBILITY_MS - elapsed));
    this.okExpiryTimers.set(job.handleId, timer);
  }

  render() {
    const strip = document.getElementById("agent-tool-job-strip");
    const list = document.getElementById("agent-tool-job-list");
    if (!strip || !list) return;
    const jobs = [...this.jobs.values()];
    if (jobs.length === 0) {
      strip.hidden = true;
      list.textContent = "";
      return;
    }
    strip.hidden = false;
    this.syncCollapsedUi();
    const runningCount = jobs.filter((job) => job.status === "running").length;
    const title = strip.querySelector(".agent-tool-job-strip-title");
    const meta = document.getElementById("agent-tool-job-strip-meta");
    if (title) title.textContent = `${jobs.length} tool run${jobs.length === 1 ? "" : "s"}`;
    if (meta) {
      const failedCount = jobs.filter((job) => job.status === "fail" || job.status === "killed").length;
      meta.textContent = runningCount > 0
        ? `${runningCount} running`
        : failedCount > 0 ? `${failedCount} need attention` : "All done";
      meta.dataset.done = runningCount === 0 && failedCount === 0 ? "true" : "false";
    }
    list.textContent = "";
    for (const job of jobs) {
      const card = document.createElement("div");
      card.className = "agent-tool-job-card";
      card.setAttribute("role", "listitem");
      card.dataset.status = job.status;
      card.dataset.handleId = job.handleId;

      const header = document.createElement("div");
      header.className = "agent-tool-job-card-head";

      const name = document.createElement("span");
      name.className = "agent-tool-job-card-name";
      name.textContent = job.toolName;

      const badge = document.createElement("span");
      badge.className = "agent-tool-job-card-badge";
      badge.textContent = STATUS_LABEL[job.status] ?? job.status;

      const actions = document.createElement("div");
      actions.className = "agent-tool-job-card-actions";
      actions.appendChild(badge);
      header.append(name, actions);

      if (job.tail) {
        const tail = document.createElement("pre");
        tail.className = "agent-tool-job-card-tail";
        tail.textContent = job.tail.slice(-2000);
        card.append(header, tail);
      } else {
        card.append(header);
      }

      if (job.error) {
        const err = document.createElement("div");
        err.className = "agent-tool-job-card-error";
        err.textContent = job.error;
        card.append(err);
      }

      if (job.status === "running") {
        const stop = document.createElement("button");
        stop.className = "agent-tool-job-card-stop";
        stop.type = "button";
        stop.textContent = "Stop";
        stop.setAttribute("aria-label", `Stop background job: ${job.toolName}`);
        stop.addEventListener("click", (event) => {
          event.stopPropagation();
          stop.disabled = true;
          stop.textContent = "Stopping…";
          void this.onKill(job.handleId);
        });
        actions.appendChild(stop);
      } else if (job.status === "fail" || job.status === "killed") {
        // Failed/killed jobs persist with their error until the user dismisses
        // them (#68): an error must not vanish silently.
        const dismiss = document.createElement("button");
        dismiss.className = "agent-tool-job-card-dismiss";
        dismiss.type = "button";
        dismiss.textContent = "Dismiss";
        dismiss.setAttribute("aria-label", `Dismiss finished job: ${job.toolName}`);
        dismiss.addEventListener("click", (event) => {
          event.stopPropagation();
          this.clearOkExpiry(job.handleId);
          this.jobs.delete(job.handleId);
          this.render();
        });
        actions.appendChild(dismiss);
      }

      list.append(card);
    }
  }
}
