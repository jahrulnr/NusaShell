import { sendRequest, onEvent } from "./ws-client.js";
import { fetchPlugins, listTools, startPlugin } from "./plugin-api.js";
import { renderJobOutputMarkdown } from "./agent-conversation-ui.js";
import { serializeSchemaArgs } from "./jobs-form-helpers.js";

const JOB_RUN_TIMEOUT_MS = 300_000;

function describeJobTrigger(trigger) {
  if (!trigger) return "—";
  if (trigger.kind === "schedule") return describeJobSchedule(trigger.schedule);
  if (trigger.kind === "event") {
    const plugin = trigger.pluginId ? ` @${trigger.pluginId}` : "";
    return `⚡ event ${trigger.pattern}${plugin}`;
  }
  return "—";
}

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
  if (mode.type === "agent") {
    const model = mode.model ? ` · ${mode.model}` : "";
    return `agent${model}: ${mode.prompt.slice(0, 60)}`;
  }
  if (mode.type === "tool") return `tool: ${mode.pluginId}/${mode.toolName}`;
  return "—";
}

function describeScheduleKey(schedule) {
  if (!schedule) return "";
  if (schedule.kind === "once") return schedule.runAt;
  if (schedule.kind === "interval") {
    const m = schedule.minutes;
    if (m % 1440 === 0) return `every ${m / 1440}d`;
    if (m % 60 === 0) return `every ${m / 60}h`;
    return `every ${m}m`;
  }
  if (schedule.kind === "cron") return schedule.expr;
  return "";
}

function describeTriggerKey(trigger) {
  if (!trigger) return "";
  if (trigger.kind === "schedule") return describeScheduleKey(trigger.schedule);
  if (trigger.kind === "event") return `event:${trigger.pattern}`;
  return "";
}

function describeLastStatus(status) {
  if (status === "ok") return "OK";
  if (status === "error") return "Error";
  if (status === "cancelled") return "Cancelled";
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
    this.editingId = null;
    this._lastFocus = null;
    this._runningIds = new Set();
    this._plugins = [];
    this._toolSchemas = new Map();
    this._aiSettings = null;
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
      fieldTrigger: document.getElementById("job-field-trigger"),
      scheduleFields: document.getElementById("job-schedule-fields"),
      eventFields: document.getElementById("job-event-fields"),
      fieldSchedule: document.getElementById("job-field-schedule"),
      scheduleHelp: document.getElementById("job-schedule-help"),
      fieldEventPattern: document.getElementById("job-field-event-pattern"),
      eventHelp: document.getElementById("job-event-help"),
      fieldEventPlugin: document.getElementById("job-field-event-plugin"),
      fieldThrottleMs: document.getElementById("job-field-throttle-ms"),
      fieldMaxFires: document.getElementById("job-field-max-fires"),
      fieldMode: document.getElementById("job-field-mode"),
      modeHelp: document.getElementById("job-mode-help"),
      agentFields: document.getElementById("job-agent-fields"),
      promptLabel: document.getElementById("job-agent-prompt-label"),
      fieldPrompt: document.getElementById("job-field-prompt"),
      fieldProvider: document.getElementById("job-field-provider"),
      fieldModel: document.getElementById("job-field-model"),
      fieldEffort: document.getElementById("job-field-effort"),
      effortLabel: document.getElementById("job-effort-label"),
      toolFields: document.getElementById("job-tool-fields"),
      fieldPluginId: document.getElementById("job-field-plugin-id"),
      fieldToolName: document.getElementById("job-field-tool-name"),
      toolHelp: document.getElementById("job-tool-help"),
      schemaForm: document.getElementById("job-tool-schema-form"),
      argsFallbackLabel: document.getElementById("job-tool-args-fallback-label"),
      fieldArgs: document.getElementById("job-field-args"),
      fieldRepeat: document.getElementById("job-field-repeat"),
      fieldOnCompleteType: document.getElementById("job-field-oncomplete-type"),
      fieldOnCompletePayload: document.getElementById("job-field-oncomplete-payload"),
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
    onEvent("job.started", (payload) => {
      this._runningIds.add(payload.jobId);
      if (this._isViewActive()) this._patchRowRunning(payload.jobId, true);
    });
    onEvent("job.cancelled", (payload) => {
      this.notify(`Job “${payload.name}” cancelled`, "info");
      this._runningIds.delete(payload.jobId);
      if (this._isViewActive()) void this.refresh();
    });
    onEvent("job.completed", (payload) => {
      this.notify(`Job “${payload.name}” completed`, "success");
      this._runningIds.delete(payload.jobId);
      if (this._isViewActive()) void this.refresh();
    });
    onEvent("job.failed", (payload) => {
      this.notify(`Job “${payload.name}” failed: ${payload.error}`, "error");
      this._runningIds.delete(payload.jobId);
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
    this.els.fieldTrigger?.addEventListener("change", () => this._toggleTriggerFields());
    this.els.fieldSchedule?.addEventListener("blur", () => void this.validateSchedule());
    this.els.fieldEventPlugin?.addEventListener("change", () => void this._populateEventPatternHints());
    this.els.fieldProvider?.addEventListener("change", () => this._syncModelOptions());
    this.els.fieldPluginId?.addEventListener("change", () => void this._onPluginChange());
    this.els.fieldToolName?.addEventListener("change", () => this._onToolChange());
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

  _isJobRunning(jobId) {
    return this._runningIds.has(jobId);
  }

  _patchRowRunning(jobId, running) {
    const row = this.els.list?.querySelector(`[data-job-id="${jobId}"]`);
    if (!row) return;
    row.classList.toggle("job-row-running", running);
    const stopBtn = row.querySelector('[data-control="job-stop-btn"]');
    const runBtn = row.querySelector('[data-control="job-run-btn"]');
    const toggleBtn = row.querySelector('[data-control="job-toggle-btn"]');
    if (running) {
      if (stopBtn) stopBtn.hidden = false;
      if (runBtn) runBtn.disabled = true;
      if (toggleBtn) toggleBtn.disabled = true;
    } else {
      if (stopBtn) stopBtn.hidden = true;
      if (runBtn) runBtn.disabled = false;
      if (toggleBtn) toggleBtn.disabled = false;
    }
  }

  _renderRow(job) {
    const row = document.createElement("div");
    row.className = "job-row";
    row.dataset.jobId = job.id;
    row.setAttribute("role", "listitem");

    const running = this._isJobRunning(job.id);
    if (running) row.classList.add("job-row-running");

    const status = job.lastStatus ?? "idle";
    const statusDot = document.createElement("span");
    statusDot.className = `job-status-dot job-status-${status}`;
    if (running) statusDot.classList.add("job-status-running");
    statusDot.setAttribute("aria-hidden", "true");

    const info = document.createElement("div");
    info.className = "job-info";
    const title = document.createElement("div");
    title.className = "job-title";
    title.textContent = job.name;
    const meta = document.createElement("div");
    meta.className = "job-meta";
    const repeat = job.repeat?.times ? ` · ${job.repeat.completed}/${job.repeat.times}` : "";
    meta.textContent = `${describeJobTrigger(job.trigger)} · ${describeJobMode(job.mode)}${repeat}`;
    info.append(title, meta);

    const strip = document.createElement("div");
    strip.className = "job-strip";
    const next = document.createElement("div");
    next.className = "job-strip-next";
    if (running) {
      next.textContent = "running…";
    } else if (job.enabled && job.nextRunAt) {
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
    runBtn.disabled = running;
    runBtn.addEventListener("click", () => void this.runJob(job.id));

    const stopBtn = document.createElement("button");
    stopBtn.type = "button";
    stopBtn.className = "mini-btn danger";
    stopBtn.textContent = "Stop";
    stopBtn.dataset.control = "job-stop-btn";
    stopBtn.setAttribute("aria-label", `Stop ${job.name}`);
    stopBtn.hidden = !running;
    stopBtn.addEventListener("click", () => void this.cancelJob(job.id, job.name));

    const toggleBtn = document.createElement("button");
    toggleBtn.type = "button";
    toggleBtn.className = "mini-btn";
    toggleBtn.textContent = job.enabled ? "Pause" : "Resume";
    toggleBtn.dataset.control = "job-toggle-btn";
    toggleBtn.setAttribute("aria-label", `${job.enabled ? "Pause" : "Resume"} ${job.name}`);
    toggleBtn.disabled = running;
    toggleBtn.addEventListener("click", () => void this.toggleJob(job.id, !job.enabled));

    const outputBtn = document.createElement("button");
    outputBtn.type = "button";
    outputBtn.className = "mini-btn";
    outputBtn.textContent = "Output";
    outputBtn.dataset.control = "job-output-btn";
    outputBtn.setAttribute("aria-label", `View output for ${job.name}`);
    outputBtn.addEventListener("click", () => void this.showOutput(job.id, job.name));

    const editBtn = document.createElement("button");
    editBtn.type = "button";
    editBtn.className = "mini-btn";
    editBtn.textContent = "Edit";
    editBtn.dataset.control = "job-edit-btn";
    editBtn.setAttribute("aria-label", `Edit ${job.name}`);
    editBtn.addEventListener("click", () => void this.openModal(job));

    const removeBtn = document.createElement("button");
    removeBtn.type = "button";
    removeBtn.className = "mini-btn danger";
    removeBtn.textContent = "Remove";
    removeBtn.dataset.control = "job-remove-btn";
    removeBtn.setAttribute("aria-label", `Remove ${job.name}`);
    removeBtn.addEventListener("click", () => this.openDeleteDialog(job.id, job.name));

    actions.append(runBtn, stopBtn, toggleBtn, editBtn, outputBtn, removeBtn);
    row.append(statusDot, info, strip, actions);
    return row;
  }

  async openModal(job = null) {
    this._lastFocus = document.activeElement;
    this.editingId = job?.id ?? null;
    this.els.modalTitle.textContent = job ? "Edit job" : "New job";
    this.els.fieldName.value = job?.name ?? "";
    const triggerKind = job?.trigger?.kind ?? "schedule";
    this.els.fieldTrigger.value = triggerKind;
    this.els.fieldSchedule.value = triggerKind === "schedule" && job ? describeTriggerKey(job.trigger) : "";
    this.els.fieldEventPattern.value = triggerKind === "event" && job ? job.trigger.pattern : "";
    this.els.fieldEventPlugin.value = triggerKind === "event" && job?.trigger?.pluginId ? job.trigger.pluginId : "";
    this.els.fieldThrottleMs.value = triggerKind === "event" && job?.trigger?.throttleMs ? String(job.trigger.throttleMs) : "";
    this.els.fieldMaxFires.value = triggerKind === "event" && job?.trigger?.maxFiresPerHour ? String(job.trigger.maxFiresPerHour) : "";
    this.els.fieldMode.value = job?.mode?.type ?? "agent";
    this.els.fieldPrompt.value = job?.mode?.type === "agent" ? job.mode.prompt : "";
    this.els.fieldRepeat.value = job?.repeat?.times ?? "";
    this.els.fieldOnCompleteType.value = job?.onComplete?.type ?? "";
    this.els.fieldOnCompletePayload.value = job?.onComplete?.payload ? JSON.stringify(job.onComplete.payload, null, 2) : "";
    this.els.scheduleHelp.textContent = "";
    this.els.fieldArgs.value = "{}";
    this.els.schemaForm.textContent = "";
    this.els.argsFallbackLabel.hidden = true;

    await this._loadModelOptions();
    if (job?.mode?.type === "agent") {
      this.els.fieldProvider.value = job.mode.providerId ?? "";
      this._syncModelOptions();
      this.els.fieldModel.value = job.mode.model ?? "";
      this.els.fieldEffort.value = job.mode.effort ?? "";
    } else {
      this.els.fieldProvider.value = "";
      this.els.fieldModel.value = "";
      this.els.fieldEffort.value = "";
    }

    await this._loadPluginOptions();
    if (job?.mode?.type === "tool") {
      this.els.fieldPluginId.value = job.mode.pluginId;
      await this._onPluginChange(job.mode.args, job.mode.toolName);
    } else {
      this.els.fieldPluginId.value = "";
      this.els.fieldToolName.value = "";
      this._clearToolOptions();
    }

    this._toggleModeFields();
    await this._loadEventPluginOptions();
    this._toggleTriggerFields();
    this.els.modal.classList.add("active");
    this.els.fieldName.focus();
  }

  closeModal() {
    this.els.modal.classList.remove("active");
    this.editingId = null;
    this._restoreFocus();
  }

  _toggleModeFields() {
    const mode = this.els.fieldMode.value;
    const isAgent = mode === "agent";
    this.els.agentFields.hidden = !isAgent;
    this.els.agentFields.setAttribute("aria-hidden", String(!isAgent));
    this.els.toolFields.hidden = isAgent;
    this.els.toolFields.setAttribute("aria-hidden", String(isAgent));
    this.els.modeHelp.textContent = isAgent
      ? "Uses an AI model to run a headless agent turn. Costs tokens."
      : "Calls one plugin tool with fixed args — no AI model, no tokens.";
  }

  _toggleTriggerFields() {
    const kind = this.els.fieldTrigger?.value ?? "schedule";
    const isEvent = kind === "event";
    if (this.els.scheduleFields) {
      this.els.scheduleFields.hidden = isEvent;
      this.els.scheduleFields.setAttribute("aria-hidden", String(isEvent));
    }
    if (this.els.eventFields) {
      this.els.eventFields.hidden = !isEvent;
      this.els.eventFields.setAttribute("aria-hidden", String(!isEvent));
    }
    if (isEvent) void this._populateEventPatternHints();
  }

  async _loadEventPluginOptions() {
    if (!this.els.fieldEventPlugin) return;
    const select = this.els.fieldEventPlugin;
    const currentValue = select.value;
    while (select.options.length > 1) select.remove(1);
    // Reuse the plugin list already loaded by _loadPluginOptions
    const plugins = this._plugins ?? [];
    for (const plugin of plugins) {
      const opt = document.createElement("option");
      opt.value = plugin.pluginId;
      opt.textContent = plugin.pluginId;
      select.appendChild(opt);
    }
    if (currentValue) select.value = currentValue;
  }

  async _populateEventPatternHints() {
    const pluginId = this.els.fieldEventPlugin?.value || "";
    if (!this.els.eventHelp) return;
    if (!pluginId) {
      this.els.eventHelp.textContent = "Glob pattern matched against event types from plugin manifests.";
      return;
    }
    // Look up the manifest from the loaded plugin list
    const plugin = this._plugins?.find((p) => p.pluginId === pluginId);
    const emits = plugin?.manifest?.automation?.emits;
    if (emits && emits.length > 0) {
      const types = emits.map((e) => e.type).join(", ");
      this.els.eventHelp.textContent = `Available events: ${types}`;
    } else {
      this.els.eventHelp.textContent = "This plugin declares no automation events.";
    }
  }

  async _loadModelOptions() {
    if (!window.shell?.aiProviders?.list) return;
    try {
      this._aiSettings = await window.shell.aiProviders.list();
    } catch { return; }
    const providerSelect = this.els.fieldProvider;
    const current = providerSelect.value;
    providerSelect.textContent = "";
    const def = document.createElement("option");
    def.value = "";
    def.textContent = this._aiSettings.activeProviderId
      ? `Default (${this._aiSettings.activeProviderId})`
      : "Default";
    providerSelect.appendChild(def);
    for (const provider of this._aiSettings.providers ?? []) {
      const opt = document.createElement("option");
      opt.value = provider.id;
      opt.textContent = provider.id;
      providerSelect.appendChild(opt);
    }
    providerSelect.value = current;
    this._syncModelOptions();
  }

  _syncModelOptions() {
    const providerId = this.els.fieldProvider.value;
    const modelSelect = this.els.fieldModel;
    const current = modelSelect.value;
    modelSelect.textContent = "";
    const def = document.createElement("option");
    const activeModel = this._aiSettings?.models?.find((m) => m.key === this._aiSettings?.activeModelKey);
    def.value = "";
    def.textContent = activeModel ? `Default (${activeModel.id})` : "Default";
    modelSelect.appendChild(def);
    const models = (this._aiSettings?.models ?? []).filter((m) => !providerId || m.providerId === providerId);
    for (const model of models) {
      const opt = document.createElement("option");
      opt.value = model.id;
      opt.textContent = model.label || model.id;
      modelSelect.appendChild(opt);
    }
    modelSelect.value = current;
    this._syncEffortOptions();
  }

  _syncEffortOptions() {
    const modelId = this.els.fieldModel.value;
    const effortSelect = this.els.fieldEffort;
    const current = effortSelect.value;
    const model = (this._aiSettings?.models ?? []).find((m) => m.id === modelId);
    const efforts = model?.supportedEfforts ?? [];
    effortSelect.textContent = "";
    const def = document.createElement("option");
    def.value = "";
    def.textContent = "Default";
    effortSelect.appendChild(def);
    if (efforts.length > 0) {
      this.els.effortLabel.hidden = false;
      for (const eff of efforts) {
        const opt = document.createElement("option");
        opt.value = eff;
        opt.textContent = eff;
        effortSelect.appendChild(opt);
      }
    } else {
      this.els.effortLabel.hidden = true;
    }
    effortSelect.value = current;
  }

  async _loadPluginOptions() {
    this._plugins = await fetchPlugins();
    const select = this.els.fieldPluginId;
    select.textContent = "";
    const placeholder = document.createElement("option");
    placeholder.value = "";
    placeholder.textContent = "Select a plugin…";
    select.appendChild(placeholder);
    for (const plugin of this._plugins) {
      const opt = document.createElement("option");
      opt.value = plugin.pluginId;
      opt.textContent = `${plugin.pluginId}${plugin.state === "running" ? "" : " (stopped)"}`;
      select.appendChild(opt);
    }
  }

  _clearToolOptions() {
    const select = this.els.fieldToolName;
    select.textContent = "";
    const placeholder = document.createElement("option");
    placeholder.value = "";
    placeholder.textContent = "Select a tool…";
    select.appendChild(placeholder);
    this.els.schemaForm.textContent = "";
    this.els.toolHelp.textContent = "";
  }

  async _onPluginChange(prefillArgs, prefillTool) {
    const pluginId = this.els.fieldPluginId.value;
    this._clearToolOptions();
    if (!pluginId) return;
    const plugin = this._plugins.find((p) => p.pluginId === pluginId);
    if (plugin && plugin.state !== "running") {
      this.els.toolHelp.textContent = "Starting plugin…";
      await startPlugin(pluginId);
    }
    let result;
    try {
      result = await listTools(pluginId);
    } catch (e) {
      this.els.toolHelp.textContent = `Could not list tools: ${e?.message || e}`;
      return;
    }
    if (result.error) {
      this.els.toolHelp.textContent = result.error.message || "tool.list failed";
      return;
    }
    this.els.toolHelp.textContent = "";
    const tools = result.tools ?? [];
    for (const tool of tools) {
      const opt = document.createElement("option");
      opt.value = tool.name;
      opt.textContent = tool.name;
      this.els.fieldToolName.appendChild(opt);
    }
    if (prefillTool) this.els.fieldToolName.value = prefillTool;
    this._onToolChange(prefillArgs);
  }

  _onToolChange(prefillArgs) {
    const pluginId = this.els.fieldPluginId.value;
    const toolName = this.els.fieldToolName.value;
    this.els.schemaForm.textContent = "";
    if (!pluginId || !toolName) return;
    const schema = this._toolSchemas.get(`${pluginId}/${toolName}`);
    if (schema) {
      this._renderSchemaForm(schema, prefillArgs);
    } else {
      this.els.argsFallbackLabel.hidden = false;
      this.els.fieldArgs.value = prefillArgs ? JSON.stringify(prefillArgs, null, 2) : "{}";
    }
  }

  _renderSchemaForm(schema, prefillArgs) {
    const form = this.els.schemaForm;
    form.textContent = "";
    const props = schema.properties ?? {};
    const required = new Set(schema.required ?? []);
    const prefill = prefillArgs ?? {};
    const hasComplex = Object.entries(props).some(([, def]) => {
      const t = def?.type;
      return t === "object" || t === "array";
    });

    for (const [key, def] of Object.entries(props)) {
      const type = def?.type ?? "string";
      const isRequired = required.has(key);
      if (type === "object" || type === "array") {
        const label = document.createElement("label");
        label.className = "form-label";
        label.textContent = `${key}${isRequired ? " *" : ""} (${type}, JSON)`;
        const ta = document.createElement("textarea");
        ta.rows = 3;
        ta.dataset.schemaKey = key;
        ta.dataset.schemaType = "json";
        ta.placeholder = JSON.stringify(def?.default ?? (type === "array" ? [] : {}), null, 2);
        ta.value = key in prefill ? JSON.stringify(prefill[key], null, 2) : "";
        label.appendChild(ta);
        form.appendChild(label);
      } else if (def?.enum) {
        const label = document.createElement("label");
        label.className = "form-label";
        label.textContent = `${key}${isRequired ? " *" : ""}`;
        const sel = document.createElement("select");
        sel.dataset.schemaKey = key;
        sel.dataset.schemaType = "enum";
        if (!isRequired) {
          const empty = document.createElement("option");
          empty.value = "";
          empty.textContent = "—";
          sel.appendChild(empty);
        }
        for (const val of def.enum) {
          const opt = document.createElement("option");
          opt.value = val;
          opt.textContent = val;
          sel.appendChild(opt);
        }
        sel.value = key in prefill ? String(prefill[key]) : (def.default ?? "");
        label.appendChild(sel);
        form.appendChild(label);
      } else {
        const label = document.createElement("label");
        label.className = "form-label";
        label.textContent = `${key}${isRequired ? " *" : ""}`;
        const input = document.createElement("input");
        input.type = type === "number" ? "number" : type === "boolean" ? "checkbox" : "text";
        input.dataset.schemaKey = key;
        input.dataset.schemaType = type;
        if (type === "boolean") {
          input.checked = key in prefill ? Boolean(prefill[key]) : Boolean(def.default);
        } else {
          input.value = key in prefill ? String(prefill[key]) : (def.default ?? "");
        }
        if (def.description) input.title = def.description;
        label.appendChild(input);
        form.appendChild(label);
      }
    }

    if (hasComplex) {
      this.els.argsFallbackLabel.hidden = true;
    } else {
      this.els.argsFallbackLabel.hidden = true;
    }
    if (Object.keys(props).length === 0) {
      this.els.toolHelp.textContent = "This tool takes no arguments.";
    } else {
      this.els.toolHelp.textContent = "";
    }
  }

  _serializeSchemaForm() {
    const inputs = this.els.schemaForm.querySelectorAll("[data-schema-key]");
    const fields = [];
    for (const input of inputs) {
      const type = input.dataset.schemaType;
      fields.push({
        key: input.dataset.schemaKey,
        type,
        value: type === "boolean" ? input.checked : input.value,
      });
    }
    return serializeSchemaArgs(fields);
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
    const triggerKind = this.els.fieldTrigger?.value ?? "schedule";
    const schedule = this.els.fieldSchedule.value.trim();
    const eventPattern = this.els.fieldEventPattern?.value.trim() ?? "";
    const modeType = this.els.fieldMode.value;
    if (!name) {
      this.notify("Name is required", "error");
      return;
    }
    let trigger;
    if (triggerKind === "event") {
      if (!eventPattern) {
        this.notify("Event pattern is required for event trigger", "error");
        return;
      }
      trigger = { kind: "event", pattern: eventPattern };
      const eventPlugin = this.els.fieldEventPlugin?.value.trim();
      if (eventPlugin) trigger.pluginId = eventPlugin;
      const throttleMs = parseInt(this.els.fieldThrottleMs?.value ?? "", 10);
      if (!isNaN(throttleMs) && throttleMs > 0) trigger.throttleMs = throttleMs;
      const maxFires = parseInt(this.els.fieldMaxFires?.value ?? "", 10);
      if (!isNaN(maxFires) && maxFires > 0) trigger.maxFiresPerHour = maxFires;
    } else {
      if (!schedule) {
        this.notify("Schedule is required", "error");
        return;
      }
      trigger = { kind: "schedule", schedule };
    }
    let mode;
    if (modeType === "agent") {
      const prompt = this.els.fieldPrompt.value.trim();
      if (!prompt) {
        this.notify("Prompt is required for agent mode", "error");
        return;
      }
      mode = { type: "agent", prompt };
      const providerId = this.els.fieldProvider.value;
      const model = this.els.fieldModel.value;
      const effort = this.els.fieldEffort.value;
      if (providerId) mode.providerId = providerId;
      if (model) mode.model = model;
      if (effort) mode.effort = effort;
    } else {
      const pluginId = this.els.fieldPluginId.value.trim();
      const toolName = this.els.fieldToolName.value.trim();
      if (!pluginId || !toolName) {
        this.notify("Plugin and tool are required", "error");
        return;
      }
      let args;
      if (this.els.schemaForm.children.length > 0) {
        try {
          args = this._serializeSchemaForm();
        } catch (e) {
          this.notify(`Invalid args: ${e.message || e}`, "error");
          return;
        }
      } else {
        const argsText = this.els.fieldArgs.value.trim() || "{}";
        try {
          args = JSON.parse(argsText);
        } catch {
          this.notify("Args must be valid JSON", "error");
          return;
        }
      }
      mode = { type: "tool", pluginId, toolName, args };
    }
    const repeatRaw = this.els.fieldRepeat.value.trim();
    const payload = { name, trigger, mode };
    if (repeatRaw) payload.repeatTimes = parseInt(repeatRaw, 10);
    const onCompleteType = this.els.fieldOnCompleteType?.value.trim() ?? "";
    if (onCompleteType) {
      const onComplete = { type: onCompleteType };
      const onCompletePayloadText = this.els.fieldOnCompletePayload?.value.trim() ?? "";
      if (onCompletePayloadText) {
        try {
          onComplete.payload = JSON.parse(onCompletePayloadText);
        } catch {
          this.notify("On-complete payload must be valid JSON", "error");
          return;
        }
      }
      payload.onComplete = onComplete;
    }
    try {
      if (this.editingId) {
        await sendRequest("job.update", { id: this.editingId, ...payload });
        this.notify("Job updated", "success");
      } else {
        await sendRequest("job.add", payload);
        this.notify("Job created", "success");
      }
      this.closeModal();
      await this.refresh();
    } catch (error) {
      this.notify(`Could not save job: ${error.message || error}`, "error");
    }
  }

  async runJob(id) {
    try {
      const result = await sendRequest("job.run", { id }, JOB_RUN_TIMEOUT_MS);
      if (!result.ok) this.notify(`Run failed: ${result.error ?? "unknown"}`, "error");
      else this.notify("Job started", "success");
      await this.refresh();
    } catch (error) {
      this.notify(`Could not run job: ${error.message || error}`, "error");
    }
  }

  async cancelJob(id, name) {
    try {
      const result = await sendRequest("job.cancel", { id });
      if (!result.ok) this.notify(`Could not stop: ${result.error ?? "unknown"}`, "error");
      else this.notify(`Stopping “${name}”…`, "info");
    } catch (error) {
      this.notify(`Could not stop job: ${error.message || error}`, "error");
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
      const result = await sendRequest("job.output", { id, limit: 20, includeBody: false });
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
          this.els.outputBody.appendChild(this._renderOutputEntry(id, entry));
        }
      }
      this.els.outputModal.classList.add("active");
      this.els.outputClose.focus();
    } catch (error) {
      this.notify(`Could not load output: ${error.message || error}`, "error");
    }
  }

  _renderOutputEntry(jobId, entry) {
    const card = document.createElement("div");
    card.className = "job-output-entry";
    const header = document.createElement("div");
    header.className = "job-output-header";
    header.textContent = `${entry.runAt} · ${entry.status}`;
    if (entry.status === "error") header.classList.add("job-output-error");
    if (entry.status === "cancelled") header.classList.add("job-output-cancelled");
    const summary = document.createElement("div");
    summary.className = "job-output-md";
    summary.innerHTML = renderJobOutputMarkdown(entry.summary);
    card.append(header, summary);

    const expandBtn = document.createElement("button");
    expandBtn.type = "button";
    expandBtn.className = "mini-btn job-output-expand";
    expandBtn.textContent = "Show full output";
    expandBtn.dataset.control = "job-output-expand";
    let expanded = false;
    expandBtn.addEventListener("click", async () => {
      if (expanded) return;
      expanded = true;
      expandBtn.disabled = true;
      expandBtn.textContent = "Loading…";
      try {
        const full = await sendRequest("job.output", { id: jobId, limit: 20, includeBody: true });
        const items = full.outputs ?? [];
        const match = items.find((item) => item.runAt === entry.runAt && item.path === entry.path);
        if (match?.body) {
          const body = document.createElement("div");
          body.className = "job-output-md job-output-full";
          body.innerHTML = renderJobOutputMarkdown(match.body);
          card.appendChild(body);
          expandBtn.textContent = "Shown";
        } else {
          expandBtn.textContent = "No full output";
        }
      } catch (error) {
        expandBtn.textContent = "Failed to load";
        expandBtn.disabled = false;
        expanded = false;
      }
    });
    card.appendChild(expandBtn);
    return card;
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
