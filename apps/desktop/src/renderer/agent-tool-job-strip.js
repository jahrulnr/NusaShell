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

export class AgentToolJobStrip {
  constructor({ conversationId, onKill }) {
    this.conversationId = conversationId;
    this.onKill = onKill ?? ((handleId) => killToolJob(handleId));
    this.jobs = new Map();
    this.disposer = null;
  }

  mount() {
    this.disposer = subscribeToolJobEvents({
      conversationId: this.conversationId,
      onJobStarted: (p) => this.onStarted(p),
      onJobUpdate: (p) => this.onUpdate(p),
      onJobEnded: (p) => this.onEnded(p),
    });
    void this.rehydrate();
  }

  dispose() {
    this.disposer?.();
    this.disposer = null;
    this.jobs.clear();
  }

  async rehydrate() {
    try {
      const result = await listToolJobs(this.conversationId);
      const jobs = Array.isArray(result) ? result : (result?.jobs ?? []);
      this.jobs.clear();
      for (const job of jobs) {
        this.jobs.set(job.handleId, job);
      }
      this.render();
    } catch {
      // Rehydrate is best-effort; live events will populate.
    }
  }

  onStarted(payload) {
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
    const job = this.jobs.get(payload.handleId);
    if (!job) return;
    job.status = payload.status;
    if (payload.tail !== undefined) job.tail = payload.tail;
    this.render();
  }

  onEnded(payload) {
    const job = this.jobs.get(payload.handleId);
    if (!job) return;
    job.status = payload.ok ? "ok" : payload.reason === "killed" ? "killed" : "fail";
    if (payload.error) job.error = payload.error;
    this.render();
    // Auto-remove ended jobs after a short delay so the user sees the final state.
    setTimeout(() => {
      if (this.jobs.get(payload.handleId)?.status === job.status) {
        this.jobs.delete(payload.handleId);
        this.render();
      }
    }, 8000);
  }

  render() {
    const strip = document.getElementById("agent-tool-job-strip");
    const list = document.getElementById("agent-tool-job-list");
    if (!strip || !list) return;
    const visibleJobs = [...this.jobs.values()].filter((job) => job.status === "running");
    if (visibleJobs.length === 0) {
      strip.hidden = true;
      list.textContent = "";
      return;
    }
    strip.hidden = false;
    const runningCount = visibleJobs.length;
    const title = strip.querySelector(".agent-tool-job-strip-title");
    if (title) title.textContent = runningCount > 0
      ? `Background jobs · ${runningCount} running`
      : `Background jobs · ${this.jobs.size}`;
    list.textContent = "";
    for (const job of visibleJobs) {
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
      }

      list.append(card);
    }
  }
}
