// NusaShell launcher renderer — connects to backend via WebSocket,
// renders plugin grid, and handles plugin lifecycle actions.
// Uses the native browser WebSocket (not the `ws` npm package).
import { clampModelEffort, formatTokenCount, modelCompatibility, searchModels } from "./ai-model-ui.js";
import { AgentConversationController } from "./agent-conversation-controller.js";

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
let aiSettings = { activeProviderId: "", activeModelKey: "", effort: "auto", providers: [], models: [] };
let currentProviderDetailId = "";
let pendingProviderDeleteId = "";

// ============ Helpers ============

function $(sel) { return document.querySelector(sel); }
function $$(sel) { return document.querySelectorAll(sel); }
function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html !== undefined) e.innerHTML = html;
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

  count.textContent = `${logEntries.length} / 1000`;
  tail.textContent = "";
  if (filtered.length === 0) {
    const empty = el("div", "log-empty", "No logs for this source yet.");
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

function isUrlIcon(icon) { return /^https?:\/\//i.test(icon); }
function isFileIcon(icon) { return /^(file:\/\/|\.?\/|\.\\|[A-Za-z]:[\\/])/i.test(icon); }

function renderIconHtml(icon, fontSize) {
  if (isUrlIcon(icon)) return `<img src="${icon}" alt="" style="width:${fontSize}px;height:${fontSize}px;object-fit:contain" />`;
  if (isFileIcon(icon)) return `<img src="${icon}" alt="" style="width:${fontSize}px;height:${fontSize}px;object-fit:contain" onerror="this.style.display='none';this.parentNode.textContent='📦'" />`;
  return `<span style="font-size:${fontSize}px">${icon}</span>`;
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
    fill.style.width = "100%";
    settingsDot.style.background = "var(--green)";
    settingsDot.style.boxShadow = "0 0 0 3px rgba(47,191,113,0.15)";
    settingsLabel.textContent = "Connected";
  } else {
    status.textContent = "Disconnected";
    fill.style.width = "0%";
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
  const dispose = options.onDelta
    ? onEvent("agent.text_delta", (payload) => {
      if (payload?.traceId === options.traceId && payload.delta) options.onDelta(payload.delta);
    })
    : () => {};
  try {
    return await sendRequest("agent.run", {
      messages,
      pluginIds: [],
      providerId: selected.providerId,
      model: selected.id,
      effort: aiSettings.effort,
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
      },
      ...(options.traceId ? { traceId: options.traceId } : {}),
    }, 300000);
  } finally {
    dispose();
  }
}

async function cancelAgentTurn(traceId) {
  return sendRequest("agent.cancel", { traceId });
}

// ============ View Switching ============

function switchView(viewName) {
  $$("[data-view]").forEach(v => {
    if (v.tagName === "SECTION") v.classList.toggle("active", v.dataset.view === viewName);
  });
  $$("[data-nav]").forEach(n => n.classList.toggle("active", n.dataset.view === viewName));
  closeDrawer();
  hideContextMenu();
  if (viewName === "agent") agentConversationController?.renderList();
  if (viewName === "autostart") renderAutostartList();
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
    icon.textContent = isUrlIcon(plugin.icon) || isFileIcon(plugin.icon) ? "◈" : (plugin.icon || "🧩");
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
    grid.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No plugins installed. Add a plugin folder to plugins/examples/.</div>';
    return;
  }
  plugins.forEach(p => {
    const cell = el("button", "app-cell");
    cell.dataset.pluginId = p.pluginId;
    cell.innerHTML = `<div class="app-icon bg-blue">${renderIconHtml(p.icon || "🧩", 28)}</div><div class="app-name">${p.name}</div><div class="app-status ${p.state}">${stateBadgeHtml(p.state)}</div>`;
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
    row.innerHTML = `<div class="plugin-row-icon bg-blue">${renderIconHtml(p.icon || "🧩", 18)}</div><div class="plugin-row-info"><div class="plugin-row-name">${p.name}</div><div class="plugin-row-meta">${p.pluginId} · v${p.version}</div></div><div class="plugin-row-state ${p.state}">${stateBadgeHtml(p.state) || "Idle"}</div>`;
    row.addEventListener("click", () => openDrawer(p));
    table.appendChild(row);
  });
}

// ============ Plugin Detail Drawer ============

async function openDrawer(plugin) {
  currentPlugin = plugin;
  $("#drawer-icon").className = "drawer-icon bg-blue";
  $("#drawer-icon").innerHTML = renderIconHtml(plugin.icon || "🧩", 20);
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
      startPlugin(plugin.pluginId).catch((e) => console.error("[openPluginWindow] startPlugin error:", e));
    }
    const installPath = plugin.installPath || "";
    if (window.shell?.openPlugin) {
      await window.shell.openPlugin(plugin.pluginId, plugin.name, plugin.icon || "🧩", installPath, "panel");
    }
  } catch (err) {
    console.error("[openPluginWindow] error:", err);
  }
}

// ============ Context Menu ============

function showContextMenu(x, y, plugin) {
  const menu = $("#context-menu");
  menu.style.display = "block";
  menu.style.left = `${x}px`;
  menu.style.top = `${y}px`;
  menu.dataset.pluginId = plugin.pluginId;
}

function hideContextMenu() { $("#context-menu").style.display = "none"; }

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
    showInstallStatus("Please enter a path.", true);
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

  const windowControls = window.shell?.windowControls;
  if (windowControls) {
    $("#window-minimize").addEventListener("click", () => windowControls.minimize());
    $("#window-maximize").addEventListener("click", () => windowControls.toggleMaximize());
    $("#window-close").addEventListener("click", () => windowControls.close());
  }

  // Display WS URL in settings
  $("#settings-ws-url").textContent = WS_URL;

  // Nav switching
  $$("[data-nav]").forEach(item => item.addEventListener("click", () => switchView(item.dataset.view)));
  $("#nav-settings-btn").addEventListener("click", () => switchView("settings"));

  const providerPresets = {
    openrouter: { id: "openrouter", type: "openrouter", label: "OpenRouter", baseUrl: "https://openrouter.ai/api/v1", api: "chat", detail: "API key · OpenAI-compatible chat", apiKeyOptional: false },
    omniroute: { id: "omniroute", type: "omniroute", label: "OmniRoute", baseUrl: "http://127.0.0.1:20128/v1", api: "responses", detail: "Local OpenAI-compatible Responses gateway", apiKeyOptional: true },
    "9router": { id: "9router", type: "9router", label: "9Router", baseUrl: "http://127.0.0.1:20128/v1", api: "chat", detail: "Local OpenAI-compatible gateway", apiKeyOptional: true },
    openai: { id: "openai", type: "openai", label: "OpenAI", baseUrl: "https://api.openai.com/v1", api: "responses", detail: "Official OpenAI Responses endpoint", apiKeyOptional: false },
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
    getActiveModel: activeModel,
    notify: showToast,
    log: writeRendererLog,
  });

  const renderProviderCards = () => {
    $$("[data-custom-provider-card]").forEach((card) => card.remove());
    $$("[data-provider-preset]").forEach((card) => {
      const preset = providerPresets[card.dataset.providerPreset];
      const provider = preset && configuredProvider(preset.id);
      const configured = provider && (provider.hasApiKey || provider.apiKeyOptional);
      const status = card.querySelector(".provider-status");
      const dot = card.querySelector(".provider-toggle");
      const action = card.querySelector(".provider-card-action");
      const footer = card.querySelector(".provider-card-footer");
      let actions = footer?.querySelector(".provider-card-actions");
      if (footer && action && !actions) {
        actions = el("div", "provider-card-actions");
        action.replaceWith(actions);
        actions.appendChild(action);
      }
      actions?.querySelector(".provider-card-delete")?.remove();
      status.textContent = provider ? (configured ? `● Configured · ${provider.models.length} models` : "● Needs API key") : "● Not configured";
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

  const renderAgentModelPicker = () => {
    const selected = activeModel();
    $("#agent-model-trigger-label").textContent = `${selected?.id || "Choose model"} · ${aiSettings.effort || "auto"}`;
    $("#agent-provider-status").textContent = selected ? `${selected.providerName} · ${selected.id}` : "Choose a model";
    const list = $("#agent-model-list");
    list.textContent = "";
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

  const syncAiControls = () => {
    renderProviderCards();
    renderAgentModelPicker();
    if (currentProviderDetailId) renderProviderDetail();
    $("#settings-ai-strategy").value = aiSettings.strategy || "failover";
    $("#settings-ai-budget").value = aiSettings.totalAttemptBudget || 4;
    $("#settings-ai-stream").checked = aiSettings.stream !== false;
    $("#settings-ai-vision").value = aiSettings.vision || "auto";
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
    $("#settings-ai-provider-type").value = existing?.type || preset.type || "openai-compatible";
    $("#settings-ai-name").value = existing?.name || (custom ? "" : preset.label);
    $("#settings-ai-id").value = existing?.id || preset.id;
    $("#settings-ai-id").readOnly = Boolean(existing);
    $("#settings-ai-api").value = existing?.api || preset.api;
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
      });
      syncAiControls();
      showToast("Agent runtime saved.", "success");
    } catch (error) {
      showToast(`Could not save agent runtime: ${error.message || error}`, "error");
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
  $$(".ctx-item").forEach(item => item.addEventListener("click", (e) => {
    e.stopPropagation();
    const action = item.dataset.action;
    const id = $("#context-menu").dataset.pluginId;
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
  $("#install-url-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter") doInstall("url", e.target.value);
  });
  $("#install-local-input").addEventListener("keydown", (e) => {
    if (e.key === "Enter") doInstall("local", e.target.value);
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
  if (searchInput) {
    searchInput.addEventListener("input", () => {
      const q = searchInput.value.toLowerCase();
      $$("#app-grid .app-cell").forEach(cell => {
        const name = cell.querySelector(".app-name")?.textContent.toLowerCase() || "";
        cell.style.display = !q || name.includes(q) ? "" : "none";
      });
    });
  }

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

  // Auto-update
  initUpdater();

  void agentConversationController.initialize().catch((error) => {
    showToast(`Could not load conversations: ${error.message || error}`, "error");
  });

  // Connect and subscribe
  connectWs();
  // Subscribe after connect (handled in ws.onopen)
  setTimeout(async () => {
    if (connected) {
      try { await subscribe(["*"]); } catch {}
    }
  }, 500);

  // Periodic refresh (fallback for state sync)
  setInterval(() => { if (connected) refreshAll(); }, 5000);
});
