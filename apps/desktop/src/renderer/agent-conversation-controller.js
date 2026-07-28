import { buildAgentContext, mergeCompactionCheckpoint, searchConversations } from "./agent-conversation-ui.js";

export class AgentConversationController {
  constructor({ shell, runTurn, cancelTurn, getActiveModel, notify, log }) {
    this.shell = shell;
    this.runTurn = runTurn;
    this.cancelTurn = cancelTurn;
    this.getActiveModel = getActiveModel;
    this.notify = notify;
    this.log = log;
    this.conversation = null;
    this.conversations = [];
    this.activeId = "";
    this.pendingDeleteId = "";
    this.turnPending = false;
    this.activeTraceId = "";
    this.failedMessage = null;
    this.attachments = [];
  }

  async initialize() {
    if (!this.shell?.agentConversations) {
      this.notify("Conversation storage is unavailable. Restart NusaShell after rebuilding the preload.", "error");
      return;
    }
    this.bindEvents();
    await this.refresh();
    if (this.conversations.length === 0) await this.create();
    else await this.open(this.conversations[0].id);
  }

  renderList() {
    const list = $("#agent-conversation-list");
    const count = $("#agent-conversation-count");
    if (!list || !count) return;
    count.textContent = `${this.conversations.length} thread${this.conversations.length === 1 ? "" : "s"}`;
    list.textContent = "";
    const visible = searchConversations(this.conversations, $("#agent-conversation-search")?.value);
    if (visible.length === 0) {
      list.appendChild(element("div", "agent-conversation-empty", this.conversations.length ? "No conversations match this search." : "No conversations yet."));
      return;
    }
    visible.forEach((conversation) => list.appendChild(this.conversationRow(conversation)));
  }

  async create() {
    if (this.turnPending) return;
    if (this.conversation?.messages.length === 0) {
      $("#agent-input")?.focus();
      return;
    }
    this.conversation = await this.shell.agentConversations.create();
    this.activeId = this.conversation.id;
    this.renderThread();
    await this.refresh();
    $("#agent-input")?.focus();
  }

  async submit({ retry = false } = {}) {
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    const stopButton = $("#agent-stop-btn");
    const status = $("#agent-provider-status");
    const text = input?.value.trim();
    if ((!retry && !text && this.attachments.length === 0) || this.turnPending) return;
    if (!this.conversation) await this.create();

    let pending = null;
    let selectedModel = null;
    let retryIsSafe = false;
    try {
      this.turnPending = true;
      this.activeTraceId = crypto.randomUUID();
      input.disabled = true;
      sendButton.disabled = true;
      stopButton.hidden = false;
      this.failedMessage?.remove();
      this.failedMessage = null;
      if (!retry) {
        const attachments = [...this.attachments];
        this.conversation = await this.shell.agentConversations.append(this.conversation.id, {
          role: "user",
          content: text,
          ...(attachments.length ? { attachments } : {}),
        });
        this.appendMessage("user", text, { attachments });
        input.value = "";
        this.attachments = [];
        this.renderAttachments();
        await this.refresh();
      }
      retryIsSafe = true;

      pending = this.appendMessage("assistant", retry ? "Retrying turn…" : "Working…", { pending: true });
      selectedModel = this.getActiveModel();
      status.textContent = selectedModel ? `${selectedModel.providerName} · ${selectedModel.id}` : "Choose a model";
      const bubble = pending?.querySelector(".agent-bubble");
      let streamedText = "";
      const result = await this.runTurn(buildAgentContext(this.conversation), {
        traceId: this.activeTraceId,
        onDelta: (delta) => {
          streamedText += delta;
          if (bubble) bubble.textContent = streamedText;
        },
      });
      retryIsSafe = false;
      pending?.remove();

      try {
        this.conversation = await this.shell.agentConversations.append(this.conversation.id, {
          role: "assistant",
          content: result.text,
          traceId: result.traceId,
          model: result.model,
          rounds: result.rounds,
          toolCalls: result.toolCalls,
        });
      } catch (error) {
        this.appendMessage("assistant", result.text, result);
        status.textContent = "Response completed · local save failed";
        this.notify("The response completed but could not be saved locally.", "error");
        this.log("error", `Agent response persistence failed trace=${result.traceId}: ${error.message || String(error)}`);
        return;
      }
      if (result.compaction) {
        try {
          const checkpoint = mergeCompactionCheckpoint(
            this.conversation.checkpoint,
            result.compaction,
            this.conversation.messages.length,
          );
          this.conversation = await this.shell.agentConversations.saveCheckpoint(this.conversation.id, checkpoint);
        } catch (error) {
          this.log("error", `Agent checkpoint persistence failed trace=${result.traceId}: ${error.message || String(error)}`);
        }
      }
      this.appendMessage("assistant", result.text, result);
      await this.refresh();
      status.textContent = `${selectedModel?.providerName || "Provider"} · ${result.rounds} round${result.rounds === 1 ? "" : "s"}`;
      this.log("info", `Agent turn completed trace=${result.traceId} rounds=${result.rounds}`);
    } catch (error) {
      pending?.remove();
      if (error.code === "AGENT_TURN_CANCELLED") {
        this.appendMessage("assistant", "Turn stopped.", { error: true });
        status.textContent = "Turn stopped";
        this.log("info", `Agent turn stopped trace=${this.activeTraceId}`);
        return;
      }
      this.failedMessage = this.appendMessage(
        "assistant",
        `Turn failed: ${error.message || "Unknown error"}`,
        { error: true, retry: retryIsSafe },
      );
      status.textContent = retryIsSafe ? "Turn failed · ready to retry" : "Local conversation error";
      this.log("error", `Agent turn failed: ${error.message || String(error)}`);
    } finally {
      this.turnPending = false;
      this.activeTraceId = "";
      input.disabled = false;
      sendButton.disabled = false;
      stopButton.hidden = true;
      input.focus();
    }
  }

  closeDeleteDialog() {
    this.pendingDeleteId = "";
    $("#agent-delete-overlay").hidden = true;
    $("#agent-delete-dialog").hidden = true;
  }

  bindEvents() {
    $("#agent-form").addEventListener("submit", (event) => {
      event.preventDefault();
      void this.submit();
    });
    $("#agent-input").addEventListener("keydown", (event) => {
      if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
        event.preventDefault();
        void this.submit();
      }
    });
    $("#agent-stop-btn").addEventListener("click", () => void this.stop());
    $("#agent-attach-btn").addEventListener("click", () => $("#agent-file-input").click());
    $("#agent-file-input").addEventListener("change", (event) => void this.addAttachments(event.target.files));
    $("#agent-new-conversation").addEventListener("click", () => this.runUiAction(this.create(), "Could not create conversation"));
    $("#agent-conversation-search").addEventListener("input", () => this.renderList());
    $("#agent-delete-overlay").addEventListener("click", () => this.closeDeleteDialog());
    $("#agent-delete-close").addEventListener("click", () => this.closeDeleteDialog());
    $("#agent-delete-cancel").addEventListener("click", () => this.closeDeleteDialog());
    $("#agent-delete-confirm").addEventListener("click", () => this.runUiAction(this.deletePending(), "Could not delete conversation"));
  }

  async addAttachments(fileList) {
    const files = [...(fileList ?? [])];
    $("#agent-file-input").value = "";
    for (const file of files) {
      if (this.attachments.length >= 4) {
        this.notify("A turn can include up to 4 attachments.", "error");
        break;
      }
      if (file.size > 4 * 1024 * 1024) {
        this.notify(`${file.name} is larger than 4 MiB.`, "error");
        continue;
      }
      if (!file.type.startsWith("image/") && file.type !== "application/pdf") {
        this.notify(`${file.name} is not a supported image or PDF.`, "error");
        continue;
      }
      const selectedModel = this.getActiveModel();
      const mode = file.type.startsWith("image/") ? "image" : "file";
      const advertisedModes = selectedModel?.inputModes ?? [];
      const supported = mode === "image"
        ? advertisedModes.includes("image")
        : advertisedModes.some((item) => ["file", "pdf", "document"].includes(item));
      if (advertisedModes.length && !supported) {
        this.notify(`${selectedModel.id} does not advertise ${mode === "image" ? "image" : "document"} input support.`, "error");
        continue;
      }
      this.attachments.push({
        type: mode,
        dataUrl: await readDataUrl(file),
        mediaType: file.type,
        name: file.name,
      });
    }
    this.renderAttachments();
  }

  renderAttachments() {
    const list = $("#agent-attachments");
    list.textContent = "";
    this.attachments.forEach((attachment, index) => {
      const chip = element("span", "agent-attachment");
      chip.appendChild(element("span", "agent-attachment-name", `${attachment.type === "image" ? "IMG" : "PDF"} · ${attachment.name}`));
      const remove = element("button", "agent-attachment-remove", "×");
      remove.type = "button";
      remove.setAttribute("aria-label", `Remove ${attachment.name}`);
      remove.addEventListener("click", () => {
        this.attachments.splice(index, 1);
        this.renderAttachments();
      });
      chip.appendChild(remove);
      list.appendChild(chip);
    });
  }

  async stop() {
    if (!this.turnPending || !this.activeTraceId || !this.cancelTurn) return;
    const button = $("#agent-stop-btn");
    button.disabled = true;
    button.textContent = "Stopping…";
    try {
      await this.cancelTurn(this.activeTraceId);
    } catch (error) {
      this.notify(`Could not stop the turn: ${error.message || error}`, "error");
    } finally {
      button.disabled = false;
      button.textContent = "Stop";
    }
  }

  async refresh() {
    this.conversations = [...await this.shell.agentConversations.list()];
    this.renderList();
  }

  async open(conversationId) {
    if (this.turnPending || conversationId === this.activeId && this.conversation) return;
    const conversation = await this.shell.agentConversations.get(conversationId);
    if (!conversation) {
      await this.refresh();
      return;
    }
    this.conversation = conversation;
    this.activeId = conversation.id;
    this.renderThread();
    this.renderList();
  }

  conversationRow(conversation) {
    const row = element("div", `agent-conversation-item${conversation.id === this.activeId ? " is-active" : ""}`);
    row.setAttribute("role", "listitem");
    const open = element("button", "agent-conversation-open");
    open.type = "button";
    const title = element("span", "agent-conversation-title");
    title.textContent = conversation.title;
    const time = element("span", "agent-conversation-time");
    time.textContent = `${formatTime(conversation.updatedAt)} · ${conversation.messageCount} message${conversation.messageCount === 1 ? "" : "s"}`;
    open.append(title, time);
    open.addEventListener("click", () => this.runUiAction(this.open(conversation.id), "Could not open conversation"));
    const remove = element("button", "agent-conversation-delete", "×");
    remove.type = "button";
    remove.setAttribute("aria-label", `Delete ${conversation.title}`);
    remove.addEventListener("click", () => this.openDeleteDialog(conversation.id));
    row.append(open, remove);
    return row;
  }

  renderThread() {
    const thread = $("#agent-thread");
    if (!thread) return;
    thread.textContent = "";
    this.failedMessage = null;
    if (!this.conversation?.messages.length) {
      const empty = element("div", "agent-empty");
      empty.id = "agent-empty";
      empty.innerHTML = "<div class=\"agent-empty-mark\">✦</div><h2>Start a conversation</h2><p>Choose a configured model. The agent can discover and start MCP servers when the task needs them.</p>";
      thread.appendChild(empty);
      return;
    }
    this.conversation.messages.forEach((message) => this.appendMessage(message.role, message.content, message));
  }

  appendMessage(role, content, meta = {}) {
    const thread = $("#agent-thread");
    if (!thread) return null;
    $("#agent-empty")?.remove();
    const message = element("article", `agent-message ${role}${meta.pending ? " agent-pending" : ""}${meta.error ? " agent-message-error" : ""}`);
    const label = element("div", "agent-message-meta");
    label.textContent = role === "user" ? "YOU" : (meta.pending ? "AGENT · WORKING" : "NUSASHELL AGENT");
    const bubble = element("div", "agent-bubble");
    bubble.textContent = content || (meta.attachments?.length ? "Attached files" : "");
    message.append(label, bubble);
    if (meta.attachments?.length) {
      const attachmentList = element("div", "agent-turn-meta");
      meta.attachments.forEach((attachment) => attachmentList.appendChild(tag(`${attachment.type === "image" ? "IMG" : "PDF"} · ${attachment.name}`)));
      message.appendChild(attachmentList);
    }

    if (meta.traceId || meta.model || meta.toolCalls?.length) {
      const details = element("div", "agent-turn-meta");
      if (meta.model) details.appendChild(tag(meta.model));
      if (meta.traceId) details.appendChild(tag(`trace ${meta.traceId.slice(0, 8)}`));
      for (const toolCall of meta.toolCalls ?? []) {
        details.appendChild(tag(`${toolCall.ok ? "✓" : "!"} ${toolCall.name}`, "agent-tool-result"));
      }
      message.appendChild(details);
    }
    if (meta.retry) {
      const retry = element("button", "agent-retry-btn", "Retry turn");
      retry.type = "button";
      retry.addEventListener("click", () => void this.submit({ retry: true }));
      message.appendChild(retry);
    }
    thread.appendChild(message);
    thread.scrollTop = thread.scrollHeight;
    return message;
  }

  openDeleteDialog(conversationId) {
    const conversation = this.conversations.find((item) => item.id === conversationId);
    if (!conversation) return;
    this.pendingDeleteId = conversationId;
    $("#agent-delete-copy").textContent = `“${conversation.title}” will be permanently removed from this device.`;
    $("#agent-delete-overlay").hidden = false;
    $("#agent-delete-dialog").hidden = false;
    $("#agent-delete-confirm").focus();
  }

  async deletePending() {
    if (!this.pendingDeleteId) return;
    const deletedId = this.pendingDeleteId;
    this.closeDeleteDialog();
    await this.shell.agentConversations.delete(deletedId);
    if (this.activeId === deletedId) {
      this.conversation = null;
      this.activeId = "";
    }
    await this.refresh();
    if (!this.conversation) {
      if (this.conversations.length) await this.open(this.conversations[0].id);
      else await this.create();
    }
    this.notify("Conversation deleted.", "success");
  }

  runUiAction(operation, message) {
    void operation.catch((error) => {
      this.notify(`${message}: ${error.message || error}`, "error");
      this.log("error", `${message}: ${error.message || String(error)}`);
    });
  }
}

function $(selector) {
  return document.querySelector(selector);
}

function element(tagName, className, content) {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

function tag(content, extraClass = "") {
  return element("span", `agent-turn-tag${extraClass ? ` ${extraClass}` : ""}`, content);
}

function formatTime(timestamp) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function readDataUrl(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(new Error(`Could not read ${file.name}`));
    reader.readAsDataURL(file);
  });
}
