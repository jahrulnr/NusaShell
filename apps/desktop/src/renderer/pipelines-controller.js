/**
 * Pipelines controller — multi-step DAG with event, schedule, and manual run
 * (while NusaShell is open).
 */

import { sendRequest, onEvent } from "./ws-client.js";
import { confirmDialog } from "./ui-dialogs.js";
import { renderJobOutputMarkdown } from "./agent-conversation-ui.js";

function describeScheduleKey(schedule) {
  if (!schedule || typeof schedule !== "object") return "";
  if (schedule.kind === "interval") {
    const m = schedule.minutes;
    if (m % 1440 === 0) return `every ${m / 1440}d`;
    if (m % 60 === 0) return `every ${m / 60}h`;
    return `every ${m}m`;
  }
  if (schedule.kind === "cron") return schedule.expr ?? "";
  if (schedule.kind === "once") return schedule.runAt ?? "";
  return "";
}

function parseScheduleText(input) {
  const trimmed = String(input ?? "").trim();
  if (!trimmed) return null;
  if (/^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2})?(\.\d{1,3})?Z?)?$/i.test(trimmed)) {
    let iso = trimmed;
    if (/^\d{4}-\d{2}-\d{2}$/.test(trimmed)) iso = `${trimmed}T00:00:00Z`;
    else if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/.test(trimmed)) iso = `${trimmed.replace(" ", "T")}:00Z`;
    else if (/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(trimmed)) iso = `${trimmed.replace(" ", "T")}Z`;
    else if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(trimmed)) iso = `${trimmed}:00Z`;
    const parsed = new Date(iso);
    if (Number.isNaN(parsed.getTime())) return null;
    return { kind: "once", runAt: parsed.toISOString() };
  }
  const intervalMatch = /^(?:every\s+)?(\d+)\s*([mhd])$/i.exec(trimmed);
  if (intervalMatch) {
    const value = parseInt(intervalMatch[1], 10);
    const unit = intervalMatch[2].toLowerCase();
    const minutes = value * (unit === "m" ? 1 : unit === "h" ? 60 : 1440);
    if (minutes <= 0) return null;
    return { kind: "interval", minutes };
  }
  if (trimmed.split(/\s+/).length === 5) {
    return { kind: "cron", expr: trimmed };
  }
  return null;
}

export class PipelinesController {
  constructor({ notify }) {
    this.notify = notify;
    this.pipelines = [];
    this.editingPipeline = null;
    this.steps = [];
    this._runRefreshTimer = null;
    this._liveStepStatus = new Map(); // stepId → status for open details
    this._detailsPipelineId = null;
    this._detailsRunId = null;
    this._detailsPipeline = null;
    this._detailsLatestRun = null;
    this._detailsStepId = null;
    /** @type {Map<string, string>} pipelineId → active runId */
    this._activeRunByPipeline = new Map();
    /** @type {Array<() => void>} */
    this._unsubscribers = [];

    this.els = {
      view: document.querySelector('[data-view="pipelines"]'),
      list: document.getElementById("pipelines-list"),
      empty: document.getElementById("pipelines-empty"),
      error: document.getElementById("pipelines-error"),
      errorMessage: document.getElementById("pipelines-error-message"),
      newBtn: document.getElementById("pipelines-new-btn"),
      modal: document.getElementById("pipeline-modal"),
      modalTitle: document.getElementById("pipeline-modal-title"),
      modalClose: document.getElementById("pipeline-modal-close"),
      modalCancel: document.getElementById("pipeline-modal-cancel"),
      modalSave: document.getElementById("pipeline-modal-save"),
      fieldName: document.getElementById("pipeline-field-name"),
      fieldDescription: document.getElementById("pipeline-field-description"),
      fieldTriggerKind: document.getElementById("pipeline-field-trigger-kind"),
      scheduleFields: document.getElementById("pipeline-schedule-fields"),
      eventFields: document.getElementById("pipeline-event-fields"),
      fieldSchedule: document.getElementById("pipeline-field-schedule"),
      fieldEventPattern: document.getElementById("pipeline-field-event-pattern"),
      fieldEventPlugin: document.getElementById("pipeline-field-event-plugin"),
      stepsList: document.getElementById("pipeline-steps-list"),
      addStepBtn: document.getElementById("pipeline-add-step-btn"),
      detailsModal: document.getElementById("pipeline-details-modal"),
      detailsTitle: document.getElementById("pipeline-details-title"),
      detailsStatus: document.getElementById("pipeline-details-status"),
      detailsMeta: document.getElementById("pipeline-details-meta"),
      detailsDag: document.getElementById("pipeline-details-dag"),
      detailsStep: document.getElementById("pipeline-details-step"),
      detailsOutput: document.getElementById("pipeline-details-output"),
      detailsClose: document.getElementById("pipeline-details-close"),
      detailsOk: document.getElementById("pipeline-details-ok"),
      detailsRun: document.getElementById("pipeline-details-run"),
      detailsCancel: document.getElementById("pipeline-details-cancel"),
      emptyNewBtn: document.querySelector('[data-control="pipelines-empty-new"]'),
    };

    this._bind();
    this._bindEvents();
  }

  destroy() {
    for (const unsub of this._unsubscribers) {
      try { unsub(); } catch { /* ignore */ }
    }
    this._unsubscribers = [];
    if (this._runRefreshTimer) {
      clearTimeout(this._runRefreshTimer);
      this._runRefreshTimer = null;
    }
    this._activeRunByPipeline.clear();
    this._liveStepStatus.clear();
    this._detailsPipelineId = null;
    this._detailsRunId = null;
    this._detailsPipeline = null;
    this._detailsLatestRun = null;
    this._detailsStepId = null;
  }

  _on(eventType, handler) {
    const unsub = onEvent(eventType, handler);
    this._unsubscribers.push(unsub);
  }

  _bind() {
    this.els.newBtn?.addEventListener("click", () => this.openModal());
    this.els.emptyNewBtn?.addEventListener("click", () => this.openModal());
    this.els.modalClose?.addEventListener("click", () => this.closeModal());
    this.els.modalCancel?.addEventListener("click", () => this.closeModal());
    this.els.modalSave?.addEventListener("click", () => this.savePipeline());
    this.els.fieldTriggerKind?.addEventListener("change", () => this._toggleTriggerFields());
    this.els.addStepBtn?.addEventListener("click", () => this._addStepRow());
    this.els.detailsClose?.addEventListener("click", () => this.closeDetails());
    this.els.detailsOk?.addEventListener("click", () => this.closeDetails());
    this.els.detailsRun?.addEventListener("click", () => {
      if (this._detailsPipelineId) this._runPipeline(this._detailsPipelineId);
    });
    this.els.detailsCancel?.addEventListener("click", () => {
      if (this._detailsPipelineId) void this._cancelPipeline(this._detailsPipelineId);
    });
  }

  _bindEvents() {
    this._on("pipeline.started", (payload) => {
      if (payload?.pipelineId && payload?.runId) {
        this._activeRunByPipeline.set(payload.pipelineId, payload.runId);
      }
      if (this._detailsPipelineId === payload?.pipelineId) {
        this._detailsRunId = payload?.runId ?? this._detailsRunId;
        if (this.els.detailsCancel) this.els.detailsCancel.hidden = false;
        if (this.els.detailsRun) this.els.detailsRun.disabled = true;
        if (this.els.detailsStatus) {
          this.els.detailsStatus.textContent = "running";
          this.els.detailsStatus.dataset.status = "running";
        }
      }
      void this.loadPipelines();
    });
    this._on("pipeline.step_updated", (payload) => {
      if (this._detailsPipelineId !== payload?.pipelineId) return;
      // Ignore stale events from a previous run when a newer run is tracked.
      if (this._detailsRunId && payload.runId && payload.runId !== this._detailsRunId) return;
      this._liveStepStatus.set(payload.stepId, payload.status);
      this._patchDagStep(payload.stepId, payload.status, payload.summary, payload.error);
    });
    this._on("pipeline.completed", (payload) => {
      if (payload?.pipelineId) {
        this._activeRunByPipeline.delete(payload.pipelineId);
        this._clearDetailsRunning(payload.pipelineId, payload.runId);
      }
      this.notify(`Pipeline “${payload.name}” completed`, "success");
      void this._refreshAfterRun(payload.pipelineId);
    });
    this._on("pipeline.failed", (payload) => {
      if (payload?.pipelineId) {
        this._activeRunByPipeline.delete(payload.pipelineId);
        this._clearDetailsRunning(payload.pipelineId, payload.runId);
      }
      this.notify(`Pipeline “${payload.name}” failed: ${payload.error}`, "error");
      void this._refreshAfterRun(payload.pipelineId);
    });
    this._on("pipeline.cancelled", (payload) => {
      if (payload?.pipelineId) {
        this._activeRunByPipeline.delete(payload.pipelineId);
        this._clearDetailsRunning(payload.pipelineId, payload.runId);
      }
      this.notify(`Pipeline “${payload.name}” cancelled`, "info");
      void this._refreshAfterRun(payload.pipelineId);
    });
  }

  /**
   * When a run for the currently-open detail ends, reset the modal UI so it
   * does not stay "stuck running" (Run disabled / Cancel visible) after the
   * backend has finished.
   */
  _clearDetailsRunning(pipelineId, runId) {
    if (this._detailsPipelineId !== pipelineId) return;
    if (this._detailsRunId && runId && this._detailsRunId !== runId) return; // stale event from an older run
    this._detailsRunId = null;
    if (this.els.detailsCancel) this.els.detailsCancel.hidden = true;
    if (this.els.detailsRun) {
      this.els.detailsRun.textContent = "▶ Run now";
      this.els.detailsRun.disabled = false;
    }
  }

  _toggleTriggerFields() {
    const kind = this.els.fieldTriggerKind?.value ?? "event";
    if (this.els.scheduleFields) this.els.scheduleFields.hidden = kind !== "schedule";
    if (this.els.eventFields) this.els.eventFields.hidden = kind !== "event";
  }

  async loadPipelines() {
    try {
      this.els.error.hidden = true;
      this.els.list.setAttribute("aria-busy", "true");
      const result = await sendRequest("pipeline.list", {});
      this.pipelines = result.pipelines ?? [];
      this._renderList();
    } catch (err) {
      this.els.errorMessage.textContent = err.message ?? String(err);
      this.els.error.hidden = false;
    } finally {
      this.els.list.setAttribute("aria-busy", "false");
    }
  }

  _renderList() {
    this.els.list.innerHTML = "";
    this.els.empty.hidden = this.pipelines.length > 0;
    for (const pipeline of this.pipelines) {
      this.els.list.appendChild(this._renderCard(pipeline));
    }
  }

  _renderCard(pipeline) {
    const card = document.createElement("div");
    card.className = "pipeline-card";
    card.dataset.status = String(pipeline.lastStatus ?? "").toLowerCase();
    card.setAttribute("role", "listitem");
    const status = pipeline.lastStatus ?? "—";
    const stepCount = pipeline.steps?.length ?? 0;
    let triggerLabel = "trigger: —";
    if (pipeline.trigger?.kind === "event") {
      triggerLabel = `event: ${pipeline.trigger.pattern}`;
    } else if (pipeline.trigger?.kind === "schedule") {
      const key = describeScheduleKey(pipeline.trigger.schedule);
      triggerLabel = key ? `schedule: ${key}` : "schedule";
      if (pipeline.nextRunAt) {
        triggerLabel += ` · next ${new Date(pipeline.nextRunAt).toLocaleString()}`;
      }
    }
    const steps = Array.isArray(pipeline.steps) ? pipeline.steps : [];
    const visibleSteps = steps.slice(0, 4);
    const remainingSteps = Math.max(0, steps.length - visibleSteps.length);
    const statusLabel = status === "—" ? "not run" : String(status);
    card.innerHTML = `
      <div class="pipeline-card-signal" aria-hidden="true"><span></span></div>
      <div class="pipeline-card-body">
        <div class="pipeline-card-header">
          <div class="pipeline-card-heading">
            <div class="pipeline-card-kicker"><span class="pipeline-card-kicker-mark">◆</span> PIPELINE <span class="pipeline-card-slash">/</span> ${this._escape(String(pipeline.id ?? "local"))}</div>
            <h2 class="pipeline-card-name">${this._escape(pipeline.name)}</h2>
          </div>
          <span class="pipeline-card-status" data-status="${this._escape(String(status))}"><span class="pipeline-status-dot"></span>${this._escape(statusLabel)}</span>
        </div>
        ${pipeline.description ? `<p class="pipeline-card-desc">${this._escape(pipeline.description)}</p>` : '<p class="pipeline-card-desc is-muted">No description — add context for the next run.</p>'}
        <div class="pipeline-card-flow" aria-label="${stepCount} pipeline steps">
          ${visibleSteps.length ? visibleSteps.map((step, index) => `
            <div class="pipeline-flow-step">
              <span class="pipeline-flow-index">${String(index + 1).padStart(2, "0")}</span>
              <span class="pipeline-flow-name">${this._escape(step.name || step.id || `Step ${index + 1}`)}</span>
              <span class="pipeline-flow-kind">${step.action?.type === "tool" ? "TOOL" : "AGENT"}</span>
            </div>
            ${index < visibleSteps.length - 1 ? '<span class="pipeline-flow-arrow" aria-hidden="true">→</span>' : ''}
          `).join("") : '<span class="pipeline-flow-empty">No steps configured</span>'}
          ${remainingSteps ? `<span class="pipeline-flow-more">+${remainingSteps} more</span>` : ""}
        </div>
        <div class="pipeline-card-footer">
          <div class="pipeline-card-meta">
            <span class="pipeline-meta-item"><span class="pipeline-meta-label">TRIGGER</span>${this._escape(triggerLabel)}</span>
            <span class="pipeline-meta-item"><span class="pipeline-meta-label">STEPS</span>${stepCount}</span>
            ${pipeline.enabled ? '<span class="pipeline-enabled"><span class="pipeline-enabled-dot"></span> armed</span>' : '<span class="pipeline-disabled">paused</span>'}
          </div>
          <div class="pipeline-card-actions">
            <button class="pipeline-action pipeline-action-primary" data-action="details">Inspect</button>
            <button class="pipeline-action" data-action="run">Run now</button>
            <button class="pipeline-action pipeline-action-icon" data-action="more" aria-label="More pipeline actions" title="More actions">•••</button>
          </div>
        </div>
      </div>
    `;
    card.querySelector('[data-action="details"]').addEventListener("click", () => this.openDetails(pipeline));
    card.querySelector('[data-action="run"]').addEventListener("click", () => this._runPipeline(pipeline.id));
    card.querySelector('[data-action="more"]').addEventListener("click", (event) => this._openCardMenu(event, pipeline));
    return card;
  }

  _openCardMenu(event, pipeline) {
    const menu = document.createElement("div");
    menu.className = "pipeline-card-menu";
    menu.innerHTML = `
      <button type="button" data-menu-action="edit">Edit pipeline</button>
      <button type="button" data-menu-action="toggle">${pipeline.enabled ? "Pause pipeline" : "Resume pipeline"}</button>
      <button type="button" class="is-danger" data-menu-action="delete">Delete pipeline</button>
    `;
    const anchor = event.currentTarget;
    anchor.parentElement.appendChild(menu);
    const close = () => menu.remove();
    menu.querySelector('[data-menu-action="edit"]').addEventListener("click", () => { close(); this.openModal(pipeline); });
    menu.querySelector('[data-menu-action="toggle"]').addEventListener("click", () => { close(); this._togglePipeline(pipeline); });
    menu.querySelector('[data-menu-action="delete"]').addEventListener("click", () => { close(); this._deletePipeline(pipeline.id); });
    requestAnimationFrame(() => document.addEventListener("click", (clickEvent) => {
      if (!menu.contains(clickEvent.target) && clickEvent.target !== anchor) close();
    }, { once: true }));
  }

  openModal(pipeline = null) {
    this.editingPipeline = pipeline;
    this.steps = pipeline ? JSON.parse(JSON.stringify(pipeline.steps)) : [];
    this.els.modalTitle.textContent = pipeline ? "Edit pipeline" : "New pipeline";
    this.els.fieldName.value = pipeline?.name ?? "";
    this.els.fieldDescription.value = pipeline?.description ?? "";
    const kind = pipeline?.trigger?.kind === "schedule" ? "schedule" : "event";
    this.els.fieldTriggerKind.value = kind;
    this._toggleTriggerFields();
    this.els.fieldSchedule.value = kind === "schedule" && pipeline?.trigger?.schedule
      ? describeScheduleKey(pipeline.trigger.schedule)
      : "";
    this.els.fieldEventPattern.value = pipeline?.trigger?.kind === "event"
      ? (pipeline.trigger.pattern ?? "")
      : "";
    this.els.fieldEventPlugin.value = pipeline?.trigger?.kind === "event"
      ? (pipeline.trigger.pluginId ?? "")
      : "";
    this._renderSteps();
    this.els.modal.classList.add("active");
  }

  closeModal() {
    this.els.modal.classList.remove("active");
    this.editingPipeline = null;
    this.steps = [];
  }

  _renderSteps() {
    this.els.stepsList.innerHTML = "";
    for (let i = 0; i < this.steps.length; i++) {
      this.els.stepsList.appendChild(this._renderStepRow(this.steps[i], i));
    }
  }

  _renderStepRow(step, index) {
    const row = document.createElement("div");
    row.className = "pipeline-step-row";
    const otherStepIds = this.steps.map((s, j) => (j !== index ? s.id : null)).filter(Boolean);
    const depOptions = otherStepIds.map((id) =>
      `<option value="${this._escapeAttr(id)}" ${step.dependsOn?.includes(id) ? "selected" : ""}>${this._escape(id)}</option>`,
    ).join("");
    row.innerHTML = `
      <div class="pipeline-step-header">
        <span class="pipeline-step-index">#${index + 1}</span>
        <input type="text" class="pipeline-step-id" placeholder="step-id" value="${this._escapeAttr(step.id ?? "")}" data-field="id">
        <input type="text" class="pipeline-step-name" placeholder="Step name" value="${this._escapeAttr(step.name ?? "")}" data-field="name">
        <button class="ghost-btn danger" data-action="remove-step">Remove</button>
      </div>
      <label class="form-label">Action type
        <select data-field="action-type">
          <option value="agent" ${step.action?.type === "agent" ? "selected" : ""}>Agent</option>
          <option value="tool" ${step.action?.type === "tool" ? "selected" : ""}>Tool</option>
        </select>
      </label>
      <div class="pipeline-step-agent" ${step.action?.type === "tool" ? "hidden" : ""}>
        <label class="form-label">Instructions<textarea rows="2" data-field="agent-prompt" placeholder="Classify this email…">${this._escape(step.action?.prompt ?? "")}</textarea></label>
      </div>
      <div class="pipeline-step-tool" ${step.action?.type !== "tool" ? "hidden" : ""}>
        <label class="form-label">Plugin ID<input type="text" data-field="tool-plugin" value="${this._escapeAttr(step.action?.pluginId ?? "")}" placeholder="nusashell.mail"></label>
        <label class="form-label">Tool name<input type="text" data-field="tool-name" value="${this._escapeAttr(step.action?.toolName ?? "")}" placeholder="mail_send"></label>
        <label class="form-label">Args (JSON)<textarea rows="2" data-field="tool-args" placeholder='{"to":"x@y.com"}'>${this._escape(step.action?.args ? JSON.stringify(step.action.args, null, 2) : "")}</textarea></label>
      </div>
      <label class="form-label">Depends on (step IDs)
        <select multiple data-field="depends-on">${depOptions}</select>
      </label>
      <label class="form-label">Output key (optional)<input type="text" data-field="output-key" value="${this._escapeAttr(step.outputKey ?? "")}" placeholder="classification"></label>
    `;
    row.querySelector('[data-action="remove-step"]').addEventListener("click", () => {
      this.steps.splice(index, 1);
      this._renderSteps();
    });
    row.querySelector('[data-field="action-type"]').addEventListener("change", (e) => {
      step.action = step.action ?? {};
      step.action.type = e.target.value;
      row.querySelector(".pipeline-step-agent").hidden = e.target.value !== "agent";
      row.querySelector(".pipeline-step-tool").hidden = e.target.value !== "tool";
    });
    this._bindStepField(row, step, "id", "id");
    this._bindStepField(row, step, "name", "name");
    this._bindStepField(row, step, "agent-prompt", "agent-prompt");
    this._bindStepField(row, step, "tool-plugin", "tool-plugin");
    this._bindStepField(row, step, "tool-name", "tool-name");
    this._bindStepField(row, step, "output-key", "output-key");
    row.querySelector('[data-field="tool-args"]').addEventListener("input", (e) => {
      step._toolArgsRaw = e.target.value;
    });
    row.querySelector('[data-field="depends-on"]').addEventListener("change", (e) => {
      step.dependsOn = Array.from(e.target.selectedOptions).map((o) => o.value);
    });
    return row;
  }

  _bindStepField(row, step, field, key) {
    const el = row.querySelector(`[data-field="${field}"]`);
    if (!el) return;
    el.addEventListener("input", (e) => {
      step[key] = e.target.value;
      if (key === "agent-prompt") {
        step.action = step.action ?? { type: "agent" };
        step.action.type = "agent";
        step.action.prompt = e.target.value;
      } else if (key === "tool-plugin") {
        step.action = step.action ?? { type: "tool", toolName: "", args: {} };
        step.action.type = "tool";
        step.action.pluginId = e.target.value;
      } else if (key === "tool-name") {
        step.action = step.action ?? { type: "tool", pluginId: "", args: {} };
        step.action.type = "tool";
        step.action.toolName = e.target.value;
      }
    });
  }

  _addStepRow() {
    this.steps.push({
      id: `step-${this.steps.length + 1}`,
      name: "",
      action: { type: "agent", prompt: "" },
    });
    this._renderSteps();
  }

  async savePipeline() {
    const name = this.els.fieldName.value.trim();
    if (!name) {
      this.notify("Pipeline name is required", "error");
      return;
    }
    if (this.steps.length === 0) {
      this.notify("Pipeline must have at least one step", "error");
      return;
    }
    for (const step of this.steps) {
      if (!step.id?.trim()) {
        this.notify("Every step must have an id", "error");
        return;
      }
      if (!step.name?.trim()) {
        this.notify(`Step "${step.id}" must have a name`, "error");
        return;
      }
      if (step.action?.type === "tool") {
        const raw = step._toolArgsRaw;
        if (raw !== undefined && raw.trim() !== "") {
          try {
            step.action.args = JSON.parse(raw);
          } catch {
            this.notify(`Step "${step.id}" args must be valid JSON`, "error");
            return;
          }
        } else if (!step.action.args || typeof step.action.args !== "object") {
          step.action.args = {};
        }
      }
    }
    const trigger = await this._buildTrigger();
    if (!trigger) return;
    const steps = this.steps.map((s) => {
      const step = { id: s.id, name: s.name, action: s.action };
      if (s.dependsOn?.length) step.dependsOn = s.dependsOn;
      if (s.outputKey) step.outputKey = s.outputKey;
      return step;
    });
    const payload = { name, trigger, steps };
    if (this.els.fieldDescription.value.trim()) {
      payload.description = this.els.fieldDescription.value.trim();
    }
    try {
      if (this.editingPipeline) {
        await sendRequest("pipeline.update", { id: this.editingPipeline.id, ...payload });
        this.notify("Pipeline updated", "success");
      } else {
        await sendRequest("pipeline.add", payload);
        this.notify("Pipeline created", "success");
      }
      this.closeModal();
      await this.loadPipelines();
    } catch (err) {
      this.notify(`Failed to save: ${err.message ?? err}`, "error");
    }
  }

  async _buildTrigger() {
    const kind = this.els.fieldTriggerKind?.value ?? "event";
    if (kind === "schedule") {
      const text = this.els.fieldSchedule?.value.trim() ?? "";
      if (!text) {
        this.notify("Schedule is required", "error");
        return null;
      }
      try {
        const validated = await sendRequest("job.validate-schedule", { schedule: text });
        if (validated && validated.ok === false) {
          this.notify(validated.error ?? "Invalid schedule", "error");
          return null;
        }
      } catch (err) {
        this.notify(err.message ?? "Invalid schedule", "error");
        return null;
      }
      const schedule = parseScheduleText(text);
      if (!schedule) {
        this.notify("Unrecognized schedule format", "error");
        return null;
      }
      return { kind: "schedule", schedule };
    }
    const pattern = this.els.fieldEventPattern.value.trim();
    if (!pattern) {
      this.notify("Event pattern is required", "error");
      return null;
    }
    const trigger = { kind: "event", pattern };
    const pluginId = this.els.fieldEventPlugin.value.trim();
    if (pluginId) trigger.pluginId = pluginId;
    return trigger;
  }

  async _runPipeline(id) {
    try {
      if (this._detailsPipelineId === id && this.els.detailsRun) {
        this.els.detailsRun.textContent = "Starting…";
        this.els.detailsRun.disabled = true;
      }
      if (this.els.detailsCancel) this.els.detailsCancel.hidden = false;
      this._liveStepStatus.clear();
      // pipeline.run is fire-and-track: it returns immediately (no runId
      // necessarily present) and the run progresses via pipeline.* events.
      const result = await sendRequest("pipeline.run", { id }, 10000);
      if (result.ok) {
        this.notify("Pipeline started", "success");
        if (result.runId) this._activeRunByPipeline.set(id, result.runId);
        this._scheduleRefresh(id);
      } else {
        const code = result.errorCode ? ` (${result.errorCode})` : "";
        this.notify(`Pipeline failed to start: ${result.error ?? "unknown"}${code}`, "error");
        await this._refreshAfterRun(id);
      }
    } catch (err) {
      this.notify(`Failed to run: ${err.message ?? err}`, "error");
      await this._refreshAfterRun(id);
    } finally {
      // Only re-enable the details Run button if there is no active run.
      if (this.els.detailsRun && !this._activeRunByPipeline.has(this._detailsPipelineId)) {
        this.els.detailsRun.textContent = "▶ Run now";
        this.els.detailsRun.disabled = false;
      }
    }
  }

  async _cancelPipeline(id) {
    try {
      const runId = this._activeRunByPipeline.get(id) ?? this._detailsRunId ?? id;
      const result = await sendRequest("pipeline.cancel", { id: runId });
      if (result.ok) this.notify("Cancel requested", "success");
      else this.notify(result.error ?? "Nothing to cancel", "error");
      await this._refreshAfterRun(id);
    } catch (err) {
      this.notify(`Failed to cancel: ${err.message ?? err}`, "error");
    }
  }

  _scheduleRefresh(id) {
    if (this._runRefreshTimer) clearTimeout(this._runRefreshTimer);
    this._runRefreshTimer = setTimeout(() => {
      this._runRefreshTimer = null;
      void this._refreshAfterRun(id);
    }, 800);
  }

  async _refreshAfterRun(id) {
    await this.loadPipelines();
    if (this._detailsPipelineId === id && this.els.detailsModal.classList.contains("active")) {
      const updated = this.pipelines.find((p) => p.id === id);
      if (updated) this.openDetails(updated);
    }
  }

  async _togglePipeline(pipeline) {
    try {
      await sendRequest("pipeline.update", { id: pipeline.id, enabled: !pipeline.enabled });
      this.notify(pipeline.enabled ? "Pipeline disabled" : "Pipeline enabled", "success");
      await this.loadPipelines();
    } catch (err) {
      this.notify(`Failed: ${err.message ?? err}`, "error");
    }
  }

  async _deletePipeline(id) {
    if (!await confirmDialog({ title: "Delete pipeline?", message: "This removes the pipeline and its steps.", confirmLabel: "Delete", danger: true })) return;
    try {
      await sendRequest("pipeline.remove", { id });
      this.notify("Pipeline deleted", "success");
      await this.loadPipelines();
    } catch (err) {
      this.notify(`Failed to delete: ${err.message ?? err}`, "error");
    }
  }

  openDetails(pipeline) {
    this._detailsPipelineId = pipeline.id;
    this._detailsPipeline = pipeline;
    this._detailsLatestRun = null;
    this._detailsStepId = null;
    this._detailsRunId = this._activeRunByPipeline.get(pipeline.id) ?? pipeline.lastRunId ?? null;
    this._liveStepStatus.clear();
    this.els.detailsTitle.textContent = pipeline.name;

    const status = pipeline.lastStatus ?? "—";
    this.els.detailsStatus.textContent = status;
    this.els.detailsStatus.dataset.status = String(status);

    let triggerLine = "Trigger — —";
    if (pipeline.trigger?.kind === "event") {
      triggerLine = `Trigger — event: ${pipeline.trigger.pattern ?? ""}`;
    } else if (pipeline.trigger?.kind === "schedule") {
      triggerLine = `Trigger — schedule: ${describeScheduleKey(pipeline.trigger.schedule)}`;
    }
    const lastRun = pipeline.lastRunAt ? new Date(pipeline.lastRunAt).toLocaleString() : "never";
    const lines = [triggerLine, `Last run — ${lastRun}`];
    if (pipeline.nextRunAt && pipeline.trigger?.kind === "schedule") {
      lines.push(`Next run — ${new Date(pipeline.nextRunAt).toLocaleString()}`);
    }
    if (this._detailsRunId) lines.push(`Run id — ${this._detailsRunId}`);
    if (!pipeline.enabled) lines.push("disabled");
    this.els.detailsMeta.replaceChildren();
    for (const line of lines) {
      const span = document.createElement("span");
      span.textContent = line;
      this.els.detailsMeta.appendChild(span);
    }

    void this._renderDetailsDag(pipeline);
    this.els.detailsOutput.hidden = true;
    this.els.detailsOutput.replaceChildren();
    if (this.els.detailsStep) {
      this.els.detailsStep.hidden = true;
      this.els.detailsStep.replaceChildren();
    }

    this.els.detailsRun.textContent = "▶ Run now";
    const running = pipeline.lastStatus === "running" || this._activeRunByPipeline.has(pipeline.id);
    this.els.detailsRun.disabled = running || !pipeline.enabled;
    if (this.els.detailsCancel) this.els.detailsCancel.hidden = !running;
    this.els.detailsModal.classList.add("active");
  }

  async _renderDetailsDag(pipeline) {
    const levels = this._topoLevels(pipeline.steps ?? []);
    let stepStatusById = new Map();
    let latest = null;
    try {
      const history = await sendRequest("pipeline.runs", {
        id: pipeline.id,
        limit: 1,
        includeBody: true,
      });
      latest = history.runs?.[0] ?? null;
      this._detailsLatestRun = latest;
      if (latest?.runId && !this._activeRunByPipeline.has(pipeline.id)) this._detailsRunId = latest.runId;
      if (latest?.stepRuns) {
        for (const sr of latest.stepRuns) {
          stepStatusById.set(sr.stepId, sr.status);
        }
      }
    } catch {
      /* history optional */
    }
    for (const [stepId, status] of this._liveStepStatus) {
      stepStatusById.set(stepId, status);
    }
    if (stepStatusById.size === 0 && pipeline.lastStatus === "ok") {
      for (const step of pipeline.steps ?? []) stepStatusById.set(step.id, "ok");
    }

    this.els.detailsDag.innerHTML = "";
    for (const level of levels) {
      const col = document.createElement("div");
      col.className = "pipeline-dag-level";
      for (const step of level) {
        col.appendChild(this._renderDagNode(step, stepStatusById.get(step.id) ?? "pending"));
      }
      this.els.detailsDag.appendChild(col);
    }
    if (levels.length === 0) {
      const empty = document.createElement("p");
      empty.className = "pipeline-node-sub";
      empty.style.padding = "8px";
      empty.textContent = "No steps defined.";
      this.els.detailsDag.appendChild(empty);
    }

    this._renderDetailsOutput(pipeline, latest);
  }

  _renderDetailsOutput(pipeline, latest) {
    const entries = [];
    if (pipeline.lastError) {
      entries.push(`
        <article class="pipeline-output-entry pipeline-output-error">
          <div class="pipeline-output-entry-head"><span>Run error</span><span data-status="error">FAILED</span></div>
          <div class="pipeline-output-markdown">${renderJobOutputMarkdown(pipeline.lastError)}</div>
        </article>
      `);
    }
    for (const stepRun of latest?.stepRuns ?? []) {
      const body = stepRun.outputPreview || stepRun.summary || stepRun.error || "";
      if (!body) continue;
      const label = stepRun.stepId || "step";
      const status = stepRun.status || "completed";
      const truncated = stepRun.outputTruncated ? " · truncated" : "";
      entries.push(`
        <article class="pipeline-output-entry" data-step-output="${this._escapeAttr(label)}">
          <div class="pipeline-output-entry-head"><span>${this._escape(label)}${this._escape(truncated)}</span><span data-status="${this._escape(status)}">${this._escape(status)}</span></div>
          <div class="pipeline-output-markdown">${renderJobOutputMarkdown(body)}</div>
        </article>
      `);
    }
    if (!entries.length) return;
    this.els.detailsOutput.hidden = false;
    this.els.detailsOutput.innerHTML = `
      <div class="pipeline-output-heading"><span>Run output</span><span>${entries.length} entr${entries.length === 1 ? "y" : "ies"}</span></div>
      <div class="pipeline-output-list">${entries.join("")}</div>
    `;
  }

  _selectDetailsStep(stepId) {
    const pipeline = this._detailsPipeline;
    const step = pipeline?.steps?.find((item) => item.id === stepId);
    if (!step || !this.els.detailsStep) return;
    this._detailsStepId = stepId;

    this.els.detailsDag?.querySelectorAll(".pipeline-node").forEach((node) => {
      const selected = node.dataset.stepId === stepId;
      node.classList.toggle("is-selected", selected);
      node.setAttribute("aria-pressed", String(selected));
    });

    const stepRun = this._detailsLatestRun?.stepRuns?.find((run) => run.stepId === stepId);
    const action = step.action?.type === "tool"
      ? `Tool · ${step.action.pluginId ?? "plugin"}/${step.action.toolName ?? "tool"}`
      : "Agent turn";
    const dependency = step.dependsOn?.length ? step.dependsOn.join(", ") : "None";
    const outputKey = step.outputKey || "Not stored";
    const status = stepRun?.status ?? this._liveStepStatus.get(stepId) ?? "queued";
    const body = stepRun?.outputPreview || stepRun?.summary || stepRun?.error || "No output recorded for this step yet.";
    const started = stepRun?.startedAt ? new Date(stepRun.startedAt).toLocaleString() : "—";
    const completed = stepRun?.completedAt ? new Date(stepRun.completedAt).toLocaleString() : "—";
    const definition = step.action?.type === "tool"
      ? `${step.action.pluginId ?? "plugin"}/${step.action.toolName ?? "tool"}`
      : step.action?.prompt || "No agent instructions configured.";

    this.els.detailsStep.hidden = false;
    this.els.detailsStep.innerHTML = `
      <div class="pipeline-step-inspector-head">
        <div><span class="pipeline-step-inspector-kicker">Selected step</span><strong>${this._escape(step.name || step.id)}</strong></div>
        <span class="pipeline-step-inspector-status" data-status="${this._escape(status)}">${this._escape(status)}</span>
      </div>
      <div class="pipeline-step-inspector-grid">
        <div><span>Action</span><strong>${this._escape(action)}</strong></div>
        <div><span>Depends on</span><strong>${this._escape(dependency)}</strong></div>
        <div><span>Output key</span><strong>${this._escape(outputKey)}</strong></div>
        <div><span>Started / completed</span><strong>${this._escape(started)} → ${this._escape(completed)}</strong></div>
      </div>
      <section class="pipeline-step-inspector-section pipeline-step-inspector-definition">
        <div class="pipeline-step-inspector-section-head"><span>Definition</span><span>Configured instruction</span></div>
        <code>${this._escape(definition)}</code>
      </section>
      <section class="pipeline-step-inspector-section pipeline-step-inspector-result">
        <div class="pipeline-step-inspector-section-head"><span>Latest output</span><span>${this._escape(status)}</span></div>
        <div class="pipeline-step-inspector-output">${renderJobOutputMarkdown(body)}</div>
      </section>
    `;

    const output = Array.from(this.els.detailsOutput?.querySelectorAll("[data-step-output]") ?? [])
      .find((entry) => entry.dataset.stepOutput === stepId);
    this.els.detailsOutput?.querySelectorAll("[data-step-output]").forEach((entry) => {
      entry.classList.toggle("is-focused", entry === output);
    });
    output?.scrollIntoView?.({ block: "nearest" });
  }

  closeDetails() {
    this.els.detailsModal.classList.remove("active");
    this._detailsPipelineId = null;
    this._detailsRunId = null;
    this._detailsPipeline = null;
    this._detailsLatestRun = null;
    this._detailsStepId = null;
    this._liveStepStatus.clear();
    if (this.els.detailsRun) this.els.detailsRun.disabled = false;
    if (this._runRefreshTimer) {
      clearTimeout(this._runRefreshTimer);
      this._runRefreshTimer = null;
    }
  }

  _patchDagStep(stepId, status, summary, error) {
    const node = this.els.detailsDag?.querySelector(`[data-step-id="${CSS.escape(stepId)}"]`);
    if (!node) return;
    node.dataset.stepStatus = status;
    const iconEl = node.querySelector(".pipeline-node-icon");
    if (iconEl) iconEl.textContent = this._statusIcon(status);
    const subEl = node.querySelector(".pipeline-node-sub");
    if (subEl && (summary || error)) {
      const base = subEl.dataset.baseSub ?? subEl.textContent;
      subEl.dataset.baseSub = base;
      subEl.textContent = error ? `${base} · ${error}` : summary ? `${base} · ${summary}` : base;
    }
  }

  _statusIcon(status) {
    if (status === "ok") return "✓";
    if (status === "error") return "!";
    if (status === "running") return "…";
    if (status === "skipped") return "–";
    if (status === "cancelled") return "×";
    return "◔";
  }

  _renderDagNode(step, stepStatus) {
    const node = document.createElement("button");
    node.type = "button";
    node.className = "pipeline-node";
    node.dataset.stepId = step.id ?? "";
    node.dataset.stepStatus = stepStatus;
    node.setAttribute("aria-pressed", String(this._detailsStepId === step.id));
    node.setAttribute("aria-label", `Inspect step ${step.name || step.id || "unnamed"}`);
    const actionLabel = step.action?.type === "tool"
      ? `tool · ${step.action.pluginId ?? ""}/${step.action.toolName ?? ""}`
      : "agent";
    const sub = `${actionLabel}${step.outputKey ? ` → ${step.outputKey}` : ""}`;
    const iconEl = document.createElement("span");
    iconEl.className = "pipeline-node-icon";
    iconEl.textContent = this._statusIcon(stepStatus);
    const body = document.createElement("div");
    body.className = "pipeline-node-body";
    const nameEl = document.createElement("div");
    nameEl.className = "pipeline-node-name";
    nameEl.title = step.id ?? "";
    nameEl.textContent = step.name || step.id || "";
    const subEl = document.createElement("div");
    subEl.className = "pipeline-node-sub";
    subEl.dataset.baseSub = sub;
    subEl.textContent = sub;
    body.append(nameEl, subEl);
    node.append(iconEl, body);
    node.addEventListener("click", () => this._selectDetailsStep(step.id));
    return node;
  }

  _topoLevels(steps) {
    const byId = new Map(steps.map((s) => [s.id, s]));
    const depth = new Map();
    const compute = (id, seen = new Set()) => {
      if (depth.has(id)) return depth.get(id);
      if (seen.has(id)) return 0;
      seen.add(id);
      const step = byId.get(id);
      const deps = (step?.dependsOn ?? []).filter((d) => byId.has(d));
      const d = deps.length === 0 ? 0 : Math.max(...deps.map((dep) => compute(dep, seen))) + 1;
      depth.set(id, d);
      return d;
    };
    steps.forEach((s) => compute(s.id));
    const levels = [];
    for (const step of steps) {
      const d = depth.get(step.id) ?? 0;
      (levels[d] ??= []).push(step);
    }
    return levels;
  }

  _escape(str) {
    const div = document.createElement("div");
    div.textContent = str ?? "";
    return div.innerHTML;
  }

  _escapeAttr(str) {
    return this._escape(str).replace(/"/g, "&quot;");
  }
}
