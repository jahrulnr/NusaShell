// ---- Logging helper (show bridge traffic in the UI) ----
function log(msg) {
  const el = document.getElementById("log");
  const line = document.createElement("div");
  line.className = "line";
  line.textContent = `[${new Date().toLocaleTimeString()}] ${msg}`;
  el.appendChild(line);
  el.scrollTop = el.scrollHeight;
}

let currentPluginId = null;

// ---- Render grid from /api/plugins ----
async function loadPlugins() {
  const res = await fetch("/api/plugins");
  const plugins = await res.json();
  const grid = document.getElementById("grid");
  grid.innerHTML = "";
  for (const p of plugins) {
    const cell = document.createElement("div");
    cell.className = "icon-cell";
    cell.onclick = () => openPlugin(p);
    cell.innerHTML = `
      <div class="icon-box">${p.icon || "🧩"}<span class="badge ${p.state}"></span></div>
      <div class="icon-label">${p.name}</div>
    `;
    grid.appendChild(cell);
  }
}

function openPlugin(manifest) {
  currentPluginId = manifest.id;
  document.getElementById("pluginWindowTitle").textContent = `${manifest.icon} ${manifest.name}`;
  document.getElementById("pluginFrame").src = `/plugins/${manifest.id}/ui/index.html`;
  document.getElementById("pluginWindow").classList.add("open");
  log(`opened plugin "${manifest.id}" - UI loaded into iframe`);
}

function closePluginWindow() {
  document.getElementById("pluginWindow").classList.remove("open");
  document.getElementById("pluginFrame").src = "about:blank";
  log(`closed plugin "${currentPluginId}" window (MCP process stays alive in the background if still 'running')`);
  currentPluginId = null;
  loadPlugins(); // refresh badge state
}

// ---- BRIDGE: accept tool_call from iframe, forward to shell backend, return result ----
window.addEventListener("message", async (event) => {
  const msg = event.data;
  if (!msg || msg.type !== "tool_call") return;

  log(`bridge: UI -> shell : tool_call "${msg.tool}" (plugin: ${currentPluginId})`);

  const res = await fetch(`/api/plugins/${currentPluginId}/call`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tool: msg.tool, args: msg.args })
  });
  const data = await res.json();

  log(`bridge: shell -> UI : tool_result for "${msg.tool}" ${data.error ? "(error: " + data.error + ")" : "(ok)"}`);

  const iframe = document.getElementById("pluginFrame");
  iframe.contentWindow.postMessage(
    { type: "tool_result", requestId: msg.requestId, result: data.result, error: data.error },
    "*"
  );

  loadPlugins(); // refresh badge (becomes "running" after the process is spawned)
});

loadPlugins();
setInterval(loadPlugins, 3000); // poll so running/idle badges stay current
