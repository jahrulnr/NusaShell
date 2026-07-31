// NusaShell launcher renderer — connects to backend via WebSocket,
// renders plugin grid, and handles plugin lifecycle actions.
// Uses the native browser WebSocket (not the `ws` npm package).
import { clampModelEffort, formatTokenCount, modelCompatibility, searchModels } from "./ai-model-ui.js";
import { AgentConversationController } from "./agent-conversation-controller.js";
import { SkillsController } from "./skills-controller.js";
import { LearningController } from "./learning-controller.js";
import {
  applyTextEdit,
  countLogsBySource,
  filterLauncherPlugins,
  normalizeTransparentIcon,
  pluginIconPresentation,
  positionContextMenu,
  providerApiModes,
} from "./launcher-ui.js";

const WS_URL = window.shell?.wsUrl ?? "ws://127.0.0.1:9130";
const PROTOCOL_VERSION = "1.0";

// ============ Lightweight WS client (browser-native) ============

const pendingRequests = new Map();
const eventHandlers = new Map();
let ws = null;
let connected = false;
let reconnectTimer = null;
let activeSubscriptions = new Set();

function connectWs() {
  ws = new WebSocket(WS_URL);

  ws.onopen = () => {
    connected = true;
    updateConnStatus(true);
    writeRendererLog("info", `WebSocket connected to ${WS_URL}`);
    if (activeSubscriptions.size > 0) {
      sendRequest("subscribe", { eventTypes: [...activeSubscriptions] }).catch(() => {});
    }
    refreshAll();
  };

  ws.onclose = () => {
    connected = false;
    updateConnStatus(false);
    writeRendererLog("warn", "WebSocket connection closed; reconnecting");
    for (const [, ctrl] of pendingRequests) ctrl.reject(new Error("Connection closed"));
    pendingRequests.clear();
    scheduleReconnect();
  };

  ws.onerror = () => writeRendererLog("error", "WebSocket connection error");

  ws.onmessage = (event) => {
    let msg;
    try { msg = JSON.parse(event.data); } catch { return; }
    if (!msg || typeof msg !== "object") return;

    if (msg.kind === "response") {
      const ctrl = pendingRequests.get(msg.id);
      if (ctrl) {
        pendingRequests.delete(msg.id);
        if (msg.ok) {
          writeRendererLog("debug", `WebSocket response ${msg.id}`);
          ctrl.resolve(msg.result);
        } else {
          writeRendererLog("warn", `WebSocket request failed: ${msg.error?.message ?? "Unknown error"}`);
          const error = new Error(msg.error?.message ?? "Unknown error");
          error.code = msg.error?.code;
          error.details = msg.error?.details;
          ctrl.reject(error);
        }
      }
    } else if (msg.kind === "event") {
      dispatchEvent(msg.event, msg.payload, msg.sequence);
    }
  };
}

function scheduleReconnect() {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectWs();
  }, 1000);
}

function sendRequest(method, payload, timeoutMs = 10000) {
  return new Promise((resolve, reject) => {
    if (!connected || !ws || ws.readyState !== WebSocket.OPEN) {
      reject(new Error("Not connected"));
      return;
    }
    const id = `req_${crypto.randomUUID()}`;
    const ctrl = { resolve, reject };
    pendingRequests.set(id, ctrl);

    const timer = setTimeout(() => {
      pendingRequests.delete(id);
      ctrl.reject(new Error("Request timeout"));
    }, timeoutMs);

    const origResolve = ctrl.resolve;
    const origReject = ctrl.reject;
    ctrl.resolve = (v) => { clearTimeout(timer); origResolve(v); };
    ctrl.reject = (e) => { clearTimeout(timer); origReject(e); };

    writeRendererLog("debug", `WebSocket request ${method} (${id})`);
    ws.send(JSON.stringify({ kind: "request", id, method, protocolVersion: PROTOCOL_VERSION, payload }));
  });
}

function dispatchEvent(eventType, payload, sequence) {
  writeRendererLog("debug", `Backend event ${eventType} (#${sequence})`);
  const handlers = eventHandlers.get(eventType);
  if (handlers) handlers.forEach(h => h(payload, sequence));
  const allHandlers = eventHandlers.get("*");
  if (allHandlers) allHandlers.forEach(h => h({ event: eventType, payload, sequence }));
}

function onEvent(eventType, handler) {
  if (!eventHandlers.has(eventType)) eventHandlers.set(eventType, new Set());
  eventHandlers.get(eventType).add(handler);
  return () => eventHandlers.get(eventType)?.delete(handler);
}

async function subscribe(eventTypes) {
  const types = eventTypes ?? ["*"];
  const result = await sendRequest("subscribe", { eventTypes: types });
  for (const t of result.subscribed) activeSubscriptions.add(t);
}

// ============ State ============

let plugins = [];
let currentPlugin = null;
let logSourceFilter = "all";
const logEntries = [];
const STATES = ["idle", "starting", "running", "stopping", "crashed"];
let agentConversationController = null;
let skillsController = null;
let learningController = null;
let launcherSearchQuery = "";
let aiSettings = { activeProviderId: "", activeModelKey: "", effort: "auto", providers: [], models: [] };
let currentProviderDetailId = "";
let pendingProviderDeleteId = "";
let editContextTarget = null;

// ============ Helpers ============

function $(sel) { return document.querySelector(sel); }
function $$(sel) { return document.querySelectorAll(sel); }
function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html != null) e.innerHTML = html;
  return e;
}
function nowTime() { return new Date().toLocaleTimeString("en-US", { hour12: false }); }

function writeRendererLog(level, message) {
  window.shell?.logs?.write(level, message);
}

function addLogEntry(entry) {
  if (logEntries.some((item) => item.id === entry.id)) return;
  logEntries.push(entry);
  logEntries.sort((a, b) => a.id - b.id);
  if (logEntries.length > 1000) logEntries.splice(0, logEntries.length - 1000);
  renderLogTail();
}

function renderLogTail() {
  const tail = $("#log-tail");
  const count = $("#log-count");
  if (!tail || !count) return;

  const stickToBottom = tail.scrollTop + tail.clientHeight >= tail.scrollHeight - 24;
  const filtered = logSourceFilter === "all"
    ? logEntries
    : logEntries.filter((entry) => entry.source === logSourceFilter);
  const sourceCounts = countLogsBySource(logEntries);

  count.textContent = `${logEntries.length} / 1000`;
  $$("[data-log-source]").forEach((chip) => {
    const badge = chip.querySelector(".chip-count");
    if (badge) badge.textContent = String(sourceCounts[chip.dataset.logSource] ?? 0);
  });
  tail.textContent = "";
  if (filtered.length === 0) {
    const emptyMessages = {
      main: "No Electron entries yet. Main-process console output appears here when emitted.",
      ipc: "No IPC entries yet. Window controls and plugin tool calls appear here when used.",
      backend: "No backend entries yet. Backend lifecycle and request logs appear here when emitted.",
      mcp: "No MCP entries yet. Start or use a plugin to produce MCP process logs.",
      renderer: "No frontend entries yet. Renderer lifecycle and browser errors appear here.",
      all: "No shell logs have been retained yet.",
    };
    const empty = el("div", "log-empty", emptyMessages[logSourceFilter] ?? emptyMessages.all);
    tail.appendChild(empty);
    return;
  }

  filtered.forEach((entry) => {
    const row = el("div", "log-entry");
    const time = new Date(entry.timestamp).toLocaleTimeString("en-US", { hour12: false });
    row.innerHTML = `<span class="log-entry-time"></span><span class="log-entry-source"></span><span class="log-entry-level ${entry.level}"></span><span class="log-entry-message"></span>`;
    row.children[0].textContent = time;
    row.children[1].textContent = entry.source;
    row.children[2].textContent = entry.level;
    row.children[3].textContent = entry.message;
    tail.appendChild(row);
  });

  if (stickToBottom) tail.scrollTop = tail.scrollHeight;
}

function initCentralLogs() {
  const logs = window.shell?.logs;
  if (!logs) return;

  logs.onEntry(addLogEntry);
  logs.list().then((entries) => entries.forEach(addLogEntry)).catch((error) => {
    console.error("Failed to load the log tail:", error);
  });

  for (const [method, level] of [["debug", "debug"], ["info", "info"], ["log", "info"], ["warn", "warn"], ["error", "error"]]) {
    const original = console[method].bind(console);
    console[method] = (...args) => {
      original(...args);
      writeRendererLog(level, args.map((arg) => arg instanceof Error ? (arg.stack || arg.message) : String(arg)).join(" "));
    };
  }

  window.addEventListener("error", (event) => writeRendererLog("error", event.error?.stack || event.message));
  window.addEventListener("unhandledrejection", (event) => writeRendererLog("error", `Unhandled rejection: ${String(event.reason)}`));
  writeRendererLog("info", "Launcher renderer initialized");
}

function setPluginIcon(container, icon, size, installPath = "") {
  const presentation = pluginIconPresentation(icon);
  container.replaceChildren();
  container.classList.toggle("has-image", presentation.kind === "image");
  container.classList.add("bg-blue");

  if (presentation.kind === "image") {
    const image = document.createElement("img");
    image.className = "plugin-icon-image";
    image.alt = "";
    image.width = size;
    image.height = size;
    const showFallback = () => {
      container.classList.remove("has-image");
      container.classList.add("bg-blue");
      setPluginIcon(container, "🧩", Math.min(size, 28));
    };
    image.addEventListener("error", showFallback, { once: true });
    image.addEventListener("load", () => normalizeTransparentIcon(image), { once: true });
    container.appendChild(image);
    const localIcon = presentation.source.startsWith("file:");
    if (localIcon && window.shell?.pluginIcons?.read && installPath) {
      window.shell.pluginIcons.read(presentation.source, installPath)
        .then((dataUrl) => {
          image.src = dataUrl;
        })
        .catch(showFallback);
    } else {
      image.src = presentation.source;
    }
    return;
  }

  const glyph = document.createElement("span");
  glyph.className = "plugin-icon-glyph";
  glyph.style.fontSize = `${Math.round(size * 0.55)}px`;
  glyph.textContent = presentation.text;
  container.appendChild(glyph);
}

function stateBadgeHtml(state) {
  if (state === "running") return '<span class="status-dot"></span>Running';
  if (state === "starting") return '<span class="status-dot"></span>Starting';
  if (state === "stopping") return '<span class="status-dot"></span>Stopping';
  if (state === "crashed") return '<span class="status-dot"></span>Crashed';
  return '';
}

function updateConnStatus(connected) {
  const status = $("#conn-status");
  const fill = $("#conn-fill");
  const settingsDot = $("#settings-conn-dot");
  const settingsLabel = $("#settings-conn-label");

  if (connected) {
    status.textContent = "Connected";
    fill.classList.add("is-connected");
    settingsDot.style.background = "var(--green)";
    settingsDot.style.boxShadow = "0 0 0 3px rgba(47,191,113,0.15)";
    settingsLabel.textContent = "Connected";
  } else {
    status.textContent = "Disconnected";
    fill.classList.remove("is-connected");
    settingsDot.style.background = "var(--text-faint)";
    settingsDot.style.boxShadow = "none";
    settingsLabel.textContent = "Disconnected";
  }
}

// ============ API calls ============

async function fetchPlugins() {
  try {
    const result = await sendRequest("plugin.list", {});
    plugins = [...result.plugins];
  } catch (e) {
    console.error("Failed to fetch plugins:", e);
  }
}

async function startPlugin(pluginId) {
  try { await sendRequest("plugin.start", { pluginId }); } catch (e) { console.error(e); }
}

async function stopPlugin(pluginId) {
  try { await sendRequest("plugin.stop", { pluginId }); } catch (e) { console.error(e); }
}

async function restartPlugin(pluginId) {
  try { await sendRequest("plugin.restart", { pluginId }); } catch (e) { console.error(e); }
}

async function getPluginDetail(pluginId) {
  try { return await sendRequest("plugin.get", { pluginId }); } catch (e) { console.error(e); return null; }
}

async function listTools(pluginId) {
  try { return await sendRequest("tool.list", { pluginId }); } catch (e) { return { tools: [] }; }
}

async function callTool(pluginId, toolName, args) {
  const requestId = `req_${crypto.randomUUID()}`;
  try { return await sendRequest("tool.call", { pluginId, requestId, toolName, args }); }
  catch (e) { return { error: e.message }; }
}

async function pingSystem() {
  try { return await sendRequest("system.ping", {}); } catch (e) { return { error: e.message }; }
}

async function getVersion() {
  try { return await sendRequest("system.version", {}); } catch (e) { return { error: e.message }; }
}

async function installPlugin(source, path) {
  try { return await sendRequest("plugin.install", { source, path }, 30000); }
  catch (e) { return { error: e.message }; }
}

async function uninstallPlugin(pluginId) {
  try { return await sendRequest("plugin.uninstall", { pluginId }); }
  catch (e) { return { error: e.message }; }
}

async function setPluginAutostart(pluginId, autostart) {
  try { return await sendRequest("plugin.autostart", { pluginId, autostart }); }
  catch (e) { return { error: e.message }; }
}

async function runAgentTurn(messages, options = {}) {
  const selected = aiSettings.models.find((model) => model.key === aiSettings.activeModelKey);
  if (!selected) throw new Error("Choose an imported AI model before sending a turn.");
  const disposers = [];
  if (options.onDelta) {
    disposers.push(onEvent("agent.text_delta", (payload) => {
      if (payload?.traceId === options.traceId && payload.delta) options.onDelta(payload.delta);
    }));
  }
  if (options.onReasoningDelta) {
    disposers.push(onEvent("agent.reasoning_delta", (payload) => {
      if (payload?.traceId === options.traceId && payload.delta) options.onReasoningDelta(payload.delta);
    }));
  }
  if (options.onToolCallStart) {
    disposers.push(onEvent("agent.tool_call_start", (payload) => {
      if (payload?.traceId === options.traceId) options.onToolCallStart(payload);
    }));
  }
  if (options.onToolCallEnd) {
    disposers.push(onEvent("agent.tool_call_end", (payload) => {
      if (payload?.traceId === options.traceId) options.onToolCallEnd(payload);
    }));
  }
  if (options.onContextUpdate) {
    disposers.push(onEvent("agent.context", (payload) => {
      if (payload?.traceId === options.traceId) options.onContextUpdate(payload);
    }));
  }
  try {
    return await sendRequest("agent.run", {
      messages,
      pluginIds: [],
      providerId: selected.providerId,
      model: selected.id,
      effort: aiSettings.effort,
      userPrompt: aiSettings.userPrompt,
      ...(options.workspace ? { workspace: options.workspace } : {}),
      ...(options.resume ? { resume: true } : {}),
      modelCapabilities: {
        contextWindow: selected.contextWindow,
        maxOutput: selected.maxOutput,
        inputModes: selected.inputModes,
        outputModes: selected.outputModes,
        supportedEfforts: selected.supportedEfforts,
        defaultEffort: selected.defaultEffort,
        reasoningSupported: selected.reasoningSupported,
        reasoningMandatory: selected.reasoningMandatory,
        reasoningSupportsMaxTokens: selected.reasoningSupportsMaxTokens,
        supportsTools: selected.supportsTools,
        supportsVision: selected.supportsVision,
      },
      ...(options.traceId ? { traceId: options.traceId } : {}),
    }, 1800000);
  } finally {
    disposers.forEach((dispose) => dispose());
  }
}

async function cancelAgentTurn(traceId) {
  return sendRequest("agent.cancel", { traceId });
}

async function runAcpTurn(prompt, options = {}) {
  const providers = await window.shell.acpProviders.list();
  const selected = providers.find((p) => p.manifest.id === options.providerId);
  if (!selected) throw new Error("The ACP provider for this conversation is not configured.");
  const disposers = [];
  if (options.onDelta) {
    disposers.push(onEvent("acp.text_delta", (payload) => {
      if (payload?.traceId === options.traceId && payload.delta) options.onDelta(payload.delta, payload.messageId);
    }));
  }
  if (options.onReasoningDelta) {
    disposers.push(onEvent("acp.thought_delta", (payload) => {
      if (payload?.traceId === options.traceId && payload.delta) options.onReasoningDelta(payload.delta);
    }));
  }
  if (options.onToolCallStart) {
    disposers.push(onEvent("acp.tool_call", (payload) => {
      if (payload?.traceId === options.traceId) options.onToolCallStart({ callId: payload.call.id, name: payload.call.title, args: payload.call.rawInput ?? {} });
    }));
  }
  if (options.onToolCallEnd) {
    disposers.push(onEvent("acp.tool_call_update", (payload) => {
      if (payload?.traceId === options.traceId) options.onToolCallEnd({ callId: payload.callId, ok: payload.status === "ok", error: payload.status === "fail" ? "Failed" : undefined });
    }));
  }
  if (options.onTurnEnd) {
    disposers.push(onEvent("acp.turn_end", (payload) => {
      if (payload?.traceId === options.traceId) options.onTurnEnd({ ok: payload.ok, error: payload.error });
    }));
  }
  if (options.onPermissionRequest) {
    disposers.push(onEvent("acp.permission_request", (payload) => {
      if (payload?.traceId === options.traceId) options.onPermissionRequest(payload);
    }));
  }
  if (options.onAskRequest) {
    disposers.push(onEvent("acp.ask_request", (payload) => {
      if (payload?.traceId === options.traceId) options.onAskRequest(payload);
    }));
  }
  if (options.onTurnEnd) {
    disposers.push(onEvent("acp.turn_end", (payload) => {
      if (payload?.traceId === options.traceId) options.onTurnEnd({ ok: payload.ok, error: payload.error });
    }));
  }
  try {
    return await sendRequest("acp.run", {
      traceId: options.traceId,
      conversationId: options.conversationId,
      workspace: options.workspace,
      provider: { providerId: selected.manifest.id, command: selected.config.command, args: selected.config.args, authMethodId: selected.manifest.authMethodId },
      prompt,
    }, 1800000);
  } finally {
    disposers.forEach((dispose) => dispose());
  }
}

async function cancelAcpTurn(traceId, conversationId) {
  return sendRequest("acp.cancel", { traceId, conversationId });
}

async function getAcpSessionInfo(conversationId) {
  return sendRequest("acp.session_info", { conversationId });
}

async function setAcpConfigOption(conversationId, configId, value) {
  return sendRequest("acp.set_config_option", { conversationId, configId, value });
}

async function ensureAcpSession(conversationId, workspace, provider) {
  return sendRequest("acp.ensure_session", { conversationId, workspace, provider });
}

async function answerAcpPermission(payload) {
  return sendRequest("acp.permission_answer", payload, 30000);
}

async function answerAcpAsk(payload) {
  return sendRequest("acp.ask_answer", payload, 30000);
}

async function answerAskQuestion(payload) {
  return sendRequest("agent.ask_answer", payload, 30000);
}

// ============ View Switching ============

function switchView(viewName) {
  $$("[data-view]").forEach(v => {
    if (v.tagName === "SECTION") v.classList.toggle("active", v.dataset.view === viewName);
  });
  $$("[data-nav]").forEach(n => n.classList.toggle("active", n.dataset.view === viewName));
  closeDrawer();
  hideContextMenu();
  if (viewName === "agent") {
    agentConversationController?.renderList();
    agentConversationController?.scrollToBottom();
    agentConversationController?.updateContextStatus();
  }
  if (viewName === "skills") void skillsController?.refresh();
  if (viewName === "learning") learningController?.initialize();
  if (viewName === "autostart") renderAutostartList();
  if (viewName === "jobs") void jobsController.refresh();
  if (viewName === "settings") void syncAppBehaviorControls();
}

async function syncAppBehaviorControls() {
  const launchAtLogin = $("#settings-launch-at-login");
  const startHidden = $("#settings-start-hidden");
  const keepInBackground = $("#settings-keep-in-background");
  const help = $("#settings-launch-at-login-help");
  if (!launchAtLogin || !startHidden || !keepInBackground || !window.shell?.appBehavior) return;
  try {
    const settings = await window.shell.appBehavior.get();
    launchAtLogin.checked = Boolean(settings.launchAtLogin);
    startHidden.checked = Boolean(settings.startHidden);
    keepInBackground.checked = Boolean(settings.keepInBackground);
    launchAtLogin.disabled = !settings.canSetLoginAutostart;
    if (help) {
      help.textContent = settings.canSetLoginAutostart
        ? "Starts NusaShell when you log in."
        : "Starts NusaShell when you log in. Requires a packaged build.";
    }
  } catch (error) {
    showToast(`Could not load startup settings: ${error.message || error}`, "error");
  }
}

function wireAppBehaviorToggle(id, key) {
  const input = $(id);
  if (!input || !window.shell?.appBehavior) return;
  input.addEventListener("change", async () => {
    const previous = !input.checked;
    input.disabled = true;
    try {
      const settings = await window.shell.appBehavior.set({ [key]: input.checked });
      input.checked = Boolean(settings[key]);
      const launchAtLogin = $("#settings-launch-at-login");
      if (launchAtLogin) {
        launchAtLogin.disabled = !settings.canSetLoginAutostart;
        launchAtLogin.checked = Boolean(settings.launchAtLogin);
      }
      const startHidden = $("#settings-start-hidden");
      if (startHidden) startHidden.checked = Boolean(settings.startHidden);
      const keepInBackground = $("#settings-keep-in-background");
      if (keepInBackground) keepInBackground.checked = Boolean(settings.keepInBackground);
      showToast("Startup settings saved.", "success");
    } catch (error) {
      input.checked = previous;
      showToast(`Could not save startup settings: ${error.message || error}`, "error");
    } finally {
      try {
        const settings = await window.shell.appBehavior.get();
        input.disabled = key === "launchAtLogin" && !settings.canSetLoginAutostart;
      } catch {
        input.disabled = false;
      }
    }
  });
}

function renderAutostartList() {
  const list = $("#autostart-list");
  const count = $("#autostart-count");
  if (!list || !count) return;
  count.textContent = `${plugins.filter((plugin) => plugin.autostart).length} enabled`;
  list.textContent = "";
  if (plugins.length === 0) {
    list.appendChild(el("div", "agent-scope-empty", "No MCP plugins are installed yet."));
    return;
  }
  plugins.forEach((plugin) => {
    const row = el("div", "autostart-row");
    const icon = el("div", "autostart-icon");
    setPluginIcon(icon, plugin.icon || "🧩", 28, plugin.installPath);
    const info = el("div", "autostart-info");
    const name = el("div", "autostart-name"); name.textContent = plugin.name;
    const meta = el("div", "autostart-meta"); meta.textContent = `${plugin.pluginId} · ${plugin.state}`;
    info.append(name, meta);
    const toggle = document.createElement("input");
    toggle.className = "autostart-toggle";
    toggle.type = "checkbox";
    toggle.checked = Boolean(plugin.autostart);
    toggle.setAttribute("aria-label", `Start ${plugin.name} when NusaShell opens`);
    toggle.addEventListener("change", async () => {
      toggle.disabled = true;
      const result = await setPluginAutostart(plugin.pluginId, toggle.checked);
      if (result.error) { toggle.checked = !toggle.checked; showToast(`Autostart update failed: ${result.error}`, "error"); }
      else { plugin.autostart = toggle.checked; renderAutostartList(); }
      toggle.disabled = false;
    });
    row.append(icon, info, toggle);
    list.appendChild(row);
  });
}

// ============ Agent workspace ============

// ============ Render: App Grid (Home) ============

function renderAppGrid() {
  const grid = $("#app-grid");
  grid.innerHTML = "";
  if (plugins.length === 0) {
    grid.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No plugins installed. Add a plugin folder to plugins/.</div>';
    return;
  }
  const visiblePlugins = filterLauncherPlugins(plugins, launcherSearchQuery);
  if (visiblePlugins.length === 0) {
    grid.appendChild(el("div", "app-grid-empty", `No plugins match “${launcherSearchQuery}”.`));
    return;
  }
  visiblePlugins.forEach(p => {
    const cell = el("button", "app-cell");
    cell.dataset.pluginId = p.pluginId;
    const icon = el("div", "app-icon");
    setPluginIcon(icon, p.icon || "🧩", 60, p.installPath);
    const name = el("div", "app-name");
    name.textContent = p.name;
    const status = el("div", `app-status ${p.state}`);
    status.innerHTML = stateBadgeHtml(p.state);
    cell.append(icon, name, status);
    cell.addEventListener("click", () => openPluginWindow(p));
    cell.addEventListener("contextmenu", (e) => { e.preventDefault(); showContextMenu(e.clientX, e.clientY, p); });
    grid.appendChild(cell);
  });
}

// ============ Render: Installed Table ============

function renderInstalledTable() {
  const table = $("#plugin-table");
  table.innerHTML = "";
  if (plugins.length === 0) {
    table.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No plugins installed.</div>';
    return;
  }
  plugins.forEach(p => {
    const row = el("div", "plugin-row");
    row.dataset.pluginId = p.pluginId;
    const icon = el("div", "plugin-row-icon");
    setPluginIcon(icon, p.icon || "🧩", 38, p.installPath);
    const info = el("div", "plugin-row-info");
    const name = el("div", "plugin-row-name");
    name.textContent = p.name;
    const meta = el("div", "plugin-row-meta");
    meta.textContent = `${p.pluginId} · v${p.version}`;
    info.append(name, meta);
    const state = el("div", `plugin-row-state ${p.state}`);
    state.innerHTML = stateBadgeHtml(p.state) || "Idle";
    row.append(icon, info, state);
    row.addEventListener("click", () => openDrawer(p));
    table.appendChild(row);
  });
}

// ============ Plugin Detail Drawer ============

async function openDrawer(plugin) {
  currentPlugin = plugin;
  $("#drawer-icon").className = "drawer-icon";
  setPluginIcon($("#drawer-icon"), plugin.icon || "🧩", 38, plugin.installPath);
  $("#drawer-title").textContent = plugin.name;
  $("#drawer-subtitle").textContent = `${plugin.pluginId} · v${plugin.version}`;

  const sm = $("#state-machine");
  sm.innerHTML = "";
  STATES.forEach((s, i) => {
    if (i > 0) sm.appendChild(el("span", "state-arrow", "→"));
    sm.appendChild(el("span", `state-node${s === plugin.state ? " current" : ""}`, s));
  });

  const toolsResult = await listTools(plugin.pluginId);
  const tools = toolsResult?.tools ?? [];
  $("#tool-count").textContent = tools.length;
  const tl = $("#tools-list");
  tl.innerHTML = "";
  if (tools.length === 0) {
    tl.innerHTML = '<div style="color:var(--text-faint);font-size:12px">No tools available. Start the plugin to discover tools.</div>';
  } else {
    tools.forEach(t => {
      const item = el("div", "tool-item");
      item.innerHTML = `<div class="tool-item-info"><div class="tool-item-name">${t.name}</div><div class="tool-item-desc">${t.description || ""}</div></div>`;
      tl.appendChild(item);
    });
  }

  const detail = await getPluginDetail(plugin.pluginId);
  const m = detail || plugin;
  $("#manifest-info").innerHTML = `<div class="manifest-row"><span class="manifest-key">id</span><span class="manifest-val">${plugin.pluginId}</span></div><div class="manifest-row"><span class="manifest-key">version</span><span class="manifest-val">${plugin.version}</span></div><div class="manifest-row"><span class="manifest-key">state</span><span class="manifest-val">${plugin.state}</span></div><div class="manifest-row"><span class="manifest-key">enabled</span><span class="manifest-val">${plugin.enabled}</span></div>`;

  $("#plugin-drawer").classList.add("active");
  $("#drawer-overlay").classList.add("active");
}

function closeDrawer() {
  $("#plugin-drawer").classList.remove("active");
  $("#drawer-overlay").classList.remove("active");
  currentPlugin = null;
}

// ============ Plugin Window (opens in separate BrowserWindow via IPC) ============

async function openPluginWindow(plugin) {
  try {
    if (plugin.state === "idle") {
      await sendRequest("plugin.start", { pluginId: plugin.pluginId });
    }
    const installPath = plugin.installPath || "";
    if (window.shell?.openPlugin) {
      await window.shell.openPlugin(
        plugin.pluginId,
        plugin.name,
        plugin.icon || "🧩",
        installPath,
        {
          ...(plugin.ui || {}),
          keepAliveOnClose: Boolean(plugin.keepAliveOnClose),
        },
      );
    }
  } catch (err) {
    console.error("[openPluginWindow] error:", err);
  }
}

// ============ Context Menu ============

function showContextMenu(x, y, plugin) {
  const menu = $("#context-menu");
  menu.style.display = "block";
  menu.dataset.pluginId = plugin.pluginId;
  setContextMenuMode("plugin");
  $$("#context-menu .ctx-item").forEach((item) => { item.disabled = false; });
  const point = positionContextMenu(
    { x, y },
    { width: menu.offsetWidth || 180, height: menu.offsetHeight || 200 },
    { width: window.innerWidth, height: window.innerHeight },
  );
  menu.style.left = `${point.x}px`;
  menu.style.top = `${point.y}px`;
  menu.querySelector(".ctx-item")?.focus();
}

function isEditableTextControl(target) {
  if (target instanceof HTMLTextAreaElement) return true;
  return target instanceof HTMLInputElement
    && ["text", "search", "url", "tel", "password"].includes(target.type);
}

function showEditContextMenu(x, y, target) {
  const menu = $("#context-menu");
  editContextTarget = target;
  menu.style.display = "block";
  delete menu.dataset.pluginId;
  setContextMenuMode("edit");
  $$("#context-menu .ctx-item").forEach((item) => {
    item.disabled = target.readOnly && item.dataset.action !== "copy";
  });
  const point = positionContextMenu(
    { x, y },
    { width: menu.offsetWidth || 180, height: menu.offsetHeight || 150 },
    { width: window.innerWidth, height: window.innerHeight },
  );
  menu.style.left = `${point.x}px`;
  menu.style.top = `${point.y}px`;
}

function setContextMenuMode(mode) {
  const menu = $("#context-menu");
  $$("#context-menu .ctx-item").forEach((item) => {
    const action = item.dataset.action;
    const isEditAction = ["cut", "copy", "paste"].includes(action);
    item.hidden = mode === "edit" ? !isEditAction : isEditAction;
  });
  $$("#context-menu .ctx-divider").forEach((divider) => divider.hidden = mode === "edit");
}

function hideContextMenu() {
  $("#context-menu").style.display = "none";
  if (!$("#context-menu").dataset.pluginId) editContextTarget = null;
}

async function runEditContextAction(action) {
  const target = editContextTarget;
  if (!isEditableTextControl(target) || (target.readOnly && action !== "copy")) return;
  const clipboard = window.shell?.clipboard;
  const clipboardText = action === "paste" ? (clipboard?.readText() ?? "") : "";
  const result = applyTextEdit({
    value: target.value,
    selectionStart: target.selectionStart,
    selectionEnd: target.selectionEnd,
  }, action, clipboardText);

  if ((action === "copy" || action === "cut") && result.clipboardText) {
    clipboard?.writeText(result.clipboardText);
  }
  if (action !== "copy" && result.value !== target.value) {
    target.value = result.value;
    target.dispatchEvent(new Event("input", { bubbles: true }));
  }
  target.focus();
  target.setSelectionRange(result.selectionStart, result.selectionEnd);
}

function setSidebarCompact(compact, persist = true) {
  const sidebar = $("#sidebar");
  const toggle = $("#sidebar-mode-toggle");
  sidebar.classList.toggle("is-compact", compact);
  toggle.setAttribute("aria-pressed", String(compact));
  toggle.setAttribute("aria-label", compact ? "Expand sidebar" : "Collapse sidebar");
  toggle.title = compact ? "Show icons and text" : "Use icon-only sidebar";
  toggle.querySelector(".nav-label").textContent = compact ? "Show labels" : "Collapse Sidebar";
  if (persist) localStorage.setItem("nusashell.sidebarMode", compact ? "icons" : "full");
}

// ============ Event handling ============

function handlePluginEvent(payload, eventType) {
  const idx = plugins.findIndex(p => p.pluginId === payload.pluginId);
  if (idx >= 0) {
    const newState = payload.state ?? payload.newState;
    if (newState) {
      plugins[idx] = { ...plugins[idx], state: newState };
    }
  }
  if (eventType === "plugin.installed" || eventType === "plugin.uninstalled") {
    refreshAll();
  }
  renderAppGrid();
  renderInstalledTable();
  renderAutostartList();
  if (currentPlugin?.pluginId === payload.pluginId) {
    openDrawer(plugins[idx] ?? currentPlugin);
  }
}

// ============ Toast ============

// ============ Jobs Controller ============

const jobsController = {
  list: [],
  _modalOpen: false,

  async refresh() {
    try {
      const result = await sendRequest("job.list", {});
      this.list = result.jobs ?? [];
      this.render();
    } catch (error) {
      showToast(`Could not load jobs: ${error.message || error}`, "error");
    }
  },

  render() {
    const container = $("#jobs-list");
    if (!container) return;
    container.innerHTML = "";
    if (this.list.length === 0) {
      container.innerHTML = '<div class="empty-state">No jobs yet. Click “New Job” to create one.</div>';
      return;
    }
    for (const job of this.list) {
      container.appendChild(this._renderRow(job));
    }
  },

  _renderRow(job) {
    const row = el("div", "job-row");
    row.dataset.jobId = job.id;
    const statusDot = el("span", `job-status-dot job-status-${job.lastStatus ?? "idle"}`);
    const info = el("div", "job-info");
    const title = el("div", "job-title");
    title.textContent = job.name;
    const meta = el("div", "job-meta");
    meta.textContent = `${describeJobSchedule(job.schedule)} · ${describeJobMode(job.mode)}${job.repeat.times ? ` · ${job.repeat.completed}/${job.repeat.times}` : ""}`;
    info.appendChild(title);
    info.appendChild(meta);
    const next = el("div", "job-next");
    next.textContent = job.enabled ? `next: ${job.nextRunAt ?? "—"}` : "paused";
    const actions = el("div", "job-actions");
    const runBtn = el("button", "mini-btn");
    runBtn.textContent = "Run";
    runBtn.dataset.control = "job-run-btn";
    runBtn.addEventListener("click", () => this.runJob(job.id));
    const toggleBtn = el("button", "mini-btn");
    toggleBtn.textContent = job.enabled ? "Pause" : "Resume";
    toggleBtn.dataset.control = "job-toggle-btn";
    toggleBtn.addEventListener("click", () => this.toggleJob(job.id, !job.enabled));
    const outputBtn = el("button", "mini-btn");
    outputBtn.textContent = "Output";
    outputBtn.dataset.control = "job-output-btn";
    outputBtn.addEventListener("click", () => this.showOutput(job.id, job.name));
    const removeBtn = el("button", "mini-btn danger-btn");
    removeBtn.textContent = "Remove";
    removeBtn.dataset.control = "job-remove-btn";
    removeBtn.addEventListener("click", () => this.removeJob(job.id));
    actions.append(runBtn, toggleBtn, outputBtn, removeBtn);
    row.append(statusDot, info, next, actions);
    return row;
  },

  openModal() {
    this._modalOpen = true;
    $("#job-modal-title").textContent = "New Job";
    $("#job-field-name").value = "";
    $("#job-field-schedule").value = "";
    $("#job-field-mode").value = "agent";
    $("#job-field-prompt").value = "";
    $("#job-field-plugin-id").value = "";
    $("#job-field-tool-name").value = "";
    $("#job-field-args").value = "{}";
    $("#job-field-repeat").value = "";
    $("#job-schedule-help").textContent = "";
    this._toggleModeFields();
    $("#job-modal").style.display = "flex";
  },

  closeModal() {
    this._modalOpen = false;
    $("#job-modal").style.display = "none";
  },

  _toggleModeFields() {
    const mode = $("#job-field-mode").value;
    $("#job-agent-prompt-label").style.display = mode === "agent" ? "" : "none";
    $("#job-tool-fields").style.display = mode === "tool" ? "" : "none";
  },

  async validateSchedule() {
    const schedule = $("#job-field-schedule").value.trim();
    const help = $("#job-schedule-help");
    if (!schedule) { help.textContent = ""; return; }
    try {
      const result = await sendRequest("job.validate-schedule", { schedule });
      help.textContent = result.ok ? `✓ ${result.description}` : `✗ ${result.error}`;
      help.style.color = result.ok ? "var(--accent)" : "var(--danger)";
    } catch (error) {
      help.textContent = `✗ ${error.message || error}`;
      help.style.color = "var(--danger)";
    }
  },

  async saveJob() {
    const name = $("#job-field-name").value.trim();
    const schedule = $("#job-field-schedule").value.trim();
    const modeType = $("#job-field-mode").value;
    if (!name || !schedule) { showToast("Name and schedule are required", "error"); return; }
    let mode;
    if (modeType === "agent") {
      const prompt = $("#job-field-prompt").value.trim();
      if (!prompt) { showToast("Prompt is required for agent mode", "error"); return; }
      mode = { type: "agent", prompt };
    } else {
      const pluginId = $("#job-field-plugin-id").value.trim();
      const toolName = $("#job-field-tool-name").value.trim();
      const argsText = $("#job-field-args").value.trim() || "{}";
      let args;
      try { args = JSON.parse(argsText); } catch { showToast("Args must be valid JSON", "error"); return; }
      if (!pluginId || !toolName) { showToast("Plugin ID and tool name are required", "error"); return; }
      mode = { type: "tool", pluginId, toolName, args };
    }
    const repeatRaw = $("#job-field-repeat").value.trim();
    const payload = { name, schedule, mode };
    if (repeatRaw) payload.repeatTimes = parseInt(repeatRaw, 10);
    try {
      await sendRequest("job.add", payload);
      showToast("Job created", "success");
      this.closeModal();
      await this.refresh();
    } catch (error) {
      showToast(`Could not create job: ${error.message || error}`, "error");
    }
  },

  async runJob(id) {
    try {
      const result = await sendRequest("job.run", { id });
      if (!result.ok) showToast(`Run failed: ${result.error ?? "unknown"}`, "error");
      else showToast("Job started", "success");
      await this.refresh();
    } catch (error) {
      showToast(`Could not run job: ${error.message || error}`, "error");
    }
  },

  async toggleJob(id, enabled) {
    try {
      await sendRequest("job.set-enabled", { id, enabled });
      await this.refresh();
    } catch (error) {
      showToast(`Could not update job: ${error.message || error}`, "error");
    }
  },

  async removeJob(id) {
    if (!confirm("Remove this job?")) return;
    try {
      await sendRequest("job.remove", { id });
      await this.refresh();
    } catch (error) {
      showToast(`Could not remove job: ${error.message || error}`, "error");
    }
  },

  async showOutput(id, name) {
    try {
      const result = await sendRequest("job.output", { id, limit: 20 });
      $("#job-output-title").textContent = `Output: ${name}`;
      const body = $("#job-output-body");
      body.innerHTML = "";
      const entries = result.outputs ?? [];
      if (entries.length === 0) {
        body.innerHTML = '<div class="empty-state">No output yet.</div>';
      } else {
        for (const entry of entries) {
          const card = el("div", "job-output-entry");
          const header = el("div", "job-output-header");
          header.textContent = `${entry.runAt} · ${entry.status}`;
          if (entry.status === "error") header.classList.add("job-output-error");
          const summary = el("pre", "job-output-summary");
          summary.textContent = entry.summary;
          card.appendChild(header);
          card.appendChild(summary);
          body.appendChild(card);
        }
      }
      $("#job-output-modal").style.display = "flex";
    } catch (error) {
      showToast(`Could not load output: ${error.message || error}`, "error");
    }
  },
};

function describeJobSchedule(schedule) {
  if (!schedule) return "—";
  if (schedule.kind === "once") return `once @ ${schedule.runAt}`;
  if (schedule.kind === "interval") {
    const m = schedule.minutes;
    if (m % 1440 === 0) return `every ${m / 1440}d`;
    if (m % 60 === 0) return `every ${m / 60}h`;
    return `every ${m}m`;
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

function showToast(message, type = "info") {
  const container = $("#toast-container");
  const toast = el("div", `toast toast-${type}`);
  toast.textContent = message;
  container.appendChild(toast);
  setTimeout(() => { toast.classList.add("toast-show"); }, 10);
  setTimeout(() => {
    toast.classList.remove("toast-show");
    setTimeout(() => toast.remove(), 300);
  }, 4000);
}

// ============ Add Plugin Modal ============

function openAddPluginModal() {
  $("#add-plugin-modal").style.display = "flex";
  $("#install-url-input").value = "";
  $("#install-local-input").value = "";
  $("#install-status").style.display = "none";
  $("#install-url-input").focus();
}

function closeAddPluginModal() {
  $("#add-plugin-modal").style.display = "none";
}

function showInstallStatus(message, isError) {
  const status = $("#install-status");
  status.style.display = "block";
  status.className = `modal-status${isError ? " modal-status-error" : " modal-status-info"}`;
  status.textContent = message;
}

async function doInstall(source, path) {
  if (!path || !path.trim()) {
    showInstallStatus(source === "local" ? "Choose a plugin folder or archive first." : "Enter a plugin URL first.", true);
    return;
  }
  showInstallStatus("Installing...", false);
  const result = await installPlugin(source, path.trim());
  if (result.error) {
    showInstallStatus(`Error: ${result.error}`, true);
    showToast(`Install failed: ${result.error}`, "error");
  } else {
    showInstallStatus(`Installed ${result.pluginId} v${result.version}`, false);
    showToast(`Plugin ${result.pluginId} installed`, "success");
    setTimeout(() => closeAddPluginModal(), 1000);
    await refreshAll();
  }
}

// ============ Uninstall ============

async function doUninstall(pluginId, pluginName) {
  if (!confirm(`Uninstall "${pluginName || pluginId}"? This cannot be undone.`)) return;
  const result = await uninstallPlugin(pluginId);
  if (result.error) {
    showToast(`Uninstall failed: ${result.error}`, "error");
  } else {
    showToast(`Plugin ${pluginId} uninstalled`, "success");
    closeDrawer();
    await refreshAll();
  }
}

// ============ Auto-update banner ============

function initUpdater() {
  if (!window.shell?.updater) return;

  const banner = $("#update-banner");
  const bannerText = $("#update-banner-text");
  const bannerBtn = $("#update-banner-btn");
  const bannerClose = $("#update-banner-close");

  bannerClose.addEventListener("click", () => { banner.style.display = "none"; });
  bannerBtn.addEventListener("click", () => {
    if (confirm("Restart now to apply the update?")) {
      window.shell.updater.quitAndInstall();
    }
  });

  window.shell.updater.on("update-available", (info) => {
    banner.style.display = "flex";
    bannerText.textContent = `Update available: v${info.version}`;
    bannerBtn.style.display = "none";
  });

  window.shell.updater.on("update-not-available", () => {
    banner.style.display = "none";
  });

  window.shell.updater.on("download-progress", (progress) => {
    const pct = Math.round(progress.percent || 0);
    bannerText.textContent = `Downloading update... ${pct}%`;
  });

  window.shell.updater.on("update-downloaded", (info) => {
    banner.style.display = "flex";
    bannerText.textContent = `Update ready: v${info.version}`;
    bannerBtn.style.display = "inline-block";
  });

  window.shell.updater.on("update-error", (data) => {
    showToast(`Update error: ${data?.message || "unknown"}`, "error");
  });

  window.shell.updater.checkForUpdates().catch(() => {});
}

// ============ Refresh all views ============

async function refreshAll() {
  await fetchPlugins();
  renderAppGrid();
  renderInstalledTable();
  renderAutostartList();
}

// ============ Init ============

document.addEventListener("DOMContentLoaded", () => {
  initCentralLogs();
  const savedSidebarMode = localStorage.getItem("nusashell.sidebarMode");
  setSidebarCompact(savedSidebarMode ? savedSidebarMode === "icons" : window.innerWidth <= 960, false);

  const windowControls = window.shell?.windowControls;
  if (windowControls) {
    $("#window-minimize").addEventListener("click", () => windowControls.minimize());
    $("#window-maximize").addEventListener("click", () => windowControls.toggleMaximize());
    $("#window-close").addEventListener("click", () => windowControls.close());

    const alwaysOnTopButton = $("#window-always-on-top");
    alwaysOnTopButton.addEventListener("click", async () => {
      alwaysOnTopButton.disabled = true;
      try {
        const isActive = await windowControls.toggleAlwaysOnTop();
        const label = isActive ? "Stop keeping window on top" : "Keep window on top";
        alwaysOnTopButton.classList.toggle("is-active", isActive);
        alwaysOnTopButton.setAttribute("aria-pressed", String(isActive));
        alwaysOnTopButton.setAttribute("aria-label", label);
        alwaysOnTopButton.title = label;
      } catch (error) {
        showToast(error instanceof Error ? error.message : "Could not change always-on-top mode", "error");
      } finally {
        alwaysOnTopButton.disabled = false;
      }
    });
  }

  // Display WS URL in settings
  $("#settings-ws-url").textContent = WS_URL;

  // Nav switching
  $$("[data-nav]").forEach(item => item.addEventListener("click", () => switchView(item.dataset.view)));
  $("#nav-settings-btn").addEventListener("click", () => switchView("settings"));
  $("#sidebar-mode-toggle").addEventListener("click", () => {
    setSidebarCompact(!$("#sidebar").classList.contains("is-compact"));
  });
  $("#open-docs").addEventListener("click", () => {
    window.shell?.shellControls?.openDocs().catch((error) => {
      showToast(`Could not open docs: ${error.message || error}`, "error");
    });
  });

  const providerPresets = {
    openrouter: { id: "openrouter", type: "openrouter", label: "OpenRouter", baseUrl: "https://openrouter.ai/api/v1", api: "chat", detail: "API key · OpenAI-compatible endpoint", apiKeyOptional: false },
    omniroute: { id: "omniroute", type: "omniroute", label: "OmniRoute", baseUrl: "http://127.0.0.1:20128/v1", api: "responses", detail: "Local OpenAI-compatible gateway", apiKeyOptional: true },
    "9router": { id: "9router", type: "9router", label: "9Router", baseUrl: "http://127.0.0.1:20128/v1", api: "chat", detail: "Local OpenAI-compatible gateway", apiKeyOptional: true },
    openai: { id: "openai", type: "openai", label: "OpenAI", baseUrl: "https://api.openai.com/v1", api: "responses", detail: "Official OpenAI endpoint", apiKeyOptional: false },
    claude: { id: "claude", type: "claude", label: "Claude API", baseUrl: "https://api.anthropic.com/v1", api: "messages", detail: "Anthropic model catalog · Messages compatibility", apiKeyOptional: false },
    custom: { id: "", type: "openai-compatible", label: "Custom provider", baseUrl: "", api: "chat", detail: "OpenAI-compatible endpoint", apiKeyOptional: false },
  };
  const builtInProviderIds = new Set(Object.values(providerPresets).map((preset) => preset.id).filter(Boolean));

  const configuredProvider = (providerId) => aiSettings.providers.find((provider) => provider.id === providerId);
  const activeModel = () => aiSettings.models.find((model) => model.key === aiSettings.activeModelKey);
  const setProviderEnabled = async (provider, enabled) => {
    try {
      aiSettings = await window.shell.aiProviders.save({
        id: provider.id,
        name: provider.name,
        type: provider.type,
        api: provider.api,
        baseUrl: provider.baseUrl,
        apiKey: "",
        apiKeyOptional: provider.apiKeyOptional,
        enabled,
        defaultModel: provider.defaultModel,
        timeoutMs: provider.timeoutMs,
        maxAttempts: provider.maxAttempts,
        weight: provider.weight,
      });
      syncAiControls();
      showToast(`${provider.name} ${enabled ? "enabled" : "disabled"}.`, "success");
    } catch (error) {
      showToast(`Could not ${enabled ? "enable" : "disable"} ${provider.name}: ${error.message || error}`, "error");
    }
  };
  const closeProviderEditor = () => { $("#ai-settings-form").hidden = true; $("#provider-modal-overlay").hidden = true; };
  const closeAcpProviderEditor = () => { $("#acp-provider-form").hidden = true; $("#acp-provider-modal-overlay").hidden = true; };
  const showAcpProviderEditor = (provider) => {
    $("#acp-provider-modal-overlay").hidden = false;
    $("#acp-provider-form").hidden = false;
    $("#acp-provider-title").textContent = `Configure ${provider.manifest.displayName}`;
    $("#acp-provider-subtitle").textContent = provider.manifest.description;
    $("#acp-provider-id").value = provider.manifest.id;
    $("#acp-provider-enabled").checked = provider.config.enabled;
    $("#acp-provider-command").value = provider.config.command || "";
    $("#acp-provider-args").value = (provider.config.args || []).join(" ");
    $("#acp-provider-auth-method").textContent = provider.manifest.authMethodId ? `Auth: ${provider.manifest.authMethodId}` : "No auth required";
  };
  const closeProviderDeleteDialog = () => {
    pendingProviderDeleteId = "";
    $("#provider-delete-dialog").hidden = true;
    $("#provider-delete-overlay").hidden = true;
    $("#provider-delete-confirm").disabled = false;
    $("#provider-delete-confirm").textContent = "Delete";
  };
  const openProviderDeleteDialog = (providerId) => {
    const provider = configuredProvider(providerId);
    if (!provider) return;
    pendingProviderDeleteId = provider.id;
    $("#provider-delete-title").textContent = `Delete ${provider.name}?`;
    $("#provider-delete-copy").textContent = "This removes its saved credential, imported models, and connection settings from this device.";
    $("#provider-delete-dialog").hidden = false;
    $("#provider-delete-overlay").hidden = false;
    $("#provider-delete-cancel").focus();
  };
  agentConversationController = new AgentConversationController({
    shell: window.shell,
    runTurn: runAgentTurn,
    cancelTurn: cancelAgentTurn,
    answerAsk: answerAskQuestion,
    getActiveModel: activeModel,
    getVisionMode: () => aiSettings.vision,
    notify: showToast,
    log: writeRendererLog,
    runAcpTurn,
    cancelAcpTurn,
    answerAcpPermission,
    answerAcpAsk,
    getAcpSessionInfo,
    setAcpConfigOption,
    ensureAcpSession,
  });
  skillsController = new SkillsController({
    shell: window.shell,
    notify: showToast,
    log: writeRendererLog,
  });
  learningController = new LearningController(window.shell);

  const renderProviderCards = () => {
    $$("[data-custom-provider-card]").forEach((card) => card.remove());
    $$("[data-provider-preset]").forEach((card) => {
      const preset = providerPresets[card.dataset.providerPreset];
      const provider = preset && configuredProvider(preset.id);
      const configured = provider && (provider.hasApiKey || provider.apiKeyOptional);
      const status = card.querySelector(".provider-status");
      const dot = card.querySelector(".provider-toggle");
      const action = card.querySelector(".provider-card-action");
      const apiKind = card.querySelector(".provider-card-head p");
      const footer = card.querySelector(".provider-card-footer");
      let actions = footer?.querySelector(".provider-card-actions");
      if (footer && action && !actions) {
        actions = el("div", "provider-card-actions");
        action.replaceWith(actions);
        actions.appendChild(action);
      }
      actions?.querySelector(".provider-card-delete")?.remove();
      status.textContent = provider ? (configured ? `● Configured · ${provider.models.length} models` : "● Needs API key") : "● Not configured";
      if (apiKind) {
        apiKind.textContent = `${preset.apiKeyOptional ? "LOCAL" : "API KEY"} · ${(provider?.api || preset.api).toUpperCase()}`;
      }
      status.classList.toggle("configured", Boolean(configured));
      dot?.classList.toggle("is-active", Boolean(configured && provider.enabled));
      if (dot) {
        dot.disabled = !provider;
        dot.setAttribute("aria-pressed", String(Boolean(provider?.enabled)));
        dot.setAttribute("aria-label", provider
          ? `${provider.enabled ? "Disable" : "Enable"} ${provider.name}`
          : `Configure ${preset.label}`);
        dot.onclick = provider ? () => void setProviderEnabled(provider, !provider.enabled) : null;
      }
      card.classList.toggle("is-active", aiSettings.activeProviderId === preset?.id);
      if (action) action.textContent = provider ? "Details" : "Configure";
      if (provider && actions) {
        const remove = el("button", "mini-btn danger provider-card-delete", "Delete");
        remove.type = "button";
        remove.addEventListener("click", () => openProviderDeleteDialog(provider.id));
        actions.appendChild(remove);
      }
    });
    aiSettings.providers.filter((provider) => !builtInProviderIds.has(provider.id)).forEach((provider) => {
      const card = el("article", "provider-registry-card accent-custom");
      card.dataset.customProviderCard = provider.id;
      const head = el("div", "provider-card-head");
      const mark = el("span", "provider-mark", "OC");
      const identity = el("div");
      const title = document.createElement("h2"); title.textContent = provider.name;
      const kind = document.createElement("p"); kind.textContent = `${provider.api.toUpperCase()} · CUSTOM`;
      identity.append(title, kind);
      const configured = provider.hasApiKey || provider.apiKeyOptional;
      const dot = el("button", `provider-toggle${configured && provider.enabled ? " is-active" : ""}`, "●");
      dot.type = "button";
      dot.setAttribute("aria-pressed", String(provider.enabled));
      dot.setAttribute("aria-label", `${provider.enabled ? "Disable" : "Enable"} ${provider.name}`);
      dot.addEventListener("click", () => void setProviderEnabled(provider, !provider.enabled));
      head.append(mark, identity, dot);
      const description = document.createElement("p"); description.textContent = provider.baseUrl;
      const footer = el("div", "provider-card-footer");
      const status = el("span", `provider-status${configured ? " configured" : ""}`, configured ? `● Configured · ${provider.models.length} models` : "● Needs API key");
      const actions = el("div", "provider-card-actions");
      const action = el("button", "mini-btn", "Details"); action.type = "button";
      action.addEventListener("click", () => showProviderDetail(provider.id));
      const remove = el("button", "mini-btn danger", "Delete"); remove.type = "button";
      remove.addEventListener("click", () => openProviderDeleteDialog(provider.id));
      actions.append(action, remove);
      footer.append(status, actions);
      card.append(head, description, footer);
      $("#provider-registry").appendChild(card);
    });
  };

  const renderAcpProviderCards = async () => {
    const registry = $("#acp-provider-registry");
    if (!registry) return;
    registry.textContent = "";
    try {
      const providers = await window.shell.acpProviders.list();
      for (const provider of providers) {
        const card = el("article", `provider-registry-card acp-provider-card${provider.status === "configured" ? " is-active" : ""}`);
        card.dataset.acpProviderId = provider.manifest.id;
        const isUnverifiedProvider = Boolean(provider.manifest.unverified) === true;
        const kindLabel = isUnverifiedProvider ? "ACP · UNVERIFIED" : "ACP";
        const head = el("div", "provider-card-head");
        const mark = el("span", "provider-mark", provider.manifest.monogram);
        const identity = el("div");
        const title = document.createElement("h2"); title.textContent = provider.manifest.displayName;
        const kind = document.createElement("p"); kind.textContent = String(kindLabel || "ACP");
        identity.append(title, kind);
        const beta = isUnverifiedProvider ? el("span", "acp-provider-beta", "BETA") : null;
        const dot = el("button", `provider-toggle${provider.config.enabled ? " is-active" : ""}`, "●");
        dot.type = "button";
        dot.setAttribute("aria-pressed", String(provider.config.enabled));
        dot.setAttribute("aria-label", `${provider.config.enabled ? "Disable" : "Enable"} ${provider.manifest.displayName}`);
        dot.addEventListener("click", async () => {
          try {
            await window.shell.acpProviders.save({ providerId: provider.manifest.id, enabled: !provider.config.enabled });
            await renderAcpProviderCards();
            showToast(`${provider.manifest.displayName} ${!provider.config.enabled ? "enabled" : "disabled"}.`, "success");
          } catch (error) {
            showToast(`Could not update ACP provider: ${error.message || error}`, "error");
          }
        });
        head.append(mark, identity, beta ?? "", dot);
        const description = document.createElement("p"); description.textContent = provider.manifest.description;
        const footer = el("div", "provider-card-footer");
        const statusText = provider.status === "configured" ? "● Configured" : provider.status === "disabled" ? "● Disabled" : `● ${isUnverifiedProvider ? "Unverified" : "Not configured"}`;
        const status = el("span", `provider-status${provider.status === "configured" ? " configured" : ""}`, statusText);
        const action = el("button", "mini-btn provider-card-action", "Configure");
        action.type = "button";
        action.addEventListener("click", () => showAcpProviderEditor(provider));
        footer.append(status, action);
        card.append(head, description, footer);
        registry.appendChild(card);
      }
    } catch (error) {
      showToast(`Could not load ACP providers: ${error.message || error}`, "error");
    }
  };

  const renderAgentModelPicker = () => {
    const list = $("#agent-model-list");
    list.textContent = "";
    if (agentConversationController?.conversation?.kind === "acp") {
      renderAcpConfigPicker(list);
      return;
    }
    const selected = activeModel();
    $("#agent-model-trigger-label").textContent = `${selected?.id || "Choose model"} · ${aiSettings.effort || "auto"}`;
    if (selected) agentConversationController?.updateContextStatus();
    else $("#agent-provider-status").textContent = "Choose a model";
    const models = searchModels(aiSettings.models, $("#agent-model-search").value);
    if (models.length === 0) {
      list.appendChild(el("div", "agent-model-empty", aiSettings.models.length ? "No models match this search." : "No imported models. Open a provider and import its catalog."));
      return;
    }
    models.forEach((model) => {
      const row = el("div", `agent-model-row${model.key === aiSettings.activeModelKey ? " is-selected" : ""}`);
      const choose = el("button", "agent-model-choice"); choose.type = "button"; choose.setAttribute("role", "option");
      const name = el("span", "agent-model-name"); name.textContent = model.label || model.id;
      const meta = el("span", "agent-model-meta");
      const provider = el("span", "agent-model-provider"); provider.textContent = model.providerName;
      meta.appendChild(provider);
      modelCompatibility(model).forEach((capability) => { const badge = el("span", "agent-model-capability"); badge.textContent = capability; meta.appendChild(badge); });
      choose.append(name, meta);
      choose.addEventListener("click", () => void selectAgentModel(model.key, clampModelEffort(model, aiSettings.effort)));
      row.appendChild(choose);
      if (model.supportedEfforts.length > 0) {
        const effortRow = el("div", "agent-model-efforts");
        ["auto", ...model.supportedEfforts.filter((effort) => effort !== "auto")].forEach((effort) => {
          const button = el("button", `agent-effort-option${model.key === aiSettings.activeModelKey && effort === aiSettings.effort ? " is-selected" : ""}`, effort);
          button.type = "button";
          button.addEventListener("click", () => void selectAgentModel(model.key, effort));
          effortRow.appendChild(button);
        });
        row.appendChild(effortRow);
      }
      list.appendChild(row);
    });
  };

  const renderAcpConfigPicker = (list) => {
    const options = agentConversationController?.acpConfigOptions ?? [];
    if (options.length === 0) {
      list.appendChild(el("div", "agent-model-empty", "No ACP config options available yet. Start a turn to load the session."));
      return;
    }
    const query = ($("#agent-model-search").value || "").toLowerCase();
    options.forEach((opt) => {
      if (opt.type !== "select" || !opt.options) return;
      const section = el("div", "agent-model-section");
      const header = el("div", "agent-model-section-title", opt.name);
      if (opt.description) header.title = opt.description;
      section.appendChild(header);
      const filtered = opt.options.filter((o) => !query || o.name.toLowerCase().includes(query) || o.value.toLowerCase().includes(query));
      filtered.forEach((o) => {
        const isCurrent = String(opt.currentValue) === o.value;
        const row = el(`div`, `agent-model-row${isCurrent ? " is-selected" : ""}`);
        const choose = el("button", "agent-model-choice"); choose.type = "button"; choose.setAttribute("role", "option");
        const name = el("span", "agent-model-name"); name.textContent = o.name;
        const meta = el("span", "agent-model-meta");
        const tag = el("span", "agent-model-provider"); tag.textContent = opt.name;
        meta.appendChild(tag);
        if (o.description) { const desc = el("span", "agent-model-capability"); desc.textContent = o.description; meta.appendChild(desc); }
        choose.append(name, meta);
        choose.addEventListener("click", () => {
          void agentConversationController?.selectAcpConfigOption(opt.id, o.value);
          $("#agent-model-menu").hidden = true;
          $("#agent-model-trigger").setAttribute("aria-expanded", "false");
        });
        row.appendChild(choose);
        section.appendChild(row);
      });
      list.appendChild(section);
    });
  };

  const syncAiControls = () => {
    renderProviderCards();
    void renderAcpProviderCards();
    renderAgentModelPicker();
    if (currentProviderDetailId) renderProviderDetail();
    $("#settings-ai-strategy").value = aiSettings.strategy || "failover";
    $("#settings-ai-budget").value = aiSettings.totalAttemptBudget || 4;
    $("#settings-ai-stream").checked = aiSettings.stream !== false;
    $("#settings-ai-vision").value = aiSettings.vision || "auto";
    $("#settings-ai-user-prompt").value = aiSettings.userPrompt || "";
    $("#settings-ai-max-tool-rounds").value = aiSettings.maxToolRounds ?? 50;
    $("#settings-ai-max-repeated-tool-calls").value = aiSettings.maxRepeatedToolCalls ?? 50;
    $("#settings-ai-compaction").checked = aiSettings.compactionEnabled !== false;
    $("#settings-ai-max-input-tokens").value = aiSettings.maxInputTokens ?? 12000;
    $("#settings-ai-reserve-tokens").value = aiSettings.reserveTokens ?? 3000;
    $("#settings-ai-recent-turns").value = aiSettings.recentTurns ?? 4;
    $("#settings-ai-summary-max-chars").value = aiSettings.summaryMaxChars ?? 12000;
  };

  const selectAgentModel = async (modelKey, effort) => {
    try {
      aiSettings = await window.shell.aiProviders.select({ modelKey, effort });
      syncAiControls();
      $("#agent-model-menu").hidden = true;
      $("#agent-model-trigger").setAttribute("aria-expanded", "false");
    } catch (error) {
      showToast(`Could not select model: ${error.message || error}`, "error");
    }
  };

  const openProviderEditor = (presetId, existingId = "") => {
    const preset = providerPresets[presetId] || providerPresets.custom;
    const existing = configuredProvider(existingId || preset.id);
    const custom = presetId === "custom" || (!preset && existing);
    $("#provider-modal-overlay").hidden = false;
    $("#ai-settings-form").hidden = false;
    $("#ai-settings-title").textContent = `${existing ? "Edit" : "Configure"} ${existing?.name || preset.label}`;
    $("#ai-settings-subtitle").textContent = preset.detail;
    $("#provider-custom-fields").hidden = !custom;
    $("#settings-ai-preset-id").value = presetId;
    const providerType = existing?.type || preset.type || "openai-compatible";
    $("#settings-ai-provider-type").value = providerType;
    $("#settings-ai-name").value = existing?.name || (custom ? "" : preset.label);
    $("#settings-ai-id").value = existing?.id || preset.id;
    $("#settings-ai-id").readOnly = Boolean(existing);
    const selectedApi = existing?.api || preset.api;
    const apiSelect = $("#settings-ai-api");
    const apiModes = providerApiModes(providerType);
    if (selectedApi && !apiModes.some((mode) => mode.value === selectedApi)) {
      apiModes.push({
        value: selectedApi,
        label: selectedApi === "messages" ? "Anthropic Messages" : selectedApi,
      });
    }
    apiSelect.replaceChildren(...apiModes.map((mode) => {
      const option = document.createElement("option");
      option.value = mode.value;
      option.textContent = mode.label;
      return option;
    }));
    apiSelect.value = selectedApi;
    $("#settings-ai-base-url").value = existing?.baseUrl || preset.baseUrl;
    $("#settings-ai-model").value = existing?.defaultModel || "";
    $("#settings-ai-api-key").value = "";
    $("#settings-ai-api-key").placeholder = existing?.hasApiKey ? "Leave blank to keep saved key" : (preset.apiKeyOptional ? "Optional for this local gateway" : "Required");
    $("#settings-ai-enabled").checked = existing?.enabled ?? true;
    $("#settings-ai-timeout").value = Math.round((existing?.timeoutMs ?? 60000) / 1000);
    $("#settings-ai-attempts").value = existing?.maxAttempts ?? 1;
    $("#settings-ai-weight").value = existing?.weight ?? 1;
    $("#settings-ai-key-state").textContent = existing?.hasApiKey ? "Secure API key saved" : (preset.apiKeyOptional ? "API key optional" : "No API key saved");
    (custom ? $("#settings-ai-name") : $("#settings-ai-base-url")).focus();
  };

  const showProviderDetail = (providerId) => {
    currentProviderDetailId = providerId;
    renderProviderDetail();
    switchView("provider-details");
  };

  const renderProviderDetail = () => {
    const provider = configuredProvider(currentProviderDetailId);
    if (!provider) { switchView("ai-providers"); return; }
    $("#provider-detail-title").textContent = provider.name;
    $("#provider-detail-subtitle").textContent = `${provider.id} · ${provider.api}`;
    $("#provider-detail-base-url").textContent = provider.baseUrl || "Local";
    $("#provider-detail-key").textContent = provider.hasApiKey ? "•••••••• saved securely" : (provider.apiKeyOptional ? "Optional" : "Not configured");
    $("#provider-detail-default-model").textContent = provider.defaultModel || "Not set — choose per turn";
    $("#provider-detail-status").textContent = provider.enabled ? "Enabled" : "Disabled";
    $("#provider-import-models").disabled = false;
    $("#provider-detail-edit").disabled = false;
    $("#provider-detail-delete").disabled = false;
    $("#provider-add-model").disabled = false;
    const query = $("#provider-model-search").value.trim().toLowerCase();
    const models = provider.models.filter((model) => !query || `${model.id} ${model.label} ${model.description || ""}`.toLowerCase().includes(query));
    $("#provider-model-count").textContent = `${provider.models.length} model${provider.models.length === 1 ? "" : "s"}`;
    const list = $("#provider-model-list");
    list.textContent = "";
    if (models.length === 0) {
      list.appendChild(el("div", "provider-model-empty", provider.models.length ? "No models match this search." : "No models yet. Import the provider catalog or add an ID manually."));
      return;
    }
    models.forEach((model) => {
      const row = el("div", "provider-model-item");
      const top = el("div", "provider-model-item-head");
      const identity = el("div");
      const id = el("code", "provider-model-id"); id.textContent = model.id;
      const label = el("span", "provider-model-label"); label.textContent = model.label !== model.id ? model.label : "";
      identity.append(id, label);
      const badges = el("div", "provider-model-badges");
      if (model.contextWindow) { const badge = el("span", "model-badge model-badge-context"); badge.textContent = `${formatTokenCount(model.contextWindow)} ctx`; badges.appendChild(badge); }
      model.inputModes.forEach((mode) => { const badge = el("span", "model-badge model-badge-input"); badge.textContent = mode; badges.appendChild(badge); });
      modelCompatibility(model).filter((capability) => !model.inputModes.includes(capability)).forEach((capability) => { const badge = el("span", "model-badge"); badge.textContent = capability; badges.appendChild(badge); });
      top.append(identity, badges);
      row.appendChild(top);
      if (model.description) { const description = el("p", "provider-model-description"); description.textContent = model.description; row.appendChild(description); }
      list.appendChild(row);
    });
  };

  if (!window.shell?.aiProviders) {
    showToast("AI provider bridge is unavailable. Restart NusaShell after rebuilding the preload.", "error");
  } else {
    window.shell.aiProviders.list().then((settings) => { aiSettings = settings; syncAiControls(); }).catch((error) => showToast(`Could not load AI providers: ${error.message || error}`, "error"));
  }

  $("#ai-settings-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const preset = providerPresets[$("#settings-ai-preset-id").value] || providerPresets.custom;
    const input = {
      id: $("#settings-ai-id").value.trim() || preset.id,
      name: $("#settings-ai-name").value.trim() || preset.label,
      type: $("#settings-ai-provider-type").value,
      api: $("#settings-ai-api").value || preset.api,
      baseUrl: $("#settings-ai-base-url").value.trim(),
      apiKey: $("#settings-ai-api-key").value,
      apiKeyOptional: preset.apiKeyOptional,
      enabled: $("#settings-ai-enabled").checked,
      defaultModel: $("#settings-ai-model").value.trim(),
      timeoutMs: Number($("#settings-ai-timeout").value) * 1000,
      maxAttempts: Number($("#settings-ai-attempts").value),
      weight: Number($("#settings-ai-weight").value),
    };
    try {
      aiSettings = await window.shell.aiProviders.save(input);
      const savedProvider = configuredProvider(
        input.id.toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, ""),
      );
      $("#settings-ai-api-key").value = "";
      closeProviderEditor();
      syncAiControls();
      showProviderDetail(savedProvider?.id || aiSettings.activeProviderId);
      showToast("AI provider saved.", "success");
    } catch (error) {
      showToast(`Could not save provider: ${error.message || error}`, "error");
    }
  });
  $("#ai-runtime-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      aiSettings = await window.shell.aiProviders.updateRuntime({
        strategy: $("#settings-ai-strategy").value,
        totalAttemptBudget: Number($("#settings-ai-budget").value),
        stream: $("#settings-ai-stream").checked,
        vision: $("#settings-ai-vision").value,
        userPrompt: $("#settings-ai-user-prompt").value,
      });
      syncAiControls();
      showToast("Agent runtime saved.", "success");
    } catch (error) {
      showToast(`Could not save agent runtime: ${error.message || error}`, "error");
    }
  });
  wireAppBehaviorToggle("#settings-launch-at-login", "launchAtLogin");
  wireAppBehaviorToggle("#settings-start-hidden", "startHidden");
  wireAppBehaviorToggle("#settings-keep-in-background", "keepInBackground");
  void syncAppBehaviorControls();
  $("#ai-limits-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      aiSettings = await window.shell.aiProviders.updateRuntime({
        maxToolRounds: Number($("#settings-ai-max-tool-rounds").value),
        maxRepeatedToolCalls: Number($("#settings-ai-max-repeated-tool-calls").value),
      });
      syncAiControls();
      showToast("Agent limits saved.", "success");
    } catch (error) {
      showToast(`Could not save agent limits: ${error.message || error}`, "error");
    }
  });
  $("#ai-context-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      aiSettings = await window.shell.aiProviders.updateRuntime({
        compactionEnabled: $("#settings-ai-compaction").checked,
        maxInputTokens: Number($("#settings-ai-max-input-tokens").value),
        reserveTokens: Number($("#settings-ai-reserve-tokens").value),
        recentTurns: Number($("#settings-ai-recent-turns").value),
        summaryMaxChars: Number($("#settings-ai-summary-max-chars").value),
      });
      syncAiControls();
      showToast("Context settings saved.", "success");
    } catch (error) {
      showToast(`Could not save context settings: ${error.message || error}`, "error");
    }
  });
  $("#ai-settings-close").addEventListener("click", closeProviderEditor);
  $("#provider-modal-overlay").addEventListener("click", closeProviderEditor);
  $$("[data-provider-preset]").forEach((card) => card.querySelector(".provider-card-action")?.addEventListener("click", () => {
    const presetId = card.dataset.providerPreset;
    const provider = configuredProvider(providerPresets[presetId]?.id);
    if (provider) showProviderDetail(provider.id);
    else openProviderEditor(presetId);
  }));
  $("#add-custom-provider").addEventListener("click", () => openProviderEditor("custom"));
  $("#provider-details-back").addEventListener("click", () => switchView("ai-providers"));
  $("#provider-detail-edit").addEventListener("click", () => {
    const provider = configuredProvider(currentProviderDetailId);
    if (provider) openProviderEditor(builtInProviderIds.has(provider.id) ? provider.id : "custom", provider.id);
  });
  $("#provider-detail-delete").addEventListener("click", () => openProviderDeleteDialog(currentProviderDetailId));
  $("#provider-delete-close").addEventListener("click", closeProviderDeleteDialog);
  $("#provider-delete-cancel").addEventListener("click", closeProviderDeleteDialog);
  $("#provider-delete-overlay").addEventListener("click", closeProviderDeleteDialog);
  $("#provider-delete-confirm").addEventListener("click", async () => {
    const provider = configuredProvider(pendingProviderDeleteId);
    if (!provider) { closeProviderDeleteDialog(); return; }
    const button = $("#provider-delete-confirm");
    button.disabled = true;
    button.textContent = "Deleting…";
    try {
      aiSettings = await window.shell.aiProviders.delete(provider.id);
      const deletedDetail = currentProviderDetailId === provider.id;
      if (deletedDetail) currentProviderDetailId = "";
      closeProviderDeleteDialog();
      syncAiControls();
      if (deletedDetail) switchView("ai-providers");
      showToast(`${provider.name} deleted.`, "success");
    } catch (error) {
      button.disabled = false;
      button.textContent = "Delete";
      showToast(`Could not delete provider: ${error.message || error}`, "error");
    }
  });
  $("#provider-import-models").addEventListener("click", async () => {
    const button = $("#provider-import-models");
    const errorBox = $("#provider-import-error");
    errorBox.hidden = true;
    button.disabled = true;
    button.textContent = "Importing…";
    try {
      aiSettings = await window.shell.aiProviders.importModels(currentProviderDetailId);
      syncAiControls();
      showToast(`Imported ${configuredProvider(currentProviderDetailId)?.models.length || 0} models.`, "success");
    } catch (error) {
      errorBox.textContent = error.message || String(error);
      errorBox.hidden = false;
      showToast(`Could not import models: ${error.message || error}`, "error");
    } finally {
      button.disabled = false;
      button.textContent = "Import models";
    }
  });
  $("#provider-add-model").addEventListener("click", async () => {
    const modelId = window.prompt("Model ID");
    if (!modelId?.trim()) return;
    const label = window.prompt("Display label (optional)") || "";
    try {
      aiSettings = await window.shell.aiProviders.addModel(currentProviderDetailId, { id: modelId.trim(), label: label.trim() });
      syncAiControls();
      showToast("Model added.", "success");
    } catch (error) {
      showToast(`Could not add model: ${error.message || error}`, "error");
    }
  });
  $("#provider-model-search").addEventListener("input", renderProviderDetail);
  $("#agent-model-trigger").addEventListener("click", () => {
    const menu = $("#agent-model-menu");
    menu.hidden = !menu.hidden;
    $("#agent-model-trigger").setAttribute("aria-expanded", String(!menu.hidden));
    if (!menu.hidden) { renderAgentModelPicker(); $("#agent-model-search").focus(); }
  });
  $("#agent-model-search").addEventListener("input", renderAgentModelPicker);
  document.addEventListener("pointerdown", (event) => {
    if (!event.target.closest(".agent-model-control")) {
      $("#agent-model-menu").hidden = true;
      $("#agent-model-trigger").setAttribute("aria-expanded", "false");
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    if (!$("#ai-settings-form").hidden) closeProviderEditor();
    if (!$("#provider-delete-dialog").hidden) closeProviderDeleteDialog();
    if (!$("#agent-delete-dialog").hidden) agentConversationController?.closeDeleteDialog();
    $("#agent-model-menu").hidden = true;
    $("#agent-model-trigger").setAttribute("aria-expanded", "false");
  });

  // Log source filters
  $$("[data-log-source]").forEach(chip => chip.addEventListener("click", () => {
    $$("[data-log-source]").forEach(c => c.classList.remove("active"));
    chip.classList.add("active");
    logSourceFilter = chip.dataset.logSource;
    renderLogTail();
  }));

  // Drawer
  $("#drawer-close").addEventListener("click", closeDrawer);
  $("#drawer-overlay").addEventListener("click", closeDrawer);

  // Drawer actions
  $("#btn-start").addEventListener("click", () => { if (currentPlugin) startPlugin(currentPlugin.pluginId); });
  $("#btn-stop").addEventListener("click", () => { if (currentPlugin) stopPlugin(currentPlugin.pluginId); });
  $("#btn-restart").addEventListener("click", () => { if (currentPlugin) restartPlugin(currentPlugin.pluginId); });

  // Context menu
  document.addEventListener("click", hideContextMenu);
  document.addEventListener("contextmenu", (event) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) return;
    const editable = target.closest("input, textarea");
    if (!isEditableTextControl(editable)) return;
    event.preventDefault();
    showEditContextMenu(event.clientX, event.clientY, editable);
  });
  $$(".ctx-item").forEach(item => item.addEventListener("click", async (e) => {
    e.stopPropagation();
    const action = item.dataset.action;
    const id = $("#context-menu").dataset.pluginId;
    if (!id && ["cut", "copy", "paste"].includes(action)) {
      await runEditContextAction(action);
      hideContextMenu();
      return;
    }
    const p = plugins.find(pp => pp.pluginId === id);
    if (!p && action !== "uninstall") return;
    switch (action) {
      case "open": openPluginWindow(p); break;
      case "start": startPlugin(id); break;
      case "stop": stopPlugin(id); break;
      case "restart": restartPlugin(id); break;
      case "detail": openDrawer(p); break;
      case "uninstall": doUninstall(id, p?.name); break;
    }
    hideContextMenu();
  }));

  // Add Plugin modal
  $("#open-add-plugin").addEventListener("click", openAddPluginModal);
  $("#modal-close").addEventListener("click", closeAddPluginModal);
  $("#add-plugin-modal").addEventListener("click", (e) => {
    if (e.target === $("#add-plugin-modal")) closeAddPluginModal();
  });
  $("#install-url-btn").addEventListener("click", () => {
    doInstall("url", $("#install-url-input").value);
  });
  $("#install-local-btn").addEventListener("click", () => {
    doInstall("local", $("#install-local-input").value);
  });
  const pickPluginSource = async (kind) => {
    const controls = window.shell?.shellControls;
    if (!controls) {
      showInstallStatus("Native file picker is unavailable. Restart NusaShell after rebuilding.", true);
      return;
    }
    try {
      const path = await controls.pickPluginSource(kind);
      if (path) {
        $("#install-local-input").value = path;
        showInstallStatus(`${kind === "directory" ? "Folder" : "Archive"} selected. Ready to install.`, false);
      }
    } catch (error) {
      showInstallStatus(`Could not open picker: ${error.message || error}`, true);
    }
  };
  $("#pick-local-folder-btn").addEventListener("click", () => void pickPluginSource("directory"));
  $("#pick-local-archive-btn").addEventListener("click", () => void pickPluginSource("archive"));
  $("#install-url-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter") doInstall("url", e.target.value);
  });

  // Uninstall button in drawer
  $("#btn-uninstall").addEventListener("click", () => {
    if (currentPlugin) doUninstall(currentPlugin.pluginId, currentPlugin.name);
  });

  // Ping button
  $("#ping-btn").addEventListener("click", async () => {
    const result = $("#ping-result");
    result.style.display = "flex";
    const ping = await pingSystem();
    result.querySelector(".settings-value").textContent = ping?.error ? `Error: ${ping.error}` : "pong: true";
  });

  // Search
  const searchInput = $("#search-input");
  const searchClear = $("#search-clear");
  if (searchInput) {
    searchInput.addEventListener("input", () => {
      launcherSearchQuery = searchInput.value;
      searchClear.hidden = !launcherSearchQuery;
      renderAppGrid();
    });
    searchInput.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && launcherSearchQuery) {
        searchInput.value = "";
        searchInput.dispatchEvent(new Event("input"));
      }
    });
  }
  searchClear?.addEventListener("click", () => {
    searchInput.value = "";
    searchInput.dispatchEvent(new Event("input"));
    searchInput.focus();
  });

  $("#acp-provider-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const id = $("#acp-provider-id").value;
    const command = $("#acp-provider-command").value.trim() || undefined;
    const argsString = $("#acp-provider-args").value.trim();
    const args = argsString ? argsString.split(/\s+/).filter(Boolean) : undefined;
    try {
      await window.shell.acpProviders.save({ providerId: id, enabled: $("#acp-provider-enabled").checked, command, args });
      closeAcpProviderEditor();
      await renderAcpProviderCards();
      showToast("ACP provider saved.", "success");
    } catch (error) {
      showToast(`Could not save ACP provider: ${error.message || error}`, "error");
    }
  });
  $("#acp-provider-close").addEventListener("click", closeAcpProviderEditor);
  $("#acp-provider-modal-overlay").addEventListener("click", closeAcpProviderEditor);

  // Version
  getVersion().then(v => {
    if (v?.version) $("#settings-version").textContent = v.version;
  }).catch(() => {});

  // Event subscriptions
  onEvent("plugin.installed", (payload) => handlePluginEvent(payload, "plugin.installed"));
  onEvent("plugin.uninstalled", (payload) => handlePluginEvent(payload, "plugin.uninstalled"));
  onEvent("plugin.started", (payload) => handlePluginEvent(payload, "plugin.started"));
  onEvent("plugin.stopped", (payload) => handlePluginEvent(payload, "plugin.stopped"));
  onEvent("plugin.crashed", (payload) => handlePluginEvent(payload, "plugin.crashed"));
  onEvent("plugin.state_changed", (payload) => handlePluginEvent(payload, "plugin.state_changed"));
  onEvent("tool.call_completed", (payload) => handlePluginEvent(payload, "tool.call_completed"));

  onEvent("agent.learning_updated", (payload) => {
    const kinds = Array.isArray(payload?.kinds) ? payload.kinds.join(", ") : "unknown";
    const summary = payload?.summary ? `: ${payload.summary}` : "";
    showToast(`Background review updated ${kinds}${summary}`, "info");
    learningController?.refresh();
    skillsController?.refreshPending();
    skillsController?.refreshArchived();
  });

  // Auto-update
  initUpdater();

  void agentConversationController.initialize().catch((error) => {
    showToast(`Could not load conversations: ${error.message || error}`, "error");
  });
  void skillsController.initialize().catch((error) => {
    showToast(`Could not load skills: ${error.message || error}`, "error");
  });
  void learningController.initialize().catch((error) => {
    showToast(`Could not load learning graph: ${error.message || error}`, "error");
  });

  // Jobs: modal wiring + event subscriptions
  $("#jobs-new-btn")?.addEventListener("click", () => jobsController.openModal());
  $("#job-modal-close")?.addEventListener("click", () => jobsController.closeModal());
  $("#job-modal-cancel")?.addEventListener("click", () => jobsController.closeModal());
  $("#job-modal-save")?.addEventListener("click", () => jobsController.saveJob());
  $("#job-field-mode")?.addEventListener("change", () => jobsController._toggleModeFields());
  $("#job-field-schedule")?.addEventListener("blur", () => jobsController.validateSchedule());
  $("#job-output-close")?.addEventListener("click", () => { $("#job-output-modal").style.display = "none"; });
  onEvent("job.completed", (payload) => {
    showToast(`Job “${payload.name}” completed`, "success");
    if ($(".view[data-view='jobs']")?.classList.contains("active")) void jobsController.refresh();
  });
  onEvent("job.failed", (payload) => {
    showToast(`Job “${payload.name}” failed: ${payload.error}`, "error");
    if ($(".view[data-view='jobs']")?.classList.contains("active")) void jobsController.refresh();
  });

  // Connect and subscribe — pre-seed activeSubscriptions so onopen always subscribes
  activeSubscriptions.add("*");
  connectWs();

  // Periodic refresh (fallback for state sync)
  setInterval(() => { if (connected) refreshAll(); }, 5000);
});
