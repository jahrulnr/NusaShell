/**
 * Pipelines controller — manages the Pipelines view and pipeline editor modal.
 * Supports creating/editing multi-step DAG pipelines with event or schedule triggers.
 * Each step has: id, name, action (agent/tool), dependsOn, condition, outputKey.
 */

import { sendRequest } from "./ws-client.js";
import { confirmDialog } from "./ui-dialogs.js";

export class PipelinesController {
  constructor({ notify }) {
    this.notify = notify;
    this.pipelines = [];
    this.editingPipeline = null;
    this.steps = [];

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
    };

    this._bind();
  }

  _bind() {
    this.els.newBtn?.addEventListener("click", () => this.openModal());
    this.els.modalClose?.addEventListener("click", () => this.closeModal());
    this.els.modalCancel?.addEventListener("click", () => this.closeModal());
    this.els.modalSave?.addEventListener("click", () => this.savePipeline());
    this.els.fieldTriggerKind?.addEventListener("change", () => this._toggleTriggerFields());
    this.els.addStepBtn?.addEventListener("click", () => this._addStepRow());
  }

  _toggleTriggerFields() {
    const kind = this.els.fieldTriggerKind.value;
    this.els.scheduleFields.hidden = kind !== "schedule";
    this.els.eventFields.hidden = kind !== "event";
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
      const card = this._renderCard(pipeline);
      this.els.list.appendChild(card);
    }
  }

  _renderCard(pipeline) {
    const card = document.createElement("div");
    card.className = "job-card";
    card.setAttribute("role", "listitem");
    const status = pipeline.lastStatus ?? "—";
    const stepCount = pipeline.steps?.length ?? 0;
    const triggerLabel = pipeline.trigger?.kind === "event"
      ? `event: ${pipeline.trigger.pattern}`
      : `schedule: ${pipeline.trigger?.schedule ? "configured" : "—"}`;
    card.innerHTML = `
      <div class="job-card-header">
        <span class="job-card-name">${this._escape(pipeline.name)}</span>
        <span class="job-card-status" data-status="${status}">${status}</span>
      </div>
      <div class="job-card-meta">
        <span>${triggerLabel}</span>
        <span>${stepCount} step${stepCount !== 1 ? "s" : ""}</span>
        ${pipeline.enabled ? "" : '<span class="job-card-paused">disabled</span>'}
      </div>
      ${pipeline.description ? `<p class="job-card-desc">${this._escape(pipeline.description)}</p>` : ""}
      <div class="job-card-actions">
        <button class="ghost-btn" data-action="edit">Edit</button>
        <button class="ghost-btn" data-action="run">Run now</button>
        <button class="ghost-btn" data-action="toggle">${pipeline.enabled ? "Disable" : "Enable"}</button>
        <button class="ghost-btn danger" data-action="delete">Delete</button>
      </div>
    `;
    card.querySelector('[data-action="edit"]').addEventListener("click", () => this.openModal(pipeline));
    card.querySelector('[data-action="run"]').addEventListener("click", () => this._runPipeline(pipeline.id));
    card.querySelector('[data-action="toggle"]').addEventListener("click", () => this._togglePipeline(pipeline));
    card.querySelector('[data-action="delete"]').addEventListener("click", () => this._deletePipeline(pipeline.id));
    return card;
  }

  openModal(pipeline = null) {
    this.editingPipeline = pipeline;
    this.steps = pipeline ? JSON.parse(JSON.stringify(pipeline.steps)) : [];
    this.els.modalTitle.textContent = pipeline ? "Edit pipeline" : "New pipeline";
    this.els.fieldName.value = pipeline?.name ?? "";
    this.els.fieldDescription.value = pipeline?.description ?? "";
    const triggerKind = pipeline?.trigger?.kind ?? "event";
    this.els.fieldTriggerKind.value = triggerKind;
    this._toggleTriggerFields();
    if (pipeline?.trigger?.kind === "schedule") {
      this.els.fieldSchedule.value = pipeline.trigger.schedule?.raw ?? "";
    } else {
      this.els.fieldSchedule.value = "";
    }
    this.els.fieldEventPattern.value = pipeline?.trigger?.pattern ?? "";
    this.els.fieldEventPlugin.value = pipeline?.trigger?.pluginId ?? "";
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
    const otherStepIds = this.steps.map((s, j) => j !== index ? s.id : null).filter(Boolean);
    const depOptions = otherStepIds.map(id =>
      `<option value="${id}" ${step.dependsOn?.includes(id) ? "selected" : ""}>${id}</option>`
    ).join("");
    row.innerHTML = `
      <div class="pipeline-step-header">
        <span class="pipeline-step-index">#${index + 1}</span>
        <input type="text" class="pipeline-step-id" placeholder="step-id" value="${this._escape(step.id ?? "")}" data-field="id">
        <input type="text" class="pipeline-step-name" placeholder="Step name" value="${this._escape(step.name ?? "")}" data-field="name">
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
        <label class="form-label">Plugin ID<input type="text" data-field="tool-plugin" value="${this._escape(step.action?.pluginId ?? "")}" placeholder="nusashell.mail"></label>
        <label class="form-label">Tool name<input type="text" data-field="tool-name" value="${this._escape(step.action?.toolName ?? "")}" placeholder="mail_send"></label>
        <label class="form-label">Args (JSON)<textarea rows="2" data-field="tool-args" placeholder='{"to":"x@y.com"}'>${step.action?.args ? JSON.stringify(step.action.args, null, 2) : ""}</textarea></label>
      </div>
      <label class="form-label">Depends on (step IDs)
        <select multiple data-field="depends-on">${depOptions}</select>
      </label>
      <label class="form-label">Output key (optional)<input type="text" data-field="output-key" value="${this._escape(step.outputKey ?? "")}" placeholder="classification"></label>
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
      step.dependsOn = Array.from(e.target.selectedOptions).map(o => o.value);
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
    const step = {
      id: `step-${this.steps.length + 1}`,
      name: "",
      action: { type: "agent", prompt: "" },
    };
    this.steps.push(step);
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
      if (step.action?.type === "tool" && step._toolArgsRaw) {
        try {
          step.action.args = JSON.parse(step._toolArgsRaw);
        } catch {
          this.notify(`Step "${step.id}" args must be valid JSON`, "error");
          return;
        }
      }
    }
    const trigger = this._buildTrigger();
    if (!trigger) return;
    const steps = this.steps.map(s => {
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

  _buildTrigger() {
    const kind = this.els.fieldTriggerKind.value;
    if (kind === "event") {
      const pattern = this.els.fieldEventPattern.value.trim();
      if (!pattern) {
        this.notify("Event pattern is required", "error");
        return null;
      }
      const trigger = { kind: "event", pattern };
      const pluginId = this.els.fieldEventPlugin.value.trim();
      if (pluginId) trigger.pluginId = pluginId;
      return trigger;
    } else {
      const schedule = this.els.fieldSchedule.value.trim();
      if (!schedule) {
        this.notify("Schedule is required", "error");
        return null;
      }
      return { kind: "schedule", schedule: { raw: schedule } };
    }
  }

  async _runPipeline(id) {
    try {
      const result = await sendRequest("pipeline.run", { id });
      if (result.ok) {
        this.notify("Pipeline started", "success");
      } else {
        this.notify(`Pipeline failed: ${result.error ?? "unknown"}`, "error");
      }
    } catch (err) {
      this.notify(`Failed to run: ${err.message ?? err}`, "error");
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

  _escape(str) {
    const div = document.createElement("div");
    div.textContent = str ?? "";
    return div.innerHTML;
  }
}
