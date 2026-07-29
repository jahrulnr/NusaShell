// All interactions here are cosmetic / visual state only - no real MCP
// (or any other) logic. Just enough to make the mockup feel alive.
// Mock data shapes match the real backend DTOs.

// ============ Mock Data ============

const MOCK_PLUGINS = [
  { pluginId: "com.example.notes", name: "Notes", version: "1.0.0", state: "idle", enabled: true, icon: "📝", iconBg: "bg-white", category: "Productivity", manifest: { transport: "stdio", command: "node mcp/server.js", autostart: false, keepAliveOnClose: false, windowMode: "panel" } },
  { pluginId: "com.example.weather", name: "Weather", version: "1.2.0", state: "running", enabled: true, icon: "🌤", iconBg: "bg-blue", category: "Utilities", pid: 12345, manifest: { transport: "stdio", command: "node mcp/server.js", autostart: true, keepAliveOnClose: true, windowMode: "widget" } },
  { pluginId: "com.example.chat", name: "AI Chat", version: "0.9.0", state: "crashed", enabled: true, icon: "🤖", iconBg: "bg-purple", category: "AI Tools", manifest: { transport: "sse", command: "https://chat.example.com/mcp", autostart: false, keepAliveOnClose: false, windowMode: "fullscreen" } },
  { pluginId: "com.example.calendar", name: "Calendar", version: "2.1.0", state: "idle", enabled: true, icon: "📅", iconBg: "bg-blue", category: "Productivity", manifest: { transport: "stdio", command: "node mcp/server.js", autostart: false, keepAliveOnClose: false, windowMode: "panel" } },
  { pluginId: "com.example.terminal", name: "Terminal", version: "1.0.0", state: "running", enabled: true, icon: "⬛", iconBg: "bg-black", category: "Developer", pid: 12346, manifest: { transport: "stdio", command: "node mcp/server.js", autostart: true, keepAliveOnClose: true, windowMode: "fullscreen" } },
  { pluginId: "com.example.music", name: "Music", version: "3.0.0", state: "idle", enabled: false, icon: "🎵", iconBg: "bg-red", category: "Utilities", manifest: { transport: "http", command: "https://music.example.com/mcp", autostart: false, keepAliveOnClose: false, windowMode: "panel" } },
  { pluginId: "com.example.tasks", name: "Tasks", version: "1.1.0", state: "idle", enabled: true, icon: "https://raw.githubusercontent.com/twitter/twemoji/master/assets/72x72/2705.png", iconBg: "bg-green", category: "Productivity", manifest: { transport: "stdio", command: "node mcp/server.js", autostart: false, keepAliveOnClose: false, windowMode: "panel" } },
  { pluginId: "com.example.timer", name: "Timer", version: "0.5.0", state: "idle", enabled: true, icon: "file://icon.png", iconBg: "bg-orange", category: "Utilities", manifest: { transport: "stdio", command: "node mcp/server.js", autostart: false, keepAliveOnClose: false, windowMode: "widget" } },
];

const MOCK_TOOLS = {
  "com.example.notes": [
    { name: "createNote", description: "Create a new note with text content" },
    { name: "listNotes", description: "List all saved notes" },
    { name: "deleteNote", description: "Delete a note by ID" },
    { name: "searchNotes", description: "Search notes by keyword" },
  ],
  "com.example.weather": [
    { name: "getForecast", description: "Get weather forecast for a location" },
    { name: "getAlerts", description: "Get active weather alerts" },
  ],
  "com.example.chat": [
    { name: "sendMessage", description: "Send a message to the AI" },
    { name: "getHistory", description: "Get conversation history" },
    { name: "clearHistory", description: "Clear all conversation history" },
  ],
  "com.example.calendar": [
    { name: "createEvent", description: "Create a calendar event" },
    { name: "listEvents", description: "List upcoming events" },
    { name: "deleteEvent", description: "Delete an event by ID" },
  ],
  "com.example.terminal": [
    { name: "execCommand", description: "Execute a shell command" },
    { name: "getCwd", description: "Get current working directory" },
  ],
  "com.example.music": [
    { name: "play", description: "Play a track by ID or name" },
    { name: "pause", description: "Pause playback" },
    { name: "getQueue", description: "Get current playback queue" },
  ],
  "com.example.tasks": [
    { name: "createTask", description: "Create a new task" },
    { name: "listTasks", description: "List all tasks" },
    { name: "completeTask", description: "Mark a task as complete" },
  ],
  "com.example.timer": [
    { name: "startTimer", description: "Start a countdown timer" },
    { name: "stopTimer", description: "Stop the active timer" },
    { name: "getRemaining", description: "Get remaining time" },
  ],
};

const MOCK_EVENTS = [
  { event: "plugin.started", sequence: 1, payload: { pluginId: "com.example.weather", state: "running", pid: 12345, timestamp: "2026-07-28T18:00:00Z" } },
  { event: "plugin.started", sequence: 2, payload: { pluginId: "com.example.terminal", state: "running", pid: 12346, timestamp: "2026-07-28T18:01:00Z" } },
  { event: "plugin.crashed", sequence: 3, payload: { pluginId: "com.example.chat", state: "crashed", exitCode: 1, timestamp: "2026-07-28T18:02:00Z" } },
  { event: "plugin.state_changed", sequence: 4, payload: { pluginId: "com.example.chat", oldState: "running", newState: "crashed", timestamp: "2026-07-28T18:02:00Z" } },
  { event: "tool.call_completed", sequence: 5, payload: { pluginId: "com.example.weather", requestId: "req_001", toolName: "getForecast", success: true, timestamp: "2026-07-28T18:03:00Z" } },
  { event: "plugin.stopped", sequence: 6, payload: { pluginId: "com.example.notes", state: "idle", timestamp: "2026-07-28T18:04:00Z" } },
];

const MOCK_RECENT_INSTALLS = [
  { name: "AI Chat", version: "0.9.0", time: "2 hours ago" },
  { name: "Terminal", version: "1.0.0", time: "1 day ago" },
  { name: "Music", version: "3.0.0", time: "3 days ago" },
];

const STATES = ["idle", "starting", "running", "stopping", "crashed"];

// ============ State ============

let currentPlugin = null;
let eventFilter = "all";

// ============ Helpers ============

function $(sel) { return document.querySelector(sel); }
function $$(sel) { return document.querySelectorAll(sel); }
function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html !== undefined) e.innerHTML = html;
  return e;
}
function getPlugin(id) { return MOCK_PLUGINS.find(p => p.pluginId === id); }
function nowTime() { return new Date().toLocaleTimeString("en-US", { hour12: false }); }

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
  MOCK_PLUGINS.forEach(p => {
    const cell = el("button", "app-cell");
    cell.dataset.pluginId = p.pluginId;
    cell.innerHTML = `<div class="app-icon ${p.iconBg}">${renderIconHtml(p.icon, 28)}</div><div class="app-name">${p.name}</div><div class="app-status ${p.state}">${stateBadgeHtml(p.state)}</div>`;
    cell.addEventListener("click", () => openPluginWindow(p));
    cell.addEventListener("contextmenu", (e) => { e.preventDefault(); showContextMenu(e.clientX, e.clientY, p); });
    grid.appendChild(cell);
  });
  const addCell = el("button", "app-cell app-cell-add");
  addCell.innerHTML = `<div class="app-icon bg-dashed"><svg viewBox="0 0 24 24" width="24" height="24" fill="none"><path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg></div><div class="app-name">Add Plugin</div><div class="app-status"></div>`;
  addCell.addEventListener("click", () => openAddPluginModal());
  grid.appendChild(addCell);
}

// ============ Render: Installed Table ============

function renderInstalledTable() {
  const table = $("#plugin-table");
  table.innerHTML = "";
  MOCK_PLUGINS.forEach(p => {
    const row = el("div", "plugin-row");
    row.dataset.pluginId = p.pluginId;
    row.innerHTML = `<div class="plugin-row-icon ${p.iconBg}">${renderIconHtml(p.icon, 18)}</div><div class="plugin-row-info"><div class="plugin-row-name">${p.name}</div><div class="plugin-row-meta">${p.pluginId} · v${p.version} · ${p.enabled ? "Enabled" : "Disabled"}</div></div><div class="plugin-row-state ${p.state}">${stateBadgeHtml(p.state) || "Idle"}</div>`;
    row.addEventListener("click", () => openDrawer(p));
    table.appendChild(row);
  });
}

// ============ Render: Running List ============

function renderRunningList() {
  const list = $("#running-list");
  list.innerHTML = "";
  const running = MOCK_PLUGINS.filter(p => p.state === "running" || p.state === "starting");
  if (running.length === 0) {
    list.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No plugins currently running.</div>';
    return;
  }
  running.forEach(p => {
    const card = el("div", "running-card");
    card.innerHTML = `<div class="running-card-icon ${p.iconBg}">${renderIconHtml(p.icon, 20)}</div><div class="running-card-info"><div class="running-card-name">${p.name}</div><div class="running-card-meta">${p.pluginId} · ${p.manifest.transport}</div></div><div class="running-card-pid">PID ${p.pid || "—"}</div><div class="running-card-actions"><button class="mini-btn" data-action="stop" data-id="${p.pluginId}">Stop</button><button class="mini-btn" data-action="restart" data-id="${p.pluginId}">Restart</button></div>`;
    list.appendChild(card);
  });
  list.querySelectorAll("[data-action]").forEach(btn => {
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      const action = btn.dataset.action;
      const id = btn.dataset.id;
      if (action === "stop") changePluginState(id, "idle");
      if (action === "restart") changePluginState(id, "starting");
    });
  });
}

// ============ Render: Event Timeline ============

function eventDescription(e) {
  const p = e.payload;
  switch (e.event) {
    case "plugin.started": return `${p.pluginId} → running (PID ${p.pid})`;
    case "plugin.stopped": return `${p.pluginId} → ${p.state}`;
    case "plugin.crashed": return `${p.pluginId} crashed with exit code ${p.exitCode}`;
    case "plugin.state_changed": return `${p.pluginId}: ${p.oldState} → ${p.newState}`;
    case "tool.call_completed": return `${p.pluginId} · ${p.toolName} (${p.success ? "success" : "failed"})`;
    default: return JSON.stringify(p);
  }
}

function renderEventTimeline() {
  const timeline = $("#event-timeline");
  timeline.innerHTML = "";
  const events = eventFilter === "all" ? MOCK_EVENTS : MOCK_EVENTS.filter(e => e.event === eventFilter);
  if (events.length === 0) {
    timeline.innerHTML = '<div style="color:var(--text-faint);font-size:13px;padding:20px 0">No events match this filter.</div>';
    return;
  }
  const iconMap = { started: "▶", stopped: "■", crashed: "✕", state_changed: "↻", tool_call_completed: "⚡" };
  events.forEach(e => {
    const iconKey = e.event.replace("plugin.", "").replace("tool.", "tool_call_");
    const iconClass = e.event.replace("plugin.", "").replace("tool.", "tool_");
    const time = e.payload.timestamp ? new Date(e.payload.timestamp).toLocaleTimeString("en-US", { hour12: false }) : "";
    const entry = el("div", "event-entry");
    entry.innerHTML = `<div class="event-icon ${iconClass}">${iconMap[iconKey] || "•"}</div><div class="event-content"><div class="event-type">${e.event}</div><div class="event-desc">${eventDescription(e)}</div></div><div class="event-seq">#${e.sequence}</div><div class="event-time">${time}</div>`;
    timeline.appendChild(entry);
  });
}

// ============ Plugin Detail Drawer ============

function openDrawer(plugin) {
  currentPlugin = plugin;
  $("#drawer-icon").className = `drawer-icon ${plugin.iconBg}`;
  $("#drawer-icon").innerHTML = `<span style="font-size:20px">${plugin.icon}</span>`;
  $("#drawer-title").textContent = plugin.name;
  $("#drawer-subtitle").textContent = `${plugin.pluginId} · v${plugin.version}`;

  const sm = $("#state-machine");
  sm.innerHTML = "";
  STATES.forEach((s, i) => {
    if (i > 0) sm.appendChild(el("span", "state-arrow", "→"));
    sm.appendChild(el("span", `state-node${s === plugin.state ? " current" : ""}`, s));
  });

  const tools = MOCK_TOOLS[plugin.pluginId] || [];
  $("#tool-count").textContent = tools.length;
  const tl = $("#tools-list");
  tl.innerHTML = "";
  if (tools.length === 0) {
    tl.innerHTML = '<div style="color:var(--text-faint);font-size:12px">No tools available.</div>';
  } else {
    tools.forEach(t => {
      const item = el("div", "tool-item");
      item.innerHTML = `<div class="tool-item-info"><div class="tool-item-name">${t.name}</div><div class="tool-item-desc">${t.description}</div></div><button class="tool-try-btn">Try</button>`;
      item.querySelector(".tool-try-btn").addEventListener("click", () => openPluginWindow(plugin));
      tl.appendChild(item);
    });
  }

  const m = plugin.manifest;
  $("#manifest-info").innerHTML = `<div class="manifest-row"><span class="manifest-key">id</span><span class="manifest-val">${plugin.pluginId}</span></div><div class="manifest-row"><span class="manifest-key">version</span><span class="manifest-val">${plugin.version}</span></div><div class="manifest-row"><span class="manifest-key">transport</span><span class="manifest-val">${m.transport}</span></div><div class="manifest-row"><span class="manifest-key">command</span><span class="manifest-val">${m.command}</span></div><div class="manifest-row"><span class="manifest-key">autostart</span><span class="manifest-val">${m.autostart}</span></div><div class="manifest-row"><span class="manifest-key">keepAlive</span><span class="manifest-val">${m.keepAliveOnClose}</span></div><div class="manifest-row"><span class="manifest-key">window</span><span class="manifest-val">${m.windowMode}</span></div>`;

  $("#plugin-drawer").classList.add("active");
  $("#drawer-overlay").classList.add("active");
}

function closeDrawer() {
  $("#plugin-drawer").classList.remove("active");
  $("#drawer-overlay").classList.remove("active");
  currentPlugin = null;
}

// ============ Plugin Window Overlay ============

function bridgeLog(msg, type) {
  const body = $("#bridge-log-body");
  const line = el("div", "bridge-line");
  const cls = type === "req" ? "bridge-req" : type === "res" ? "bridge-res" : type === "err" ? "bridge-err" : "";
  line.innerHTML = `<span class="bridge-ts">[${nowTime()}]</span> <span class="${cls}">${msg}</span>`;
  body.appendChild(line);
  body.scrollTop = body.scrollHeight;
}

function openPluginWindow(plugin) {
  $("#pw-title").textContent = `${plugin.name}`;
  $("#plugin-window-overlay").classList.add("active");
  $("#bridge-log-body").innerHTML = "";
  bridgeLog(`opened plugin "${plugin.pluginId}" — UI loaded into iframe`);
  bridgeLog(`MCP transport: ${plugin.manifest.transport}`, "info");
  if (plugin.state === "idle") {
    bridgeLog(`plugin.start("${plugin.pluginId}") → starting`, "req");
    setTimeout(() => bridgeLog(`plugin.started — PID ${(Math.random() * 99999 | 0)}`, "res"), 400);
  }
}

function closePluginWindow() { $("#plugin-window-overlay").classList.remove("active"); }

// ============ Add Plugin Modal ============

function renderRecentInstalls() {
  const list = $("#recent-list");
  list.innerHTML = "";
  MOCK_RECENT_INSTALLS.forEach(r => {
    list.appendChild(el("div", "recent-item", `<span class="recent-item-name">${r.name}</span><span class="recent-item-ver">v${r.version}</span><span class="recent-item-time">${r.time}</span>`));
  });
}

function openAddPluginModal() {
  $("#add-plugin-modal").classList.add("active");
  renderRecentInstalls();
}

function closeAddPluginModal() {
  $("#add-plugin-modal").classList.remove("active");
  $("#manifest-preview").style.display = "none";
  $("#plugin-url-input").value = "";
}

function showManifestPreview() {
  const content = $("#manifest-preview-content");
  content.innerHTML = `<div class="manifest-row"><span class="manifest-key">id</span><span class="manifest-val">com.example.new-plugin</span></div><div class="manifest-row"><span class="manifest-key">name</span><span class="manifest-val">New Plugin</span></div><div class="manifest-row"><span class="manifest-key">version</span><span class="manifest-val">1.0.0</span></div><div class="manifest-row"><span class="manifest-key">transport</span><span class="manifest-val">stdio</span></div><div class="manifest-row"><span class="manifest-key">window</span><span class="manifest-val">panel</span></div>`;
  $("#manifest-preview").style.display = "block";
}

function simulateInstall() {
  const url = $("#plugin-url-input").value.trim();
  if (!url) { showManifestPreview(); return; }
  const newPlugin = {
    pluginId: `com.example.${url.split("/").pop() || "new-plugin"}`,
    name: url.split("/").pop() || "New Plugin",
    version: "1.0.0", state: "idle", enabled: true, icon: "🧩", iconBg: "bg-purple",
    category: "Utilities",
    manifest: { transport: "stdio", command: "node mcp/server.js", autostart: false, keepAliveOnClose: false, windowMode: "panel" },
  };
  MOCK_PLUGINS.push(newPlugin);
  MOCK_RECENT_INSTALLS.unshift({ name: newPlugin.name, version: newPlugin.version, time: "just now" });
  renderAppGrid(); renderInstalledTable(); renderRecentInstalls();
  closeAddPluginModal();
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

// ============ State Changes ============

function updateActivityBadge() { $("#activity-badge").textContent = MOCK_EVENTS.length; }

function changePluginState(pluginId, newState) {
  const p = getPlugin(pluginId);
  if (!p) return;
  const oldState = p.state;
  p.state = newState;
  if (newState === "running" && !p.pid) p.pid = Math.random() * 99999 | 10000;
  if (newState === "idle") delete p.pid;
  const seq = MOCK_EVENTS.length + 1;
  if (newState === "running") {
    MOCK_EVENTS.push({ event: "plugin.started", sequence: seq, payload: { pluginId, state: "running", pid: p.pid, timestamp: new Date().toISOString() } });
  } else if (newState === "idle") {
    MOCK_EVENTS.push({ event: "plugin.stopped", sequence: seq, payload: { pluginId, state: "idle", timestamp: new Date().toISOString() } });
  } else if (newState === "crashed") {
    MOCK_EVENTS.push({ event: "plugin.crashed", sequence: seq, payload: { pluginId, state: "crashed", exitCode: 1, timestamp: new Date().toISOString() } });
  }
  if (oldState !== newState) {
    MOCK_EVENTS.push({ event: "plugin.state_changed", sequence: seq + 1, payload: { pluginId, oldState, newState, timestamp: new Date().toISOString() } });
  }
  updateActivityBadge();
  renderAppGrid(); renderInstalledTable(); renderRunningList(); renderEventTimeline();
  if (currentPlugin && currentPlugin.pluginId === pluginId) openDrawer(p);
}

// ============ Init ============

document.addEventListener("DOMContentLoaded", () => {
  renderAppGrid(); renderInstalledTable(); renderRunningList(); renderEventTimeline(); updateActivityBadge();

  $$("[data-nav]").forEach(item => item.addEventListener("click", () => switchView(item.dataset.view)));
  $("#nav-settings-btn").addEventListener("click", () => switchView("settings"));
  $("#window-always-on-top").addEventListener("click", event => {
    const button = event.currentTarget;
    const isActive = button.getAttribute("aria-pressed") !== "true";
    const label = isActive ? "Stop keeping window on top" : "Keep window on top";
    button.classList.toggle("is-active", isActive);
    button.setAttribute("aria-pressed", String(isActive));
    button.setAttribute("aria-label", label);
    button.title = label;
  });

  $$("[data-tab]").forEach(tab => tab.addEventListener("click", () => {
    $$("[data-tab]").forEach(t => t.classList.remove("active"));
    tab.classList.add("active");
  }));

  $$("[data-event-filter]").forEach(chip => chip.addEventListener("click", () => {
    $$("[data-event-filter]").forEach(c => c.classList.remove("active"));
    chip.classList.add("active");
    eventFilter = chip.dataset.eventFilter;
    renderEventTimeline();
  }));

  $("#drawer-close").addEventListener("click", closeDrawer);
  $("#drawer-overlay").addEventListener("click", closeDrawer);

  $("#btn-start").addEventListener("click", () => {
    if (currentPlugin) { changePluginState(currentPlugin.pluginId, "starting"); setTimeout(() => changePluginState(currentPlugin.pluginId, "running"), 600); }
  });
  $("#btn-stop").addEventListener("click", () => {
    if (currentPlugin) { changePluginState(currentPlugin.pluginId, "stopping"); setTimeout(() => changePluginState(currentPlugin.pluginId, "idle"), 400); }
  });
  $("#btn-restart").addEventListener("click", () => {
    if (currentPlugin) { changePluginState(currentPlugin.pluginId, "stopping"); setTimeout(() => changePluginState(currentPlugin.pluginId, "starting"), 400); setTimeout(() => changePluginState(currentPlugin.pluginId, "running"), 1000); }
  });

  $("#pw-close").addEventListener("click", closePluginWindow);

  $("#pw-save").addEventListener("click", () => {
    const text = $(".pw-textarea").value.trim();
    if (!text) return;
    const reqId = `req_${Math.random().toString(36).slice(2, 8)}`;
    bridgeLog(`window.shell.callTool("createNote", { text: "${text.slice(0, 30)}..." })`, "req");
    bridgeLog(`bridge: UI → shell : tool_call "createNote" (requestId: ${reqId})`);
    setTimeout(() => {
      bridgeLog(`bridge: shell → MCP : tools/call "createNote"`, "req");
      bridgeLog(`bridge: MCP → shell : result (ok)`, "res");
      bridgeLog(`bridge: shell → UI : tool_result for "createNote" (ok)`, "res");
      bridgeLog(`tool.call_completed — ${reqId} · createNote (success)`);
      $(".pw-textarea").value = "";
    }, 500);
  });

  $("#open-add-plugin").addEventListener("click", openAddPluginModal);
  $("#modal-close").addEventListener("click", closeAddPluginModal);
  $("#modal-cancel").addEventListener("click", closeAddPluginModal);
  $("#modal-install").addEventListener("click", simulateInstall);
  $("#plugin-url-input").addEventListener("input", () => { if ($("#plugin-url-input").value.trim()) showManifestPreview(); });

  document.addEventListener("click", hideContextMenu);
  $$(".ctx-item").forEach(item => item.addEventListener("click", (e) => {
    e.stopPropagation();
    const action = item.dataset.action;
    const id = $("#context-menu").dataset.pluginId;
    const p = getPlugin(id);
    if (!p) return;
    switch (action) {
      case "open": openPluginWindow(p); break;
      case "start": changePluginState(id, "starting"); setTimeout(() => changePluginState(id, "running"), 600); break;
      case "stop": changePluginState(id, "stopping"); setTimeout(() => changePluginState(id, "idle"), 400); break;
      case "restart": changePluginState(id, "stopping"); setTimeout(() => changePluginState(id, "starting"), 400); setTimeout(() => changePluginState(id, "running"), 1000); break;
      case "detail": openDrawer(p); break;
      case "uninstall":
        const idx = MOCK_PLUGINS.findIndex(pp => pp.pluginId === id);
        if (idx >= 0) MOCK_PLUGINS.splice(idx, 1);
        renderAppGrid(); renderInstalledTable(); renderRunningList();
        break;
    }
    hideContextMenu();
  }));

  $("#ping-btn").addEventListener("click", () => {
    const result = $("#ping-result");
    result.style.display = "flex";
    result.querySelector(".settings-value").textContent = "pong: true (2ms)";
  });

  const searchInput = $(".search input");
  if (searchInput) {
    searchInput.addEventListener("input", () => {
      const q = searchInput.value.toLowerCase();
      $$("#app-grid .app-cell").forEach(cell => {
        const name = cell.querySelector(".app-name")?.textContent.toLowerCase() || "";
        cell.style.display = !q || name.includes(q) ? "" : "none";
      });
    });
  }
});
