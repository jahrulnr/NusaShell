// NusaShell launcher renderer — connects to backend via WebSocket,
// renders plugin grid, and handles plugin lifecycle actions.
// Uses the native browser WebSocket (not the `ws` npm package).

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
          ctrl.reject(new Error(msg.error?.message ?? "Unknown error"));
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

// ============ View Switching ============

function switchView(viewName) {
  $$("[data-view]").forEach(v => {
    if (v.tagName === "SECTION") v.classList.toggle("active", v.dataset.view === viewName);
  });
  $$("[data-nav]").forEach(n => n.classList.toggle("active", n.dataset.view === viewName));
  closeDrawer();
  hideContextMenu();
}

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
