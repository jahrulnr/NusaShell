// NusaShell launcher renderer — connects to backend via WebSocket,
// renders plugin grid, and handles plugin lifecycle actions.
// Uses the native browser WebSocket (not the `ws` npm package).
import { clampModelEffort, formatTokenCount, modelCompatibility, searchModels } from "./ai-model-ui.js";
import { AgentConversationController } from "./agent-conversation-controller.js";
import { SkillsController } from "./skills-controller.js";
import { LearningController } from "./learning-controller.js";
import { JobsController } from "./jobs-controller.js";
import {
  applyTextEdit,
  countLogsBySource,
  describeToolsPanel,
  filterLauncherPlugins,
  hasPluginUi,
  launcherGridNeedsRebuild,
  normalizeTransparentIcon,
  pluginIconPresentation,
  positionContextMenu,
  providerApiModes,
} from "./launcher-ui.js";
import { initWsClient, connectWs, sendRequest, onEvent, subscribe, isConnected } from "./ws-client.js";
import { fetchPlugins, startPlugin, stopPlugin, restartPlugin, getPluginDetail, listTools, callTool, pingSystem, getVersion, installPlugin, uninstallPlugin, setPluginAutostart } from "./plugin-api.js";
import { runAgentTurn, cancelAgentTurn, answerAskQuestion } from "./agent-api.js";
import { runAcpTurn, cancelAcpTurn, getAcpSessionInfo, setAcpConfigOption, ensureAcpSession, answerAcpPermission, answerAcpAsk } from "./acp-api.js";

const WS_URL = window.shell?.wsUrl ?? "ws://127.0.0.1:9130";

// ============ State ============

let plugins = [];
let currentPlugin = null;
let selectedPluginId = "";
let nativeMcpEditId = "";
let logSourceFilter = "all";
const logEntries = [];
const STATES = ["idle", "starting", "running", "stopping", "crashed"];
let agentConversationController = null;
let skillsController = null;
let learningController = null;
let jobsController = null;
let launcherSearchQuery = "";
let launcherCategory = "All";
let aiSettings = { activeProviderId: "", activeModelKey: "", effort: "auto", providers: [], models: [] };
let currentProviderDetailId = "";
let pendingProviderDeleteId = "";
let editContextTarget = null;
let drawerReturnFocus = null;

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
  if (viewName === "jobs") void jobsController?.refresh();
  if (viewName === "settings") void syncAppBehaviorControls();
}

async function syncAppBehaviorControls() {
  const launchAtLogin = $("#settings-launch-at-login");
  const startHidden = $("#settings-start-hidden");
  const keepInBackground = $("#settings-keep-in-background");
  const canvasEnabled = $("#settings-canvas-enabled");
  const help = $("#settings-launch-at-login-help");
  if (!launchAtLogin || !startHidden || !keepInBackground || !window.shell?.appBehavior) return;
  try {
    const settings = await window.shell.appBehavior.get();
    launchAtLogin.checked = Boolean(settings.launchAtLogin);
    startHidden.checked = Boolean(settings.startHidden);
    keepInBackground.checked = Boolean(settings.keepInBackground);
    if (canvasEnabled) canvasEnabled.checked = settings.canvasEnabled !== false;
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
      const canvasEnabled = $("#settings-canvas-enabled");
      if (canvasEnabled) canvasEnabled.checked = settings.canvasEnabled !== false;
      if (key === "canvasEnabled") {
        agentConversationController?.setCanvasEnabled(settings.canvasEnabled !== false);
      }
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
  const uiPlugins = plugins.filter(hasPluginUi);
  if (plugins.length === 0) {
    grid.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No plugins installed. Add a plugin folder to plugins/.</div>';
    return;
  }
  if (uiPlugins.length === 0) {
    grid.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No apps to launch. Installed plugins are MCP-only — manage them from the Plugins view.</div>';
    return;
  }
  const categories = ["All", ...new Set(uiPlugins.map((p) => p.category || "Uncategorized"))];
  const tabsContainer = $("#launcher-tabs");
  if (tabsContainer) {
    tabsContainer.innerHTML = "";
    categories.forEach((cat) => {
      const tab = el("button", `tab${cat === launcherCategory ? " active" : ""}`);
      tab.textContent = cat;
      tab.dataset.category = cat;
      tab.addEventListener("click", () => {
        launcherCategory = cat;
        renderAppGrid();
      });
      tabsContainer.appendChild(tab);
    });
  }

  const categoryFiltered = launcherCategory === "All"
    ? uiPlugins
    : uiPlugins.filter((p) => (p.category || "Uncategorized") === launcherCategory);
  const visiblePlugins = filterLauncherPlugins(categoryFiltered, launcherSearchQuery);
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
    cell.addEventListener("click", () => {
      const plugin = plugins.find((item) => item.pluginId === p.pluginId);
      if (plugin) openPluginWindow(plugin);
    });
    cell.addEventListener("contextmenu", (e) => {
      e.preventDefault();
      const plugin = plugins.find((item) => item.pluginId === p.pluginId);
      if (plugin) showContextMenu(e.clientX, e.clientY, plugin);
    });
    grid.appendChild(cell);
  });
}

function updateAppGridStates() {
  const pluginsById = new Map(plugins.map((plugin) => [plugin.pluginId, plugin]));
  $$("#app-grid .app-cell[data-plugin-id]").forEach((cell) => {
    const plugin = pluginsById.get(cell.dataset.pluginId);
    const status = cell.querySelector(".app-status");
    if (!plugin || !status) return;
    status.className = `app-status ${plugin.state}`;
    status.innerHTML = stateBadgeHtml(plugin.state);
  });
}

function syncAppGrid(previousPlugins) {
  if (launcherGridNeedsRebuild(previousPlugins, plugins)) renderAppGrid();
  else updateAppGridStates();
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
    const row = el("button", "plugin-row");
    row.type = "button";
    row.dataset.pluginId = p.pluginId;
    const icon = el("div", "plugin-row-icon");
    setPluginIcon(icon, p.icon || "🧩", 38, p.installPath);
    const info = el("div", "plugin-row-info");
    const name = el("div", "plugin-row-name");
    name.textContent = p.name;
    const meta = el("div", "plugin-row-meta");
    meta.textContent = `${p.pluginId} · v${p.version}${p.source === "native-mcp" ? " · Native MCP" : ""}`;
    info.append(name, meta);
    const state = el("div", `plugin-row-state ${p.state}`);
    state.innerHTML = stateBadgeHtml(p.state) || "Idle";
    row.append(icon, info, state);
    row.classList.toggle("is-selected", p.pluginId === selectedPluginId);
    row.addEventListener("click", () => {
      selectedPluginId = p.pluginId;
      renderInstalledTable();
      openDrawer(p);
    });
    table.appendChild(row);
  });
}

// ============ Plugin Detail Drawer ============

async function openDrawer(plugin) {
  drawerReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  currentPlugin = plugin;
  $("#drawer-icon").className = "drawer-icon";
  setPluginIcon($("#drawer-icon"), plugin.icon || "🧩", 38, plugin.installPath);
  $("#drawer-title").textContent = plugin.name;
  $("#drawer-subtitle").textContent = `${plugin.pluginId} · v${plugin.version}`;

  $("#btn-edit-mcp").hidden = plugin.source !== "native-mcp";

  const sm = $("#state-machine");
  sm.innerHTML = "";
  STATES.forEach((s, i) => {
    if (i > 0) sm.appendChild(el("span", "state-arrow", "→"));
    sm.appendChild(el("span", `state-node${s === plugin.state ? " current" : ""}`, s));
  });

  const toolsResult = await listTools(plugin.pluginId);
  const panel = describeToolsPanel(toolsResult, plugin);
  $("#tool-count").textContent = panel.count;
  const tl = $("#tools-list");
  tl.innerHTML = "";
  if (panel.status === "ready") {
    panel.tools.forEach(t => {
      const item = el("div", "tool-item");
      item.innerHTML = `<div class="tool-item-info"><div class="tool-item-name">${t.name}</div><div class="tool-item-desc">${t.description || ""}</div></div>`;
      tl.appendChild(item);
    });
  } else {
    const cls = panel.status === "unavailable" ? "tools-unavailable" : "tools-empty";
    tl.innerHTML = `<div class="${cls}">${panel.message}</div>`;
  }

  const detail = await getPluginDetail(plugin.pluginId);
  const m = detail || plugin;
  $("#manifest-info").innerHTML = `<div class="manifest-row"><span class="manifest-key">id</span><span class="manifest-val">${plugin.pluginId}</span></div><div class="manifest-row"><span class="manifest-key">version</span><span class="manifest-val">${plugin.version}</span></div><div class="manifest-row"><span class="manifest-key">state</span><span class="manifest-val">${plugin.state}</span></div><div class="manifest-row"><span class="manifest-key">enabled</span><span class="manifest-val">${plugin.enabled}</span></div>`;

  const drawer = $("#plugin-drawer");
  const overlay = $("#drawer-overlay");
  drawer.hidden = false;
  drawer.inert = false;
  drawer.setAttribute("aria-hidden", "false");
  overlay.setAttribute("aria-hidden", "false");
  drawer.classList.add("active");
  overlay.classList.add("active");
  $("#drawer-close").focus();
}

function closeDrawer() {
  const drawer = $("#plugin-drawer");
  const overlay = $("#drawer-overlay");
  const wasOpen = drawer.classList.contains("active");
  drawer.classList.remove("active");
  overlay.classList.remove("active");
  drawer.setAttribute("aria-hidden", "true");
  overlay.setAttribute("aria-hidden", "true");
  drawer.inert = true;
  drawer.hidden = true;
  currentPlugin = null;
  if (wasOpen && drawerReturnFocus?.isConnected) drawerReturnFocus.focus();
  drawerReturnFocus = null;
}

// ============ Plugin Window (opens in separate BrowserWindow via IPC) ============

async function openPluginWindow(plugin) {
  try {
    if (!hasPluginUi(plugin)) {
      console.warn("[openPluginWindow] plugin has no UI; ignoring open request", plugin.pluginId);
      return;
    }
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
  const canOpen = hasPluginUi(plugin);
  $$("#context-menu .ctx-item").forEach((item) => {
    item.disabled = item.dataset.action === "open" ? !canOpen : false;
  });
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
  const previousPlugins = plugins;
  const idx = plugins.findIndex(p => p.pluginId === payload.pluginId);
  if (idx >= 0) {
    const newState = payload.state ?? payload.newState;
    if (newState) {
      plugins[idx] = { ...plugins[idx], state: newState };
    }
  }
  if (eventType === "plugin.installed" || eventType === "plugin.uninstalled") {
    void refreshAll();
    return;
  }
  syncAppGrid(previousPlugins);
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

async function openAddPluginModal(tab = "custom", plugin = null) {
  $("#add-plugin-modal").style.display = "flex";
  $("#install-url-input").value = "";
  $("#install-local-input").value = "";
  $("#install-status").style.display = "none";
  nativeMcpEditId = plugin?.source === "native-mcp" ? plugin.pluginId : "";
  $("#add-plugin-modal-title").textContent = nativeMcpEditId ? "Edit MCP" : "Add MCP";
  $("#native-mcp-name").value = plugin?.name || "";
  $("#native-mcp-id").value = plugin?.pluginId || "";
  $("#native-mcp-id").disabled = Boolean(nativeMcpEditId);
  $("#native-mcp-category").value = plugin?.category || "";
  $("#native-mcp-transport").value = plugin?.transport || "stdio";
  toggleMcpTransportFields($("#native-mcp-transport").value);

  // Fetch full manifest detail for edit (command, args, url, env, headers)
  let detail = null;
  if (nativeMcpEditId) {
    detail = await getPluginDetail(nativeMcpEditId);
  }
  const m = detail || plugin || {};
  if (m.category) $("#native-mcp-category").value = m.category;
  $("#native-mcp-command").value = m.command || "";
  $("#native-mcp-args").value = Array.isArray(m.args) ? m.args.join("\n") : "";
  $("#native-mcp-url").value = m.url || "";
  $("#native-mcp-env").value = JSON.stringify(m.env || {}, null, 2);
  $("#native-mcp-headers").value = JSON.stringify(m.headers || {}, null, 2);

  setPluginModalTab(tab);
  if (tab === "custom") $("#native-mcp-name").focus();
  else $("#install-url-input").focus();
}

function toggleMcpTransportFields(transport) {
  const isStdio = transport === "stdio";
  $("#native-mcp-stdio-fields").hidden = !isStdio;
  $("#native-mcp-remote-fields").hidden = isStdio;
}

function setPluginModalTab(tab) {
  const custom = tab === "custom";
  $("#custom-mcp-panel").hidden = !custom;
  $("#nusashell-plugin-panel").hidden = custom;
  $("#custom-mcp-tab").classList.toggle("active", custom);
  $("#nusashell-plugin-tab").classList.toggle("active", !custom);
  $("#custom-mcp-tab").setAttribute("aria-selected", String(custom));
  $("#nusashell-plugin-tab").setAttribute("aria-selected", String(!custom));
}

function parseNativeJson(value) {
  const trimmed = value.trim();
  if (!trimmed) throw new Error("Paste a JSON config first");
  let parsed;
  try { parsed = JSON.parse(trimmed); }
  catch { throw new Error("Invalid JSON — check the format"); }
  const server = parsed.mcpServers ? Object.entries(parsed.mcpServers)[0] : [parsed.name || "", parsed];
  if (!server || typeof server[1] !== "object") throw new Error("JSON must contain one MCP server");
  const [name, config] = server;
  $("#native-mcp-name").value = config.name || name || "";
  $("#native-mcp-id").value = config.id || `custom.${String(name || "mcp").toLowerCase().replace(/[^a-z0-9-]+/g, "-")}`;
  $("#native-mcp-category").value = config.category || "";
  $("#native-mcp-transport").value = config.url ? (config.transport === "sse" ? "sse" : "http") : "stdio";
  toggleMcpTransportFields($("#native-mcp-transport").value);
  $("#native-mcp-command").value = config.command || "";
  $("#native-mcp-args").value = Array.isArray(config.args) ? config.args.join("\n") : "";
  $("#native-mcp-url").value = config.url || "";
  $("#native-mcp-env").value = JSON.stringify(config.env || {}, null, 2);
  $("#native-mcp-headers").value = JSON.stringify(config.headers || {}, null, 2);
}

async function saveNativeMcp() {
  try {
    const input = {
      id: $("#native-mcp-id").value.trim(),
      name: $("#native-mcp-name").value.trim(),
      category: $("#native-mcp-category").value.trim() || undefined,
      transport: $("#native-mcp-transport").value,
      command: $("#native-mcp-command").value.trim() || undefined,
      args: $("#native-mcp-args").value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean),
      url: $("#native-mcp-url").value.trim() || undefined,
      env: JSON.parse($("#native-mcp-env").value || "{}"),
      headers: JSON.parse($("#native-mcp-headers").value || "{}"),
    };
    $("#native-mcp-status").style.display = "block";
    $("#native-mcp-status").textContent = "Saving MCP...";
    const result = nativeMcpEditId
      ? await window.shell.plugins.updateNativeMcp(nativeMcpEditId, input)
      : await window.shell.plugins.registerNativeMcp(input);
    if (result?.error) throw new Error(result.error);
    showToast(nativeMcpEditId ? "MCP updated" : "MCP added", "success");
    closeAddPluginModal();
    await refreshAll();
    if (result?.restartRequired) {
      showToast("Restarting MCP...", "info");
      startPlugin(result.pluginId);
    }
  } catch (error) {
    $("#native-mcp-status").style.display = "block";
    $("#native-mcp-status").textContent = `Error: ${error.message || error}`;
    $("#native-mcp-status").className = "modal-status modal-status-error";
  }
}

function closeAddPluginModal() {
  $("#add-plugin-modal").style.display = "none";
  $("#native-mcp-id").disabled = false;
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
  const previousPlugins = plugins;
  plugins = await fetchPlugins();
  if (selectedPluginId && !plugins.some((plugin) => plugin.pluginId === selectedPluginId)) selectedPluginId = "";
  syncAppGrid(previousPlugins);
  renderInstalledTable();
  renderAutostartList();
}

// ============ Init ============

document.addEventListener("DOMContentLoaded", () => {
  const buildLabel = $("#settings-build");
  if (buildLabel) buildLabel.textContent = window.shell?.build ?? "production";
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
    const authSelect = $("#acp-provider-auth-select");
    const authHint = $("#acp-provider-auth-hint");
    authSelect.textContent = "";
    const defaultOption = el("option", "", "Default / file auth");
    defaultOption.value = "";
    authSelect.appendChild(defaultOption);
    const authIds = provider.manifest.authMethodIds ?? (provider.manifest.authMethodId ? [provider.manifest.authMethodId] : []);
    for (const id of authIds) {
      const opt = el("option", "", id);
      opt.value = id;
      authSelect.appendChild(opt);
    }
    authSelect.value = provider.config.authMethodId ?? provider.manifest.authMethodId ?? "";
    if (provider.manifest.id === "codex") {
      authHint.textContent = "ChatGPT login: run `codex login` then click Connect. API key: set OPENAI_API_KEY or CODEX_API_KEY in the process env that launches Electron, then choose api-key. Default command is npx -y @agentclientprotocol/codex-acp; install globally and set NUSASHELL_CODEX_ACP_BIN=codex-acp to skip the npx download.";
    } else if (provider.manifest.authMethodId) {
      authHint.textContent = `Auth method: ${provider.manifest.authMethodId}. Click Connect after enabling.`;
    } else {
      authHint.textContent = "";
    }
    $("#acp-provider-auth-method").textContent = provider.config.authStatus === "connected" ? "● Connected" : provider.config.authStatus === "needs-auth" ? "● Needs auth" : "Not probed";
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
    runTurn: (messages, options) => runAgentTurn(messages, { ...options, onLog: writeRendererLog }, aiSettings),
    cancelTurn: cancelAgentTurn,
    answerAsk: answerAskQuestion,
    getActiveModel: activeModel,
    getVisionMode: () => aiSettings.vision,
    notify: showToast,
    log: writeRendererLog,
    runAcpTurn: (prompt, options) => runAcpTurn(prompt, { ...options, onLog: writeRendererLog }),
    cancelAcpTurn,
    answerAcpPermission,
    answerAcpAsk,
    getAcpSessionInfo,
    setAcpConfigOption,
    ensureAcpSession,
    refreshModelPicker: () => renderAgentModelPicker(),
  });
  skillsController = new SkillsController({
    shell: window.shell,
    notify: showToast,
    log: writeRendererLog,
  });
  learningController = new LearningController(window.shell);
  jobsController = new JobsController({ notify: showToast });
  jobsController.initialize();

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
        const isConnected = provider.config.authStatus === "connected";
        const card = el("article", `provider-registry-card acp-provider-card${isConnected ? " is-active" : ""}`);
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
        let statusText;
        let statusClass = "";
        if (provider.status === "disabled") { statusText = "● Disabled"; }
        else if (provider.status === "not-configured") { statusText = `● ${isUnverifiedProvider ? "Unverified" : "Not configured"}`; }
        else if (isConnected) { statusText = "● Connected"; statusClass = " configured"; }
        else { statusText = "● Needs auth"; statusClass = " needs-auth"; }
        const status = el("span", `provider-status${statusClass}`, statusText);
        const actions = el("div", "provider-card-actions");
        const connectBtn = el("button", "mini-btn acp-connect-btn", "Connect");
        connectBtn.type = "button";
        connectBtn.hidden = !provider.config.enabled || provider.status === "not-configured" || isConnected;
        connectBtn.addEventListener("click", async () => {
          connectBtn.disabled = true;
          connectBtn.textContent = "Connecting…";
          try {
            const updated = await window.shell.acpProviders.probe(provider.manifest.id);
            if (updated?.config.authStatus === "connected") {
              showToast(`${provider.manifest.displayName} connected.`, "success");
            } else {
              showToast(`${provider.manifest.displayName} not connected: ${updated?.config.authError || "auth failed"}`, "error");
            }
            await renderAcpProviderCards();
          } catch (error) {
            showToast(`Connect failed: ${error.message || error}`, "error");
            await renderAcpProviderCards();
          }
        });
        const action = el("button", "mini-btn provider-card-action", "Configure");
        action.type = "button";
        action.addEventListener("click", () => showAcpProviderEditor(provider));
        actions.append(connectBtn, action);
        footer.append(status, actions);
        if (provider.config.authError && provider.config.authStatus !== "connected") {
          const errLine = el("p", "acp-auth-error", provider.config.authError);
          card.append(head, description, footer, errLine);
        } else {
          card.append(head, description, footer);
        }
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
  wireAppBehaviorToggle("#settings-canvas-enabled", "canvasEnabled");
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
    if (!$("#job-delete-dialog").hidden) jobsController?.closeDeleteDialog();
    if ($("#job-modal")?.classList.contains("active")) jobsController?.closeModal();
    if ($("#job-output-modal")?.classList.contains("active")) jobsController?.closeOutput();
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
  $("#plugins-add-mcp").addEventListener("click", () => openAddPluginModal("custom"));
  $("#plugins-install-plugin").addEventListener("click", () => openAddPluginModal("plugin"));
  $("#native-mcp-transport").addEventListener("change", (e) => toggleMcpTransportFields(e.target.value));
  $("#btn-edit-mcp").addEventListener("click", () => {
    if (currentPlugin?.source === "native-mcp") openAddPluginModal("custom", currentPlugin);
  });
  $("#custom-mcp-tab").addEventListener("click", () => setPluginModalTab("custom"));
  $("#nusashell-plugin-tab").addEventListener("click", () => setPluginModalTab("plugin"));
  $("#native-mcp-import-btn").addEventListener("click", () => {
    try { parseNativeJson($("#native-mcp-import").value); } catch (error) { $("#native-mcp-status").textContent = `Error: ${error.message || error}`; $("#native-mcp-status").style.display = "block"; }
  });
  $("#native-mcp-save").addEventListener("click", () => void saveNativeMcp());
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
    const authMethodId = $("#acp-provider-auth-select").value || undefined;
    try {
      await window.shell.acpProviders.save({ providerId: id, enabled: $("#acp-provider-enabled").checked, command, args, authMethodId });
      closeAcpProviderEditor();
      await renderAcpProviderCards();
      showToast("ACP provider saved.", "success");
    } catch (error) {
      showToast(`Could not save ACP provider: ${error.message || error}`, "error");
    }
  });
  $("#acp-provider-close").addEventListener("click", closeAcpProviderEditor);
  $("#acp-provider-modal-overlay").addEventListener("click", closeAcpProviderEditor);

  // Version — fetched after WS connects (see onOpen callback below)

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

  // Connect and subscribe — subscribe must happen AFTER the socket opens,
  // otherwise NusaClient.request() rejects with "Not connected" and the
  // subscription is silently lost (no streaming events reach the renderer).
  initWsClient({
    url: WS_URL,
    onOpen: (isOpen) => {
      updateConnStatus(isOpen !== false);
      if (isOpen !== false) {
        subscribe(["*"]).catch(() => {});
        refreshAll();
        getVersion().then(v => {
          if (v?.version) {
            $("#settings-version").textContent = v.version;
            $("#settings-about-version").textContent = v.version;
          }
        }).catch(() => {});
      }
    },
    onLog: writeRendererLog,
  });
  connectWs();

  // Periodic refresh (fallback for state sync)
  setInterval(() => { if (isConnected()) refreshAll(); }, 5000);
});
