const pluginId = new URLSearchParams(location.search).get("pluginId") || "nusashell.terminal";

const els = {
  container: document.getElementById("terminal-container"),
  overlay: document.getElementById("terminal-overlay"),
  overlayMessage: document.getElementById("overlay-message"),
  statusCwd: document.getElementById("status-cwd"),
  statusShell: document.getElementById("status-shell"),
  statusSession: document.getElementById("status-session"),
  statusState: document.getElementById("status-state"),
  newSession: document.getElementById("new-session-button"),
  clear: document.getElementById("clear-button"),
  retry: document.getElementById("retry-button"),
};

const POLL_MS = 60;

let term;
let fitAddon;
let sessionId = null;
let pollTimer = null;
let stopped = false;

function setStatus(state, text) {
  els.statusState.dataset.state = state;
  els.statusState.textContent = text;
}

function showOverlay(message) {
  if (message) els.overlayMessage.textContent = message;
  els.overlay.hidden = false;
  setStatus("error", "offline");
}

function hideOverlay() {
  els.overlay.hidden = true;
}

async function callTool(name, args = {}) {
  if (!window.shell || typeof window.shell.callTool !== "function") {
    throw new Error("NusaShell bridge unavailable. Open Terminal from the NusaShell launcher.");
  }
  const result = await window.shell.callTool(pluginId, name, args);
  if (result == null) throw new Error(`No response from terminal tool ${name}.`);
  if (result.isError) throw new Error(extractText(result) || `Tool ${name} failed.`);
  return result;
}

function extractText(result) {
  const chunks = Array.isArray(result?.content) ? result.content : [];
  return chunks.map((c) => (typeof c?.text === "string" ? c.text : "")).join("");
}

function parseJsonResult(result, fallback) {
  const text = extractText(result);
  if (!text) return fallback;
  try { return JSON.parse(text); } catch { return fallback; }
}

function ensureTerminal() {
  if (term) return;
  term = new window.Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: "SF Mono, Cascadia Code, JetBrains Mono, Menlo, Consolas, monospace",
    theme: {
      background: "#0b0f14",
      foreground: "#e5e9f0",
      cursor: "#4cc2ff",
      selectionBackground: "rgba(76,194,255,0.25)",
    },
  });
  fitAddon = new window.FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(els.container);
  fitAddon.fit();

  term.onData((data) => {
    if (!sessionId) return;
    callTool("terminal_write", { sessionId, data }).catch(() => {});
  });
}

function fitAndResize() {
  if (!term || !fitAddon) return;
  fitAddon.fit();
  if (sessionId) {
    callTool("terminal_resize", { sessionId, cols: term.cols, rows: term.rows }).catch(() => {});
  }
}

async function openSession() {
  ensureTerminal();
  hideOverlay();
  setStatus("starting", "starting…");

  const { cols, rows } = term;
  let lastErr = null;
  // The plugin may still be in "starting" (MCP handshake) right after the
  // window opens; retry a few times before giving up.
  for (let attempt = 0; attempt < 10; attempt++) {
    try {
      const result = await callTool("terminal_open", { cols, rows });
      const info = parseJsonResult(result, null);
      if (!info || !info.sessionId) throw new Error("Failed to open terminal session.");

      sessionId = info.sessionId;
      els.statusCwd.textContent = info.cwd || "Home";
      els.statusCwd.title = info.cwd || "";
      els.statusShell.textContent = info.shell || "shell";
      els.statusSession.textContent = info.sessionId.slice(0, 8);
      setStatus("running", "running");
      startPolling();
      return;
    } catch (err) {
      lastErr = err;
      if (!/not running|no active MCP|Backend not ready/i.test(err.message)) throw err;
      await new Promise((r) => setTimeout(r, 300));
    }
  }
  throw lastErr || new Error("Failed to open terminal session.");
}

function startPolling() {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = setInterval(async () => {
    if (!sessionId || stopped) return;
    try {
      const result = await callTool("terminal_read", { sessionId, clear: true });
      const data = parseJsonResult(result, null);
      if (!data) return;
      if (data.stdout) term.write(data.stdout);
      if (data.stderr) term.write(data.stderr);
      if (data.exited) {
        setStatus("error", `exited ${data.exitCode ?? ""}`.trim());
        stopPolling();
      }
    } catch (err) {
      setStatus("error", "offline");
      showOverlay(err.message);
      stopPolling();
    }
  }, POLL_MS);
}

function stopPolling() {
  stopped = true;
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = null;
}

async function newSession() {
  stopPolling();
  stopped = false;
  if (sessionId) {
    callTool("terminal_close", { sessionId }).catch(() => {});
    sessionId = null;
  }
  if (term) term.reset();
  try {
    await openSession();
  } catch (err) {
    showOverlay(err.message);
  }
}

async function boot() {
  try {
    await openSession();
  } catch (err) {
    showOverlay(err.message || "Start the Terminal plugin from the launcher, then retry.");
  }
}

els.newSession.addEventListener("click", newSession);
els.retry.addEventListener("click", () => { stopped = false; boot(); });
els.clear.addEventListener("click", () => { if (term) term.clear(); });
window.addEventListener("resize", fitAndResize);
window.addEventListener("beforeunload", () => {
  if (sessionId) {
    try { window.shell.callTool(pluginId, "terminal_close", { sessionId }); } catch (_) { /* ignore */ }
  }
});

boot();
