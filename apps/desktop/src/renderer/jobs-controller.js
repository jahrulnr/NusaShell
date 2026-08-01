import { sendRequest, onEvent } from "./ws-client.js";

function describeJobSchedule(schedule) {
  if (!schedule) return "—";
  if (schedule.kind === "once") return `once @ ${schedule.runAt}`;
  if (schedule.kind === "interval") {
    const minutes = schedule.minutes;
    if (minutes % 1440 === 0) return `every ${minutes / 1440}d`;
    if (minutes % 60 === 0) return `every ${minutes / 60}h`;
    return `every ${minutes}m`;
  }
  if (schedule.kind === "cron") return `cron ${schedule.expr}`;
  return "—";
}

function describeJobMode(mode) {
  if (!mode) return "—";
  if (mode.type === "agent") return `agent: ${mode.prompt.slice(0, 60)}`;
  if (mode.type === "tool") return `tool: ${mode.pluginId}/${mode.toolName}`;
  return "—";
}

function describeLastStatus(status) {
  if (status === "ok") return "OK";
  if (status === "error") return "Error";
  return "Never";
}

function humanizeNextRun(nextRunAt) {
  if (!nextRunAt) return "—";
  const target = new Date(nextRunAt);
  if (Number.isNaN(target.getTime())) return "—";
  const minutes = Math.round((target.getTime() - Date.now()) / 60000);
  if (minutes < 0) return "due";
  if (minutes < 1) return "in <1m";
  if (minutes < 60) return `in ${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `in ${hours}h ${minutes % 60}m`;
  return target.toLocaleString();
}

export class JobsController {
  constructor({ notify }) {
    this.notify = notify;
    this.list = [];
    this.loading = false;
    this.pendingDeleteId = "";
    this._lastFocus = null;
    this.els = {
      list: document.getElementById("jobs-list"),
      empty: document.getElementById("jobs-empty"),
      emptyNewBtn: document.getElementById("jobs-empty-new-btn"),
      error: document.getElementById("jobs-error"),
      errorMessage: document.getElementById("jobs-error-message"),
      newBtn: document.getElementById("jobs-new-btn"),
      modal: document.getElementById("job-modal"),
      modalTitle: document.getElementById("job-modal-title"),
      modalClose: document.getElementById("job-modal-close"),
      modalCancel: document.getElementById("job-modal-cancel"),
      modalSave: document.getElementById("job-modal-save"),
      fieldName: document.getElementById("job-field-name"),
      fieldSchedule: document.getElementById("job-field-schedule"),
      scheduleHelp: document.getElementById("job-schedule-help"),
      fieldMode: document.getElementById("job-field-mode"),
      promptLabel: document.getElementById("job-agent-prompt-label"),
      fieldPrompt: document.getElementById("job-field-prompt"),
      toolFields: document.getElementById("job-tool-fields"),
      fieldPluginId: document.getElementById("job-field-plugin-id"),
      fieldToolName: document.getElementById("job-field-tool-name"),
      fieldArgs: document.getElementById("job-field-args"),
      fieldRepeat: document.getElementById("job-field-repeat"),
      outputModal: document.getElementById("job-output-modal"),
      outputTitle: document.getElementById("job-output-title"),
      outputClose: document.getElementById("job-output-close"),
      outputBody: document.getElementById("job-output-body"),
      deleteOverlay: document.getElementById("job-delete-overlay"),
      deleteDialog: document.getElementById("job-delete-dialog"),
      deleteTitle: document.getElementById("job-delete-title"),
      deleteCopy: document.getElementById("job-delete-copy"),
      deleteClose: document.getElementById("job-delete-close"),
      deleteCancel: document.getElementById("job-delete-cancel"),
      deleteConfirm: document.getElementById("job-delete-confirm"),
    };
  }

  initialize() {
    this.bind();
    onEvent("job.completed", (payload) => {
      this.notify(`Job “${payload.name}” completed`, "success");
      if (this._isViewActive()) void this.refresh();
    });
    onEvent("job.failed", (payload) => {
      this.notify(`Job “${payload.name}” failed: ${payload.error}`, "error");
      if (this._isViewActive()) void this.refresh();
    });
  }

  bind() {
    this.els.newBtn?.addEventListener("click", () => this.openModal());
    this.els.emptyNewBtn?.addEventListener("click", () => this.openModal());
    this.els.modalClose?.addEventListener("click", () => this.closeModal());
    this.els.modalCancel?.addEventListener("click", () => this.closeModal());
    this.els.modalSave?.addEventListener("click", () => void this.saveJob());
    this.els.fieldMode?.addEventListener("change", () => this._toggleModeFields());
    this.els.fieldSchedule?.addEventListener("blur", () => void this.validateSchedule());
    this.els.outputClose?.addEventListener("click", () => this.closeOutput());
    this.els.modal?.addEventListener("click", (event) => {
      if (event.target === this.els.modal) this.closeModal();
    });
    this.els.outputModal?.addEventListener("click", (event) => {
      if (event.target === this.els.outputModal) this.closeOutput();
    });
    this.els.deleteClose?.addEventListener("click", () => this.closeDeleteDialog());
    this.els.deleteCancel?.addEventListener("click", () => this.closeDeleteDialog());
    this.els.deleteOverlay?.addEventListener("click", () => this.closeDeleteDialog());
    this.els.deleteConfirm?.addEventListener("click", () => void this.confirmDelete());
  }

  _isViewActive() {
    return document.querySelector('.view[data-view="jobs"]')?.classList.contains("active") ?? false;
  }

  async refresh() {
    this.loading = true;
    this._renderLoading();
    try {
      const result = await sendRequest("job.list", {});
      this.list = result.jobs ?? [];
      this.loading = false;
      this._hideError();
      this.render();
    } catch (error) {
      this.loading = false;
      this._showError(error);
      this.render();
    }
  }

  _renderLoading() {
    if (!this.els.list) return;
    this.els.list.setAttribute("aria-busy", "true");
    this.els.list.textContent = "";
    this.els.empty.hidden = true;
    if (this.list.length === 0) {
      const loading = document.createElement("div");
      loading.className = "jobs-empty";
      loading.setAttribute("aria-live", "polite");
      loading.innerHTML = "<strong>Loading jobs…</strong>";
      this.els.list.appendChild(loading);
    }
  }

  _showError(error) {
    if (this.els.error && this.els.errorMessage) {
      this.els.errorMessage.textContent = error.message || String(error);
      this.els.error.hidden = false;
    }
    this.notify(`Could not load jobs: ${error.message || error}`, "error");
  }

  _hideError() {
    this.els.error?.setAttribute("hidden", "");
  }

  render() {
    if (!this.els.list) return;
    this.els.list.removeAttribute("aria-busy");
    this.els.list.textContent = "";
    if (this.loading) return;
    if (this.list.length === 0) {
      this.els.empty.hidden = false;
      return;
    }
    this.els.empty.hidden = true;
    for (const job of this.list) {
      this.els.list.appendChild(this._renderRow(job));
    }
  }

  _renderRow(job) {
    const row = document.createElement("div");
    row.className = "job-row";
    row.dataset.jobId = job.id;
    row.setAttribute("role", "listitem");

    const status = job.lastStatus ?? "idle";
    const statusDot = document.createElement("span");
    statusDot.className = `job-status-dot job-status-${status}`;
    statusDot.setAttribute("aria-hidden", "true");

    const info = document.createElement("div");
    info.className = "job-info";
    const title = document.createElement("div");
    title.className = "job-title";
    title.textContent = job.name;
    const meta = document.createElement("div");
    meta.className = "job-meta";
    const repeat = job.repeat?.times ? ` · ${job.repeat.completed}/${job.repeat.times}` : "";
    meta.textContent = `${describeJobSchedule(job.schedule)} · ${describeJobMode(job.mode)}${repeat}`;
    info.append(title, meta);

    const strip = document.createElement("div");
    strip.className = "job-strip";
    const next = document.createElement("div");
    next.className = "job-strip-next";
    if (job.enabled && job.nextRunAt) {
      next.textContent = `next ${humanizeNextRun(job.nextRunAt)}`;
      next.title = new Date(job.nextRunAt).toLocaleString();
    } else if (!job.enabled) {
      next.textContent = "paused";
    } else {
      next.textContent = "next —";
    }
    const last = document.createElement("div");
    last.className = `job-strip-last is-${status}`;
    last.textContent = `Last: ${describeLastStatus(job.lastStatus)}`;
    strip.append(next, last);
    if (status === "error" && job.lastError) {
      const errorLine = document.createElement("div");
      errorLine.className = "job-strip-error";
      errorLine.textContent = job.lastError;
      errorLine.title = job.lastError;
      strip.append(errorLine);
    }

    const actions = document.createElement("div");
    actions.className = "job-actions";
    const runBtn = document.createElement("button");
    runBtn.type = "button";
    runBtn.className = "mini-btn";
    runBtn.textContent = "Run";
    runBtn.dataset.control = "job-run-btn";
    runBtn.setAttribute("aria-label", `Run ${job.name} now`);
    runBtn.addEventListener("click", () => void this.runJob(job.id));
    const toggleBtn = document.createElement("button");
    toggleBtn.type = "button";
    toggleBtn.className = "mini-btn";
    toggleBtn.textContent = job.enabled ? "Pause" : "Resume";
    toggleBtn.dataset.control = "job-toggle-btn";
    toggleBtn.setAttribute("aria-label", `${job.enabled ? "Pause" : "Resume"} ${job.name}`);
    toggleBtn.addEventListener("click", () => void this.toggleJob(job.id, !job.enabled));
    const outputBtn = document.createElement("button");
    outputBtn.type = "button";
    outputBtn.className = "mini-btn";
    outputBtn.textContent = "Output";
    outputBtn.dataset.control = "job-output-btn";
    outputBtn.setAttribute("aria-label", `View output for ${job.name}`);
    outputBtn.addEventListener("click", () => void this.showOutput(job.id, job.name));
    const removeBtn = document.createElement("button");
    removeBtn.type = "button";
    removeBtn.className = "mini-btn danger";
    removeBtn.textContent = "Remove";
    removeBtn.dataset.control = "job-remove-btn";
    removeBtn.setAttribute("aria-label", `Remove ${job.name}`);
    removeBtn.addEventListener("click", () => this.openDeleteDialog(job.id, job.name));
    actions.append(runBtn, toggleBtn, outputBtn, removeBtn);

    row.append(statusDot, info, strip, actions);
    return row;
  }

  openModal() {
    this._lastFocus = document.activeElement;
    this.els.modalTitle.textContent = "New job";
    this.els.fieldName.value = "";
    this.els.fieldSchedule.value = "";
    this.els.fieldMode.value = "agent";
    this.els.fieldPrompt.value = "";
    this.els.fieldPluginId.value = "";
    this.els.fieldToolName.value = "";
    this.els.fieldArgs.value = "{}";
    this.els.fieldRepeat.value = "";
    this.els.scheduleHelp.textContent = "";
    this._toggleModeFields();
    this.els.modal.classList.add("active");
    this.els.fieldName.focus();
  }

  closeModal() {
    this.els.modal.classList.remove("active");
    this._restoreFocus();
  }

  _toggleModeFields() {
    const mode = this.els.fieldMode.value;
    const isAgent = mode === "agent";
    this.els.promptLabel.hidden = !isAgent;
    this.els.promptLabel.setAttribute("aria-hidden", String(!isAgent));
    this.els.toolFields.hidden = isAgent;
    this.els.toolFields.setAttribute("aria-hidden", String(isAgent));
  }

  async validateSchedule() {
    const schedule = this.els.fieldSchedule.value.trim();
    if (!schedule) {
      this.els.scheduleHelp.textContent = "";
      return;
    }
    try {
      const result = await sendRequest("job.validate-schedule", { schedule });
      this.els.scheduleHelp.textContent = result.ok ? `✓ ${result.description}` : `✗ ${result.error}`;
      this.els.scheduleHelp.style.color = result.ok ? "var(--accent)" : "var(--danger)";
    } catch (error) {
      this.els.scheduleHelp.textContent = `✗ ${error.message || error}`;
      this.els.scheduleHelp.style.color = "var(--danger)";
    }
  }

  async saveJob() {
    const name = this.els.fieldName.value.trim();
    const schedule = this.els.fieldSchedule.value.trim();
    const modeType = this.els.fieldMode.value;
    if (!name || !schedule) {
      this.notify("Name and schedule are required", "error");
      return;
    }
    let mode;
    if (modeType === "agent") {
      const prompt = this.els.fieldPrompt.value.trim();
      if (!prompt) {
        this.notify("Prompt is required for agent mode", "error");
        return;
      }
      mode = { type: "agent", prompt };
    } else {
      const pluginId = this.els.fieldPluginId.value.trim();
      const toolName = this.els.fieldToolName.value.trim();
      const argsText = this.els.fieldArgs.value.trim() || "{}";
      let args;
      try {
        args = JSON.parse(argsText);
      } catch {
        this.notify("Args must be valid JSON", "error");
        return;
      }
      if (!pluginId || !toolName) {
        this.notify("Plugin ID and tool name are required", "error");
        return;
      }
      mode = { type: "tool", pluginId, toolName, args };
    }
    const repeatRaw = this.els.fieldRepeat.value.trim();
    const payload = { name, schedule, mode };
    if (repeatRaw) payload.repeatTimes = parseInt(repeatRaw, 10);
    try {
      await sendRequest("job.add", payload);
      this.notify("Job created", "success");
      this.closeModal();
      await this.refresh();
    } catch (error) {
      this.notify(`Could not create job: ${error.message || error}`, "error");
    }
  }

  async runJob(id) {
    try {
      const result = await sendRequest("job.run", { id });
      if (!result.ok) this.notify(`Run failed: ${result.error ?? "unknown"}`, "error");
      else this.notify("Job started", "success");
      await this.refresh();
    } catch (error) {
      this.notify(`Could not run job: ${error.message || error}`, "error");
    }
  }

  async toggleJob(id, enabled) {
    try {
      await sendRequest("job.set-enabled", { id, enabled });
      await this.refresh();
    } catch (error) {
      this.notify(`Could not update job: ${error.message || error}`, "error");
    }
  }

  openDeleteDialog(id, name) {
    this.pendingDeleteId = id;
    this.els.deleteCopy.textContent = `“${name}” will be permanently removed from this device.`;
    this.els.deleteOverlay.hidden = false;
    this.els.deleteDialog.hidden = false;
    this.els.deleteCancel.focus();
  }

  closeDeleteDialog() {
    this.pendingDeleteId = "";
    this.els.deleteOverlay.hidden = true;
    this.els.deleteDialog.hidden = true;
  }

  async confirmDelete() {
    if (!this.pendingDeleteId) return;
    const id = this.pendingDeleteId;
    this.closeDeleteDialog();
    try {
      await sendRequest("job.remove", { id });
      await this.refresh();
    } catch (error) {
      this.notify(`Could not remove job: ${error.message || error}`, "error");
    }
  }

  async showOutput(id, name) {
    try {
      const result = await sendRequest("job.output", { id, limit: 20 });
      this._lastFocus = document.activeElement;
      this.els.outputTitle.textContent = `Output: ${name}`;
      this.els.outputBody.textContent = "";
      const entries = result.outputs ?? [];
      if (entries.length === 0) {
        const empty = document.createElement("div");
        empty.className = "jobs-empty";
        empty.innerHTML = "<strong>No output yet</strong><span>This job has not recorded any run summaries.</span>";
        this.els.outputBody.appendChild(empty);
      } else {
        for (const entry of entries) {
          const card = document.createElement("div");
          card.className = "job-output-entry";
          const header = document.createElement("div");
          header.className = "job-output-header";
          header.textContent = `${entry.runAt} · ${entry.status}`;
          if (entry.status === "error") header.classList.add("job-output-error");
          const summary = document.createElement("pre");
          summary.className = "job-output-summary";
          summary.textContent = entry.summary;
          card.append(header, summary);
          this.els.outputBody.appendChild(card);
        }
      }
      this.els.outputModal.classList.add("active");
      this.els.outputClose.focus();
    } catch (error) {
      this.notify(`Could not load output: ${error.message || error}`, "error");
    }
  }

  closeOutput() {
    this.els.outputModal.classList.remove("active");
    this._restoreFocus();
  }

  _restoreFocus() {
    if (this._lastFocus && typeof this._lastFocus.focus === "function") {
      this._lastFocus.focus();
      this._lastFocus = null;
    }
  }
}
