const sessionStorageKey = "openai-responses-demo-session";
const messages = document.querySelector("#messages");
const form = document.querySelector("#chat-form");
const messageInput = document.querySelector("#message");
const sendButton = document.querySelector("#send-button");
const resetButton = document.querySelector("#reset-button");
const meta = document.querySelector("#meta");
const sessionLabel = document.querySelector("#session-label");

let sessionId = loadSessionId();

function newSessionId() {
  if (globalThis.crypto?.randomUUID) {
    return globalThis.crypto.randomUUID();
  }
  return `browser-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function loadSessionId() {
  try {
    const saved = sessionStorage.getItem(sessionStorageKey);
    if (saved) {
      return saved;
    }
    const created = newSessionId();
    sessionStorage.setItem(sessionStorageKey, created);
    return created;
  } catch {
    return newSessionId();
  }
}

function saveSessionId() {
  try {
    sessionStorage.setItem(sessionStorageKey, sessionId);
  } catch {
    // Private browsing or a blocked storage provider is still usable.
  }
}

function setSessionLabel() {
  sessionLabel.textContent = `session …${sessionId.slice(-8)}`;
}

function addMessage(role, text, detail = "") {
  const item = document.createElement("li");
  item.className = `message message-${role}`;

  const heading = document.createElement("span");
  heading.className = "message-role";
  heading.textContent = role === "user" ? "You" : role === "error" ? "Error" : "Assistant";

  const body = document.createElement("p");
  body.className = "message-body";
  // textContent is intentional: model, tool, and user text are data, not HTML.
  body.textContent = text;

  item.append(heading, body);
  if (detail) {
    const note = document.createElement("small");
    note.className = "message-detail";
    note.textContent = detail;
    item.append(note);
  }
  messages.append(item);
  item.scrollIntoView({ block: "end", behavior: "smooth" });
}

function setBusy(busy) {
  messageInput.disabled = busy;
  sendButton.disabled = busy;
  resetButton.disabled = busy;
  form.setAttribute("aria-busy", String(busy));
  sendButton.textContent = busy ? "Thinking…" : "Send";
}

async function readResponse(response) {
  const raw = await response.text();
  let data;
  try {
    data = raw ? JSON.parse(raw) : {};
  } catch {
    data = {};
  }
  if (!response.ok) {
    throw new Error(data.error || raw || `Request failed (${response.status})`);
  }
  return data;
}

async function sendMessage(event) {
  event.preventDefault();
  const message = messageInput.value.trim();
  if (!message) {
    return;
  }

  addMessage("user", message);
  messageInput.value = "";
  setBusy(true);
  try {
    const response = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_id: sessionId, message }),
    });
    const data = await readResponse(response);
    const toolNames = Array.isArray(data.tool_calls) && data.tool_calls.length > 0
      ? `tool: ${data.tool_calls.join(", ")}`
      : "no tool call";
    addMessage("assistant", data.reply, toolNames);

    const usage = data.usage || {};
    const total = Number.isFinite(usage.total_tokens) ? usage.total_tokens : 0;
    meta.textContent = `Prompt cache key: ${data.prompt_cache_key || "not returned"} · total tokens: ${total}`;
    if (data.session_id && data.session_id !== sessionId) {
      sessionId = data.session_id;
      saveSessionId();
      setSessionLabel();
    }
  } catch (error) {
    addMessage("error", error instanceof Error ? error.message : "Request failed");
  } finally {
    setBusy(false);
    messageInput.focus();
  }
}

function resetChat() {
  sessionId = newSessionId();
  saveSessionId();
  messages.replaceChildren();
  setSessionLabel();
  meta.textContent = "Prompt cache key: generated per chat session; it is not a credential.";
  addMessage("assistant", "New chat ready. Ask me something, or ask for the current time to exercise getCurrentTime.");
  messageInput.focus();
}

form.addEventListener("submit", sendMessage);
resetButton.addEventListener("click", resetChat);
messageInput.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    form.requestSubmit();
  }
});

setSessionLabel();
addMessage("assistant", "Ask me something, or ask for the current time to exercise getCurrentTime.");
