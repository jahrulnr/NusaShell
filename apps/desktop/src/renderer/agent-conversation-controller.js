import {
  buildAgentContext,
  composerTextareaSize,
  describeToolActivity,
  formatMessageTimestamp,
  mergeCompactionCheckpoint,
  renderAssistantMarkdown,
  renderReasoningMarkdown,
  searchConversations,
} from "./agent-conversation-ui.js";
import { estimateContextTokens, formatContextUsage } from "./ai-model-ui.js";
import { inspectAttachmentContent, toDataUrl } from "./attachment-content.js";

export class AgentConversationController {
  constructor({ shell, runTurn, cancelTurn, getActiveModel, getVisionMode, notify, log }) {
    this.shell = shell;
    this.runTurn = runTurn;
    this.cancelTurn = cancelTurn;
    this.getActiveModel = getActiveModel;
    this.getVisionMode = getVisionMode;
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
    this.composerInputWidth = 0;
    this.composerResizeObserver = null;
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
        const savedMessage = this.conversation.messages.at(-1);
        this.appendMessage("user", text, savedMessage ?? { attachments });
        input.value = "";
        this.resizeComposerInput();
        this.attachments = [];
        this.renderAttachments();
        await this.refresh();
      }
      retryIsSafe = true;

      pending = this.createStreamingMessage();
      selectedModel = this.getActiveModel();
      status.textContent = selectedModel ? formatContextUsage(estimateContextTokens(buildAgentContext(this.conversation)), selectedModel.contextWindow) : "Choose a model";
      const streamState = {
        reasoningEl: null,
        reasoningText: "",
        toolCards: new Map(),
        textBubble: pending?.querySelector(".agent-bubble"),
        streamedText: "",
        sawToolCallEnd: false,
      };
      const result = await this.runTurn(buildAgentContext(this.conversation), {
        traceId: this.activeTraceId,
        onDelta: (delta) => {
          streamState.streamedText += delta;
          if (streamState.textBubble) streamState.textBubble.textContent = streamState.streamedText;
        },
        onReasoningDelta: (delta) => {
          if (streamState.reasoningEl && streamState.sawToolCallEnd) {
            streamState.reasoningEl = null;
            streamState.reasoningText = "";
            streamState.sawToolCallEnd = false;
          }
          streamState.reasoningText += delta;
          if (!streamState.reasoningEl) {
            streamState.reasoningEl = this.createStreamingReasoningBlock();
            streamState.textBubble?.before(streamState.reasoningEl);
          }
          const content = streamState.reasoningEl.querySelector(".agent-reasoning-content");
          if (content) content.textContent = streamState.reasoningText;
        },
        onToolCallStart: (payload) => {
          const card = this.createStreamingToolCard(payload.callId, payload.name);
          streamState.toolCards.set(payload.callId, card);
          streamState.textBubble?.before(card);
        },
        onToolCallEnd: (payload) => {
          const card = streamState.toolCards.get(payload.callId);
          if (card) this.updateStreamingToolCard(card, payload);
          streamState.sawToolCallEnd = true;
        },
      });
      retryIsSafe = false;

      try {
        this.conversation = await this.shell.agentConversations.append(this.conversation.id, {
          role: "assistant",
          content: result.text,
          traceId: result.traceId,
          model: result.model,
          rounds: result.rounds,
          reasoning: result.reasoning,
          toolCalls: result.toolCalls,
          ...(result.steps ? { steps: result.steps } : {}),
        });
      } catch (error) {
        this.sealStreamingMessage(pending, result);
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
      const savedMessage = this.conversation.messages.at(-1);
      this.sealStreamingMessage(pending, savedMessage ?? result);
      await this.refresh();
      status.textContent = selectedModel ? formatContextUsage(estimateContextTokens(buildAgentContext(this.conversation)), selectedModel.contextWindow) : "Choose a model";
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
    const input = $("#agent-input");
    input.addEventListener("input", () => this.resizeComposerInput());
    input.addEventListener("keydown", (event) => {
      if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
        event.preventDefault();
        void this.submit();
      }
    });
    this.composerResizeObserver = new ResizeObserver(([entry]) => {
      if (!entry || entry.contentRect.width === this.composerInputWidth) return;
      this.composerInputWidth = entry.contentRect.width;
      this.resizeComposerInput();
    });
    this.composerResizeObserver.observe(input);
    this.resizeComposerInput();
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
      const bytes = new Uint8Array(await file.arrayBuffer());
      const attachment = inspectAttachmentContent(bytes);
      if (!attachment) {
        this.notify(`${file.name} is not a supported image, PDF, or UTF-8 text file.`, "error");
        continue;
      }
      const selectedModel = this.getActiveModel();
      const mode = attachment.kind;
      const visionMode = this.getVisionMode?.() ?? "auto";
      const advertisedModes = selectedModel?.inputModes ?? [];
      const supported = mode === "text"
        || (mode === "image"
          ? visionMode !== "off"
          : advertisedModes.some((item) => ["file", "pdf", "document"].includes(item)));
      if (!supported) {
        const reason = mode === "image"
          ? "has image input disabled in Agent runtime settings."
          : "does not advertise document input support.";
        this.notify(`${selectedModel?.id || "Selected model"} ${reason}`, "error");
        continue;
      }
      this.attachments.push(attachment.kind === "text"
        ? { type: "text", content: attachment.content, mediaType: attachment.mediaType, name: file.name }
        : { type: attachment.kind, dataUrl: toDataUrl(bytes, attachment.mediaType), mediaType: attachment.mediaType, name: file.name });
    }
    this.renderAttachments();
  }

  renderAttachments() {
    const list = $("#agent-attachments");
    list.textContent = "";
    this.attachments.forEach((attachment, index) => {
      const chip = element("span", "agent-attachment");
      const kind = attachment.type === "image" ? "IMG" : attachment.type === "file" ? "PDF" : "TXT";
      chip.appendChild(element("span", "agent-attachment-name", `${kind} · ${attachment.name}`));
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

  resizeComposerInput() {
    const input = $("#agent-input");
    if (!input) return;
    input.style.height = "auto";
    const style = window.getComputedStyle(input);
    const size = composerTextareaSize({
      scrollHeight: input.scrollHeight,
      lineHeight: Number.parseFloat(style.lineHeight) || 19,
      paddingTop: Number.parseFloat(style.paddingTop) || 0,
      paddingBottom: Number.parseFloat(style.paddingBottom) || 0,
    });
    input.style.height = `${size.height}px`;
    input.style.overflowY = size.overflowY;
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
    this.updateContextStatus();
  }

  updateContextStatus() {
    const status = $("#agent-provider-status");
    const selectedModel = this.getActiveModel();
    if (!status || !selectedModel) return;
    status.textContent = formatContextUsage(estimateContextTokens(buildAgentContext(this.conversation)), selectedModel.contextWindow);
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

  scrollToBottom() {
    const thread = $("#agent-thread");
    if (!thread) return;
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        thread.scrollTop = thread.scrollHeight;
      });
    });
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
    this.scrollToBottom();
  }

  appendMessage(role, content, meta = {}) {
    const thread = $("#agent-thread");
    if (!thread) return null;
    $("#agent-empty")?.remove();
    const message = element("article", `agent-message ${role}${meta.pending ? " agent-pending" : ""}${meta.error ? " agent-message-error" : ""}`);
    message.setAttribute("aria-label", role === "user" ? "Your message" : "NusaShell Agent response");

    if (role === "assistant") {
      const identity = element("div", "agent-message-identity");
      identity.append(
        element("span", "agent-message-mark", meta.pending ? "◌" : "✦"),
        element("span", "agent-message-meta", meta.pending ? "Working" : "NusaShell Agent"),
      );
      message.appendChild(identity);
    }

    if (meta.attachments?.length) {
      message.appendChild(this.messageAttachments(meta.attachments));
    }

    if (role === "assistant" && meta.steps?.length) {
      for (const step of meta.steps) {
        if (step.type === "reasoning" && step.content?.trim()) {
          message.appendChild(this.reasoningDisclosure(step.content));
        } else if (step.type === "tool_calls" && step.calls?.length) {
          message.appendChild(this.toolActivity(step.calls));
        } else if (step.type === "text" && step.content) {
          const stepBubble = element("div", "agent-bubble");
          stepBubble.innerHTML = renderAssistantMarkdown(step.content);
          message.appendChild(stepBubble);
        }
      }
    } else {
      if (role === "assistant" && meta.reasoning?.trim()) {
        message.appendChild(this.reasoningDisclosure(meta.reasoning));
      }

      if (role === "assistant" && meta.toolCalls?.length) {
        message.appendChild(this.toolActivity(meta.toolCalls));
      }

      const bubble = element("div", "agent-bubble");
      const text = content || (meta.attachments?.length ? "Attached files" : "");
      if (role === "assistant" && !meta.pending && !meta.error) bubble.innerHTML = renderAssistantMarkdown(text);
      else bubble.textContent = text;
      message.appendChild(bubble);
    }

    const footer = element("footer", "agent-message-footer");
    const timestamp = formatMessageTimestamp(meta.createdAt);
    if (timestamp) {
      const time = element("time", "agent-message-time", timestamp);
      time.dateTime = meta.createdAt;
      footer.appendChild(time);
    }
    if (role === "assistant" && meta.model) footer.appendChild(messageDetail(meta.model));
    if (role === "assistant" && meta.rounds) footer.appendChild(messageDetail(`${meta.rounds} round${meta.rounds === 1 ? "" : "s"}`));
    if (role === "assistant" && meta.traceId) footer.appendChild(messageDetail(`trace ${meta.traceId.slice(0, 8)}`));

    const actions = element("div", "agent-message-actions");
    const copy = iconButton("Copy message", copyIcon());
    copy.addEventListener("click", () => void this.copyMessage(
      content || meta.attachments?.map((attachment) => attachment.name).join("\n") || "",
      copy,
    ));
    actions.appendChild(copy);
    if (meta.retry) {
      const retry = element("button", "agent-retry-btn", "Retry");
      retry.type = "button";
      retry.addEventListener("click", () => void this.submit({ retry: true }));
      actions.prepend(retry);
    }
    footer.appendChild(actions);
    message.appendChild(footer);

    thread.appendChild(message);
    thread.scrollTop = thread.scrollHeight;
    return message;
  }

  messageAttachments(attachments) {
    const gallery = element("div", "agent-message-attachments");
    gallery.setAttribute("aria-label", `${attachments.length} attachment${attachments.length === 1 ? "" : "s"}`);
    attachments.forEach((attachment) => {
      if (attachment.type === "image") {
        const figure = element("figure", "agent-message-attachment agent-message-image");
        const image = document.createElement("img");
        image.src = attachment.dataUrl;
        image.alt = attachment.name;
        image.loading = "lazy";
        figure.append(image, element("figcaption", "", attachment.name));
        gallery.appendChild(figure);
        return;
      }
      const file = element("div", "agent-message-attachment agent-message-file");
      file.append(
        element("span", "agent-message-file-kind", attachment.type === "file" ? "PDF" : "TXT"),
        element("span", "agent-message-file-name", attachment.name),
      );
      gallery.appendChild(file);
    });
    return gallery;
  }

  reasoningDisclosure(reasoning) {
    const disclosure = document.createElement("details");
    disclosure.className = "agent-reasoning";
    const summary = document.createElement("summary");
    summary.append(
      element("span", "agent-reasoning-mark", "⌁"),
      element("span", "agent-reasoning-title", "Thinking"),
      element("span", "agent-reasoning-hint", "Show reasoning"),
      element("span", "agent-reasoning-chevron", "⌄"),
    );
    const content = element("div", "agent-reasoning-content");
    content.innerHTML = renderReasoningMarkdown(reasoning);
    disclosure.addEventListener("toggle", () => {
      const hint = disclosure.querySelector(".agent-reasoning-hint");
      if (hint) hint.textContent = disclosure.open ? "Hide reasoning" : "Show reasoning";
    });
    disclosure.append(summary, content);
    return disclosure;
  }

  toolActivity(toolCalls) {
    const activity = document.createElement("details");
    activity.className = "agent-activity";
    activity.open = toolCalls.length <= 6 || toolCalls.some((call) => !call.ok);
    const summary = document.createElement("summary");
    const activitySummary = describeToolActivity(toolCalls);
    summary.append(
      element("span", "agent-activity-terminal", "›_"),
      element("span", "agent-activity-title", activitySummary.label),
      element(
        "span",
        `agent-activity-status${activitySummary.failed ? " has-errors" : ""}`,
        activitySummary.failed
          ? `${activitySummary.succeeded} complete · ${activitySummary.failed} failed`
          : "Completed",
      ),
      element("span", "agent-activity-chevron", "⌄"),
    );
    const list = element("ol", "agent-activity-list");
    toolCalls.forEach((toolCall) => {
      const item = element("li", `agent-activity-item ${toolCall.ok ? "is-success" : "is-error"}`);
      const state = element("span", "agent-activity-state", toolCall.ok ? "✓" : "!");
      const copy = element("div", "agent-activity-copy");
      copy.appendChild(element("code", "agent-activity-name", toolCall.name));
      if (toolCall.error) copy.appendChild(element("span", "agent-activity-error", toolCall.error));
      item.append(state, copy);
      list.appendChild(item);
    });
    activity.append(summary, list);
    return activity;
  }

  createStreamingMessage() {
    const thread = $("#agent-thread");
    if (!thread) return null;
    $("#agent-empty")?.remove();
    const message = element("article", "agent-message assistant agent-pending");
    message.setAttribute("aria-label", "NusaShell Agent response");
    const identity = element("div", "agent-message-identity");
    identity.append(
      element("span", "agent-message-mark", "◌"),
      element("span", "agent-message-meta", "Working"),
    );
    message.appendChild(identity);
    message.appendChild(element("div", "agent-bubble"));
    thread.appendChild(message);
    thread.scrollTop = thread.scrollHeight;
    return message;
  }

  createStreamingReasoningBlock() {
    const disclosure = document.createElement("details");
    disclosure.className = "agent-reasoning";
    disclosure.open = true;
    const summary = document.createElement("summary");
    summary.append(
      element("span", "agent-reasoning-mark", "⌁"),
      element("span", "agent-reasoning-title", "Thinking"),
      element("span", "agent-reasoning-hint", "Hide reasoning"),
      element("span", "agent-reasoning-chevron", "⌄"),
    );
    const content = element("div", "agent-reasoning-content");
    disclosure.append(summary, content);
    return disclosure;
  }

  createStreamingToolCard(callId, name) {
    const card = element("div", "agent-tool-card is-running");
    card.dataset.callId = callId;
    const state = element("span", "agent-tool-card-state", "◌");
    const copy = element("div", "agent-tool-card-copy");
    copy.appendChild(element("code", "agent-tool-card-name", name));
    card.append(state, copy);
    return card;
  }

  updateStreamingToolCard(card, payload) {
    card.classList.remove("is-running");
    card.classList.add(payload.ok ? "is-success" : "is-error");
    const state = card.querySelector(".agent-tool-card-state");
    if (state) state.textContent = payload.ok ? "✓" : "!";
    if (payload.error) {
      const copy = card.querySelector(".agent-tool-card-copy");
      if (copy) copy.appendChild(element("span", "agent-activity-error", payload.error));
    }
  }

  sealStreamingMessage(message, meta) {
    if (!message) return;
    message.classList.remove("agent-pending");
    const mark = message.querySelector(".agent-message-mark");
    if (mark) mark.textContent = "✦";
    const metaLabel = message.querySelector(".agent-message-meta");
    if (metaLabel) metaLabel.textContent = "NusaShell Agent";

    if (!message.querySelector(".agent-reasoning") && meta.reasoning?.trim()) {
      const disclosure = this.reasoningDisclosure(meta.reasoning);
      const bubble = message.querySelector(".agent-bubble");
      if (bubble) bubble.before(disclosure);
      else message.appendChild(disclosure);
    }

    if (!message.querySelector(".agent-tool-card, .agent-activity") && meta.toolCalls?.length) {
      const activity = this.toolActivity(meta.toolCalls);
      const bubble = message.querySelector(".agent-bubble");
      if (bubble) bubble.before(activity);
      else message.appendChild(activity);
    }

    const bubble = message.querySelector(".agent-bubble");
    if (bubble) {
      const content = bubble.textContent || meta.text || meta.content || "";
      if (content) bubble.innerHTML = renderAssistantMarkdown(content);
    }

    const footer = element("footer", "agent-message-footer");
    const timestamp = formatMessageTimestamp(meta.createdAt ?? new Date().toISOString());
    if (timestamp) {
      const time = element("time", "agent-message-time", timestamp);
      time.dateTime = meta.createdAt ?? new Date().toISOString();
      footer.appendChild(time);
    }
    if (meta.model) footer.appendChild(messageDetail(meta.model));
    if (meta.rounds) footer.appendChild(messageDetail(`${meta.rounds} round${meta.rounds === 1 ? "" : "s"}`));
    if (meta.traceId) footer.appendChild(messageDetail(`trace ${meta.traceId.slice(0, 8)}`));
    const actions = element("div", "agent-message-actions");
    const copy = iconButton("Copy message", copyIcon());
    copy.addEventListener("click", () => void this.copyMessage(
      bubble?.textContent || "",
      copy,
    ));
    actions.appendChild(copy);
    footer.appendChild(actions);
    message.appendChild(footer);
  }

  async copyMessage(content, button) {
    try {
      if (this.shell?.clipboard?.writeText) this.shell.clipboard.writeText(content);
      else if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(content);
      else throw new Error("Clipboard is unavailable");
      button.classList.add("is-confirmed");
      button.setAttribute("aria-label", "Message copied");
      button.title = "Copied";
      window.setTimeout(() => {
        button.classList.remove("is-confirmed");
        button.setAttribute("aria-label", "Copy message");
        button.title = "Copy message";
      }, 1200);
    } catch (error) {
      this.notify(`Could not copy message: ${error.message || error}`, "error");
    }
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

function messageDetail(content) {
  return element("span", "agent-message-detail", content);
}

function iconButton(label, icon) {
  const button = element("button", "agent-message-action");
  button.type = "button";
  button.setAttribute("aria-label", label);
  button.title = label;
  button.innerHTML = icon;
  return button;
}

function copyIcon() {
  return '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2" stroke="currentColor" stroke-width="1.6"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2" stroke="currentColor" stroke-width="1.6"/></svg>';
}

function formatTime(timestamp) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
