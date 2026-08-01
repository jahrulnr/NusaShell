import {
  buildAgentContext,
  composerTextareaSize,
  formatMessageTimestamp,
  formatToolOutput,
  formatToolTerminalInput,
  mergeCompactionCheckpoint,
  renderAssistantMarkdown,
  renderReasoningMarkdown,
  renderToolCodeHtml,
  sanitizeAssistantSteps,
  searchConversations,
  summarizeToolArgs,
  toConversationToolCall,
} from "./agent-conversation-ui.js";
import { estimateContextTokens, formatContextUsage } from "./ai-model-ui.js";
import { inspectAttachmentContent, toDataUrl } from "./attachment-content.js";

export class AgentConversationController {
  constructor({ shell, runTurn, cancelTurn, answerAsk, getActiveModel, getVisionMode, notify, log, runAcpTurn, cancelAcpTurn, answerAcpPermission, answerAcpAsk, getAcpSessionInfo, setAcpConfigOption, ensureAcpSession, refreshModelPicker }) {
    this.shell = shell;
    this.runTurn = runTurn;
    this.cancelTurn = cancelTurn;
    this.answerAsk = answerAsk;
    this.getActiveModel = getActiveModel;
    this.getVisionMode = getVisionMode;
    this.notify = notify;
    this.log = log;
    this.runAcpTurn = runAcpTurn;
    this.cancelAcpTurn = cancelAcpTurn;
    this.answerAcpPermission = answerAcpPermission;
    this.answerAcpAsk = answerAcpAsk;
    this.getAcpSessionInfo = getAcpSessionInfo;
    this.setAcpConfigOption = setAcpConfigOption;
    this.ensureAcpSession = ensureAcpSession;
    this.refreshModelPicker = refreshModelPicker;
    this.acpConfigOptions = [];
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

  async renderAcpMenu(menu, forceClose) {
    if (forceClose) {
      menu.hidden = true;
      return;
    }
    let providers = [];
    try {
      providers = [...await this.shell.acpProviders.list()];
    } catch (error) {
      this.notify(`Could not load ACP providers: ${error.message || error}`, "error");
    }
    menu.textContent = "";
    const connected = providers.filter((p) => p.config.enabled && p.config.authStatus === "connected");
    if (connected.length === 0) {
      const item = element("button", "disabled", "No connected ACP agents — connect one in AI Providers");
      item.type = "button";
      item.disabled = true;
      menu.appendChild(item);
    } else {
      for (const provider of connected) {
        const item = element("button", "", provider.manifest.displayName);
        item.type = "button";
        item.addEventListener("click", () => {
          menu.hidden = true;
          void this.runUiAction(this.createAcp(provider.manifest.id), `Could not create ACP conversation`);
        });
        menu.appendChild(item);
      }
    }
    menu.hidden = false;
  }

  async create(options) {
    if (this.turnPending) return;
    if (this.conversation?.messages.length === 0 && !options) {
      $("#agent-input")?.focus();
      return;
    }
    this.conversation = await this.shell.agentConversations.create(options);
    this.activeId = this.conversation.id;
    this.renderThread();
    this.updateWorkspaceLabel();
    this.updateContextStatus();
    this.updateAcpStatus();
    await this.refresh();
    $("#agent-input")?.focus();
  }

  async createAcp(providerId) {
    await this.create({ kind: "acp", acp: { providerId } });
  }

  async submit({ retry = false } = {}) {
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    const stopButton = $("#agent-stop-btn");
    const status = $("#agent-provider-status");
    const text = input?.value.trim();
    if ((!retry && !text && this.attachments.length === 0) || this.turnPending) return;
    if (!this.conversation) await this.create();

    if (this.conversation?.kind === "acp") {
      return await this.submitAcp({ text, retry });
    }

    let pending = null;
    let selectedModel = null;
    let retryIsSafe = false;
    let streamState = null;
    let turnEndResolve = null;
    const turnEndPromise = new Promise((resolve) => { turnEndResolve = resolve; });
    let turnEnded = false;
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

      const lastDurable = this.conversation?.messages.at(-1);
      const resumeFrom = retry && lastDurable?.status === "interrupted" && Array.isArray(lastDurable.resumeMessages)
        ? lastDurable
        : null;

      pending = this.createStreamingMessage();
      selectedModel = this.getActiveModel();
      const turnMessages = resumeFrom ? resumeFrom.resumeMessages : buildAgentContext(this.conversation);
      const baseTokens = estimateContextTokens(turnMessages);
      let liveTokens = baseTokens;
      const setContextStatus = (tokens) => {
        liveTokens = Math.max(liveTokens, tokens);
        if (selectedModel) status.textContent = formatContextUsage(liveTokens, selectedModel.contextWindow);
      };
      setContextStatus(baseTokens);
      streamState = {
        message: pending,
        lastKind: null,
        reasoningEl: null,
        reasoningText: "",
        toolCards: new Map(),
        textBubble: null,
        streamedText: "",
      };
      const appendStreamChild = (node) => {
        streamState.message?.appendChild(node);
        this.scrollToBottom();
      };
      const result = await this.runTurn(turnMessages, {
        traceId: this.activeTraceId,
        workspace: this.conversation?.workspace,
        ...(resumeFrom ? { resume: true } : {}),
        onDelta: (delta) => {
          // Append in arrival order. A new text segment starts after reasoning/tools.
          if (streamState.lastKind !== "text") {
            streamState.textBubble = element("div", "agent-bubble");
            streamState.streamedText = "";
            appendStreamChild(streamState.textBubble);
            streamState.lastKind = "text";
          }
          streamState.streamedText += delta;
          if (streamState.textBubble) {
            streamState.textBubble.innerHTML = renderAssistantMarkdown(streamState.streamedText);
          }
          setContextStatus(liveTokens + Math.ceil(delta.length / 4));
        },
        onReasoningDelta: (delta) => {
          if (streamState.lastKind !== "reasoning") {
            streamState.reasoningEl = this.createStreamingReasoningBlock();
            streamState.reasoningText = "";
            appendStreamChild(streamState.reasoningEl);
            streamState.lastKind = "reasoning";
          }
          streamState.reasoningText += delta;
          const content = streamState.reasoningEl?.querySelector(".agent-reasoning-content");
          if (content) content.innerHTML = renderReasoningMarkdown(streamState.reasoningText);
          setContextStatus(liveTokens + Math.ceil(delta.length / 4));
        },
        onToolCallStart: (payload) => {
          streamState.lastKind = "tool";
          const card = this.createStreamingToolCard(payload.callId, payload.name, payload.args);
          streamState.toolCards.set(payload.callId, card);
          appendStreamChild(card);
        },
        onToolCallEnd: (payload) => {
          const card = streamState.toolCards.get(payload.callId);
          if (card) this.updateStreamingToolCard(card, payload);
        },
        onContextUpdate: (payload) => {
          const fromUsage = Number(payload?.inputTokens) || 0;
          const estimated = Number(payload?.estimatedTokens) || 0;
          setContextStatus(Math.max(fromUsage, estimated));
        },
        onTurnEnd: () => {
          this.sealStreamingToolCardsIncomplete(streamState);
          turnEnded = true;
          turnEndResolve?.();
        },
        onCancelRequested: () => {
          const btn = $("#agent-stop-btn");
          if (btn) btn.textContent = "Stopping…";
        },
      });
      retryIsSafe = false;

      try {
        const toolCalls = Array.isArray(result.toolCalls)
          ? result.toolCalls.map(toConversationToolCall)
          : undefined;
        const steps = sanitizeAssistantSteps(result.steps);
        const assistantMessage = {
          role: "assistant",
          content: result.text,
          traceId: result.traceId,
          model: result.model,
          rounds: result.rounds,
          reasoning: result.reasoning,
          ...(toolCalls?.length ? { toolCalls } : {}),
          ...(steps?.length ? { steps } : {}),
        };
        this.conversation = resumeFrom
          ? await this.shell.agentConversations.replaceLastInterrupted(this.conversation.id, assistantMessage)
          : await this.shell.agentConversations.append(this.conversation.id, assistantMessage);
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
      const finalTokens = Math.max(
        liveTokens,
        estimateContextTokens(buildAgentContext(this.conversation)),
        estimateContextTokens(this.conversation?.messages || []),
        Number(result.usage?.inputTokens) || 0,
      );
      status.textContent = selectedModel ? formatContextUsage(finalTokens, selectedModel.contextWindow) : "Choose a model";
      this.log("info", `Agent turn completed trace=${result.traceId} rounds=${result.rounds}`);
    } catch (error) {
      if (error.code === "AGENT_TURN_CANCELLED") {
        // Wait for the terminal turn_end event (published after in-flight
        // tools drain) before sealing, with a 2s fallback so the UI never
        // hangs on a missing event.
        if (!turnEnded) {
          await Promise.race([turnEndPromise, new Promise((r) => setTimeout(r, 2000))]);
        }
        this.sealStreamingToolCardsIncomplete(streamState);
        if (pending && streamState?.streamedText) {
          this.sealStreamingMessage(pending, { content: streamState.streamedText });
          pending.classList.add("agent-message-stopped");
        } else {
          pending?.remove();
        }
        this.appendMessage("assistant", "Turn stopped.", { error: true });
        status.textContent = "Turn stopped";
        this.log("info", `Agent turn stopped trace=${this.activeTraceId}`);
        return;
      }
      const partial = error.details?.partial;
      if (partial) {
        this.sealStreamingMessage(pending, partial);
        const interruptedMessage = {
          role: "assistant",
          content: `Turn interrupted after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`,
          status: "interrupted",
          traceId: partial.traceId,
          model: partial.model,
          rounds: partial.rounds,
          steps: sanitizeAssistantSteps(partial.steps),
          ...(Array.isArray(partial.toolCalls) && partial.toolCalls.length
            ? { toolCalls: partial.toolCalls.map(toConversationToolCall) }
            : {}),
          ...(Array.isArray(partial.messages) ? { resumeMessages: partial.messages } : {}),
        };
        try {
          this.conversation = resumeFrom
            ? await this.shell.agentConversations.replaceLastInterrupted(this.conversation.id, interruptedMessage)
            : await this.shell.agentConversations.append(this.conversation.id, interruptedMessage);
        } catch (persistError) {
          this.log("error", `Interrupted assistant persistence failed: ${persistError.message || String(persistError)}`);
        }
        this.failedMessage = this.appendMessage(
          "assistant",
          `Turn failed: ${error.message || "Unknown error"}`,
          { error: true, retry: true },
        );
        status.textContent = "Turn interrupted · ready to retry";
        this.log("error", `Agent turn failed: ${error.message || String(error)}`);
      } else {
        pending?.remove();
        this.failedMessage = this.appendMessage(
          "assistant",
          `Turn failed: ${error.message || "Unknown error"}`,
          { error: true, retry: retryIsSafe },
        );
        status.textContent = retryIsSafe ? "Turn failed · ready to retry" : "Local conversation error";
        this.log("error", `Agent turn failed: ${error.message || String(error)}`);
      }
    } finally {
      this.turnPending = false;
      this.activeTraceId = "";
      input.disabled = false;
      sendButton.disabled = false;
      stopButton.hidden = true;
      input.focus();
    }
  }

  async submitAcp({ text, retry }) {
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    const stopButton = $("#agent-stop-btn");
    const status = $("#agent-provider-status");
    if (this.turnPending || !this.conversation?.acp) return;

    let pending = null;
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
        this.conversation = await this.shell.agentConversations.append(this.conversation.id, {
          role: "user",
          content: text,
        });
        this.appendMessage("user", text);
        input.value = "";
        this.resizeComposerInput();
        await this.refresh();
      }
      retryIsSafe = true;

      pending = this.createStreamingMessage();
      const streamState = { message: pending, textBubble: null, streamedText: "", reasoningEl: null, reasoningText: "", lastKind: null, toolCards: new Map(), toolCalls: [], steps: [] };
      const appendStreamChild = (node) => {
        streamState.message?.appendChild(node);
        this.scrollToBottom();
      };
      const sealStep = () => {
        if (streamState.lastKind === "reasoning" && streamState.reasoningText?.trim()) {
          streamState.steps.push({ type: "reasoning", content: streamState.reasoningText });
        } else if (streamState.lastKind === "text" && streamState.streamedText?.trim()) {
          streamState.steps.push({ type: "text", content: streamState.streamedText });
        }
      };
      const result = await this.runAcpTurn([{ type: "text", text }], {
        traceId: this.activeTraceId,
        conversationId: this.conversation.id,
        workspace: this.conversation.workspace,
        providerId: this.conversation.acp.providerId,
        onDelta: (delta) => {
          if (streamState.lastKind !== "text") {
            sealStep();
            streamState.textBubble = element("div", "agent-bubble");
            streamState.streamedText = "";
            streamState.message?.appendChild(streamState.textBubble);
            this.scrollToBottom();
          }
          streamState.lastKind = "text";
          streamState.streamedText += delta;
          if (streamState.textBubble) streamState.textBubble.innerHTML = renderAssistantMarkdown(streamState.streamedText);
        },
        onReasoningDelta: (delta) => {
          if (streamState.lastKind !== "reasoning") {
            sealStep();
            streamState.reasoningEl = this.createStreamingReasoningBlock();
            streamState.reasoningText = "";
            streamState.message?.appendChild(streamState.reasoningEl);
            this.scrollToBottom();
          }
          streamState.lastKind = "reasoning";
          streamState.reasoningText += delta;
          const content = streamState.reasoningEl?.querySelector(".agent-reasoning-content");
          if (content) content.innerHTML = renderReasoningMarkdown(streamState.reasoningText);
        },
        onToolCallStart: (payload) => {
          if (streamState.lastKind !== "tool") sealStep();
          streamState.lastKind = "tool";
          const card = this.createStreamingToolCard(payload.callId, payload.name, payload.args);
          streamState.toolCards.set(payload.callId, card);
          streamState.toolCalls.push({ id: payload.callId, name: payload.name, ok: true, args: payload.args });
          streamState.steps.push({ type: "tool_calls", calls: [{ id: payload.callId, name: payload.name, ok: true, args: payload.args }] });
          appendStreamChild(card);
        },
        onToolCallEnd: (payload) => {
          const card = streamState.toolCards.get(payload.callId);
          if (card) this.updateStreamingToolCard(card, payload);
          const tc = streamState.toolCalls.find((t) => t.id === payload.callId);
          if (tc) { tc.ok = payload.ok !== false; if (payload.error) tc.error = payload.error; }
          const step = [...streamState.steps].reverse().find((s) => s.type === "tool_calls" && s.calls.some((c) => c.id === payload.callId));
          if (step) { const call = step.calls.find((c) => c.id === payload.callId); if (call) { call.ok = payload.ok !== false; if (payload.error) call.error = payload.error; } }
        },
        onTurnEnd: () => {
          this.sealStreamingToolCardsIncomplete(streamState);
        },
        onPermissionRequest: (payload) => {
          const card = this.createAcpPermissionCard(payload);
          if (card) appendStreamChild(card);
        },
        onAskRequest: (payload) => {
          const card = this.createAcpAskCard(payload);
          if (card) appendStreamChild(card);
        },
      });
      sealStep();
      this.sealStreamingToolCardsIncomplete(streamState);
      retryIsSafe = false;

      const fullText = streamState.steps
        .filter((s) => s.type === "text")
        .map((s) => s.content)
        .join("\n\n");
      const fullReasoning = streamState.steps
        .filter((s) => s.type === "reasoning")
        .map((s) => s.content)
        .join("\n\n");

      this.conversation = await this.shell.agentConversations.append(this.conversation.id, {
        role: "assistant",
        content: fullText || "Done.",
        traceId: this.activeTraceId,
        model: this.conversation.acp.providerId,
        reasoning: fullReasoning || undefined,
        ...(streamState.toolCalls.length ? { toolCalls: streamState.toolCalls } : {}),
        ...(streamState.steps.length ? { steps: streamState.steps } : {}),
      });
      const savedMessage = this.conversation.messages.at(-1);
      this.sealStreamingMessage(pending, savedMessage ?? { content: streamState.streamedText });
      await this.refresh();
      status.textContent = `ACP · ${this.conversation.acp.providerId}`;
    } catch (error) {
      pending?.remove();
      this.failedMessage = this.appendMessage("assistant", `Turn failed: ${error.message || "Unknown error"}`, { error: true, retry: retryIsSafe });
      status.textContent = retryIsSafe ? "ACP turn failed · ready to retry" : "ACP turn error";
      this.log("error", `ACP turn failed: ${error.message || String(error)}`);
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
    const acpButton = $("#agent-new-acp");
    const acpMenu = $("#agent-new-acp-menu");
    if (acpButton && acpMenu) {
      acpButton.hidden = false;
      acpButton.addEventListener("click", () => this.renderAcpMenu(acpMenu, !acpMenu.hidden));
      document.addEventListener("click", (event) => {
        if (!acpMenu.hidden && !acpButton.contains(event.target) && !acpMenu.contains(event.target)) {
          acpMenu.hidden = true;
        }
      });
    }
    $("#agent-conversation-search").addEventListener("input", () => this.renderList());
    $("#agent-delete-overlay").addEventListener("click", () => this.closeDeleteDialog());
    $("#agent-delete-close").addEventListener("click", () => this.closeDeleteDialog());
    $("#agent-delete-cancel").addEventListener("click", () => this.closeDeleteDialog());
    $("#agent-delete-confirm").addEventListener("click", () => this.runUiAction(this.deletePending(), "Could not delete conversation"));
    $("#agent-workspace-btn").addEventListener("click", () => this.runUiAction(this.chooseWorkspace(), "Could not choose workspace"));
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
    if (!this.turnPending || !this.activeTraceId) return;
    const isAcp = this.conversation?.kind === "acp";
    const cancel = isAcp ? this.cancelAcpTurn : this.cancelTurn;
    if (!cancel) return;
    const button = $("#agent-stop-btn");
    button.disabled = true;
    button.textContent = "Stopping…";
    try {
      await (isAcp ? cancel(this.activeTraceId, this.conversation.id) : cancel(this.activeTraceId));
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
    this.updateWorkspaceLabel();
    this.updateAcpStatus();
  }

  updateWorkspaceLabel() {
    const label = $("#agent-workspace-label");
    if (!label) return;
    const ws = this.conversation?.workspace;
    label.textContent = ws ? ws.split("/").pop() || ws : "Home";
    const btn = $("#agent-workspace-btn");
    if (btn) btn.title = ws || "Home (user home directory)";
  }

  async updateAcpStatus() {
    const bar = $("#acp-status-bar");
    const provider = $("#acp-status-provider");
    const chip = $("#acp-status-chip");
    const pill = $("#agent-acp-pill");
    const pillLabel = $("#agent-acp-pill-label");
    if (!bar || !provider || !chip) return;
    if (this.conversation?.kind === "acp") {
      bar.hidden = false;
      pill.hidden = false;
      const providerId = this.conversation.acp?.providerId ?? "unknown";
      provider.textContent = providerId;
      chip.textContent = this.turnPending ? "● RUNNING" : "● IDLE";
      chip.className = `acp-status-chip ${this.turnPending ? "is-running" : "is-idle"}`;
      const modelName = this.currentAcpModelName() ?? providerId;
      pillLabel.textContent = `ACP · ${modelName}`;
      await this.ensureAcpSessionIfNeeded();
    } else {
      bar.hidden = true;
      pill.hidden = true;
      this.acpConfigOptions = [];
      this.refreshModelPicker?.();
    }
  }

  async ensureAcpSessionIfNeeded() {
    if (this.conversation?.kind !== "acp" || !this.ensureAcpSession) return;
    if (this.acpConfigOptions.length > 0) return;
    const providers = await this.shell.acpProviders.list();
    const descriptor = providers.find((p) => p.manifest.id === this.conversation.acp.providerId);
    if (!descriptor) return;
    try {
      const info = await this.ensureAcpSession(
        this.conversation.id,
        this.conversation.workspace,
        {
          providerId: descriptor.manifest.id,
          command: descriptor.manifest.command,
          args: descriptor.manifest.args,
          authMethodId: descriptor.manifest.authMethodId,
        },
      );
      this.acpConfigOptions = info?.configOptions ?? [];
      this.updateAcpModelLabel();
    } catch (error) {
      this.log("warn", `Failed to ensure ACP session: ${error.message || error}`);
    }
  }

  updateAcpModelLabel() {
    const modelName = this.currentAcpModelName();
    if (!modelName) return;
    const pillLabel = $("#agent-acp-pill-label");
    if (pillLabel) pillLabel.textContent = `ACP · ${modelName}`;
    const triggerLabel = $("#agent-model-trigger-label");
    if (triggerLabel) triggerLabel.textContent = `${modelName} · ACP`;
  }

  currentAcpModelName() {
    const opt = this.acpConfigOptions.find((o) => o.id === "model");
    if (!opt) return undefined;
    const value = String(opt.currentValue ?? "");
    const matched = opt.options?.find((o) => o.value === value);
    return matched?.name ?? value;
  }

  async refreshAcpConfigOptions() {
    if (this.conversation?.kind !== "acp") return;
    try {
      const info = await this.getAcpSessionInfo(this.conversation.id);
      this.acpConfigOptions = info?.configOptions ?? [];
      this.updateAcpModelLabel();
    } catch (error) {
      this.log("warn", `Failed to load ACP config options: ${error.message || error}`);
    }
  }

  async selectAcpConfigOption(configId, value) {
    if (this.conversation?.kind !== "acp" || !this.setAcpConfigOption) return;
    try {
      const updated = await this.setAcpConfigOption(this.conversation.id, configId, value);
      this.acpConfigOptions = updated ?? [];
      this.updateAcpModelLabel();
    } catch (error) {
      this.notify(`Could not change ACP ${configId}: ${error.message || error}`, "error");
    }
  }

  async chooseWorkspace() {
    if (!this.conversation) return;
    const picked = await this.shell.shellControls.pickPluginSource("directory");
    if (!picked) return;
    this.conversation = await this.shell.agentConversations.setWorkspace(this.conversation.id, picked);
    this.updateWorkspaceLabel();
  }

  updateContextStatus() {
    const status = $("#agent-provider-status");
    const selectedModel = this.getActiveModel();
    if (!status) return;
    if (!selectedModel) {
      status.textContent = "Choose a model";
      return;
    }
    // Prefer the richer of provider-bound context vs full thread (steps/tools),
    // so loading a chat or finishing a turn does not collapse to 0/tiny counts.
    const providerTokens = estimateContextTokens(buildAgentContext(this.conversation));
    const threadTokens = estimateContextTokens(this.conversation?.messages || []);
    status.textContent = formatContextUsage(
      Math.max(providerTokens, threadTokens),
      selectedModel.contextWindow,
    );
  }

  conversationRow(conversation) {
    const row = element("div", `agent-conversation-item${conversation.id === this.activeId ? " is-active" : ""}`);
    row.setAttribute("role", "listitem");
    const open = element("button", "agent-conversation-open");
    open.type = "button";
    const title = element("span", "agent-conversation-title");
    title.textContent = conversation.title;
    if (conversation.kind === "acp") {
      const badge = element("span", "acp-conversation-badge", "ACP");
      title.appendChild(badge);
    }
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
    this.updateAcpStatus();
    if (!this.conversation?.messages.length) {
      const empty = element("div", "agent-empty");
      empty.id = "agent-empty";
      if (this.conversation?.kind === "acp") {
        empty.innerHTML = "<div class=\"agent-empty-mark\">✦</div><h2>Start an ACP conversation</h2><p>This thread talks to an external ACP agent over stdio JSON-RPC.</p>";
      } else {
        empty.innerHTML = "<div class=\"agent-empty-mark\">✦</div><h2>Start a conversation</h2><p>Choose a configured model. The agent can discover and start MCP servers when the task needs them.</p>";
      }
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
    const message = element("article", `agent-message ${role}${meta.pending ? " agent-pending" : ""}${meta.error ? " agent-message-error" : ""}${meta.status === "interrupted" ? " agent-message-interrupted" : ""}`);
    message.setAttribute("aria-label", role === "user" ? "Your message" : "NusaShell Agent response");

    if (role === "assistant") {
      const identity = element("div", "agent-message-identity");
      identity.append(
        element("span", "agent-message-mark", meta.pending ? "◌" : "✦"),
        element("span", "agent-message-meta", meta.pending ? "Working" : meta.status === "interrupted" ? "Interrupted" : "NusaShell Agent"),
      );
      message.appendChild(identity);
    }

    if (meta.attachments?.length) {
      message.appendChild(this.messageAttachments(meta.attachments));
    }

    if (role === "assistant" && meta.steps?.length) {
      let lastStepModel = null;
      for (const step of meta.steps) {
        if (step.model && step.model !== lastStepModel) {
          const divider = this.modelDivider(step.model);
          if (divider) message.appendChild(divider);
          lastStepModel = step.model;
        }
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

  modelDivider(model) {
    if (!model) return null;
    const selected = this.getActiveModel?.();
    const isFallback = selected && model !== selected.id;
    const divider = element("div", `agent-model-step${isFallback ? " is-fallback" : ""}`);
    divider.append(
      element("span", "agent-model-step-mark", "◈"),
      element("span", "agent-model-step-name", shortModelName(model)),
    );
    if (isFallback) divider.title = `Routed to ${model} (selected: ${selected.id})`;
    return divider;
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
    const stack = element("div", "agent-tool-stack");
    toolCalls.forEach((toolCall) => {
      if (toolCall.name === "ask_question") {
        stack.appendChild(this.createAskCard(toolCall.id, toolCall.args, {
          sealed: true,
          output: toolCall.output,
          ok: toolCall.ok !== false,
          error: toolCall.error,
        }));
      } else {
        stack.appendChild(this.toolTerminal(toolCall));
      }
    });
    return stack;
  }

  toolTerminal(toolCall, { open = false, running = false } = {}) {
    const terminal = document.createElement("details");
    terminal.className = `agent-tool-terminal${running ? " is-running" : toolCall.ok === false ? " is-error" : " is-success"}`;
    terminal.open = open;
    if (toolCall.id) terminal.dataset.callId = toolCall.id;

    const summary = document.createElement("summary");
    const meta = summarizeToolArgs(toolCall.args);
    summary.append(
      element("span", "agent-tool-terminal-prompt", "›_"),
      element("span", "agent-tool-terminal-title", toolCall.name || "tool"),
      element(
        "span",
        "agent-tool-terminal-meta",
        running ? "Running" : meta || (toolCall.ok === false ? "Failed" : "Completed"),
      ),
      element("span", "agent-tool-terminal-chevron", "⌄"),
    );

    const body = element("div", "agent-tool-terminal-body");

    const callPanel = element("div", "agent-tool-terminal-panel");
    callPanel.appendChild(element("div", "agent-tool-terminal-panel-label", "tool"));
    const input = element("pre", "agent-tool-terminal-input");
    input.innerHTML = renderToolCodeHtml(formatToolTerminalInput(toolCall.name || "tool", toolCall.args));
    callPanel.appendChild(input);
    body.appendChild(callPanel);

    const outputText = toolCall.output
      || (toolCall.error ? toolCall.error : "")
      || (toolCall.result !== undefined ? formatToolOutput(toolCall.result) : "")
      || (running ? "…" : toolCall.ok === false ? "Tool failed." : "ok");
    const outputPanel = element("div", "agent-tool-terminal-panel");
    outputPanel.appendChild(element("div", "agent-tool-terminal-panel-label", "Output"));
    const output = element("pre", `agent-tool-terminal-output${toolCall.ok === false ? " is-error" : ""}`);
    output.innerHTML = renderToolCodeHtml(outputText);
    outputPanel.appendChild(output);
    body.appendChild(outputPanel);

    terminal.append(summary, body);
    return terminal;
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
    thread.appendChild(message);
    thread.scrollTop = thread.scrollHeight;
    return message;
  }

  createStreamingReasoningBlock() {
    const disclosure = document.createElement("details");
    disclosure.className = "agent-reasoning";
    disclosure.open = false;
    const summary = document.createElement("summary");
    summary.append(
      element("span", "agent-reasoning-mark", "⌁"),
      element("span", "agent-reasoning-title", "Thinking"),
      element("span", "agent-reasoning-hint", "Show reasoning"),
      element("span", "agent-reasoning-chevron", "⌄"),
    );
    const content = element("div", "agent-reasoning-content");
    disclosure.append(summary, content);
    return disclosure;
  }

  createStreamingToolCard(callId, name, args) {
    if (name === "ask_question") {
      return this.createAskCard(callId, args, { sealed: false });
    }
    const card = this.toolTerminal(
      { id: callId, name, ok: true, args },
      { open: true, running: true },
    );
    if (args && typeof args === "object") card._toolArgs = args;
    return card;
  }

  updateStreamingToolCard(card, payload) {
    if (card.classList.contains("agent-ask-card")) {
      this.sealAskCard(card, payload);
      return;
    }
    card.classList.remove("is-running");
    card.classList.toggle("is-success", payload.ok !== false);
    card.classList.toggle("is-error", payload.ok === false);
    card.open = false;
    const args = payload.args && typeof payload.args === "object"
      ? payload.args
      : card._toolArgs;
    if (payload.args && typeof payload.args === "object") card._toolArgs = payload.args;
    const meta = card.querySelector(".agent-tool-terminal-meta");
    if (meta) {
      meta.textContent = summarizeToolArgs(args) || (payload.ok === false ? "Failed" : "Completed");
    }
    const input = card.querySelector(".agent-tool-terminal-input");
    if (input) {
      input.innerHTML = renderToolCodeHtml(formatToolTerminalInput(payload.name || "tool", args));
    }
    const output = card.querySelector(".agent-tool-terminal-output");
    if (output) {
      output.classList.toggle("is-error", payload.ok === false);
      output.innerHTML = renderToolCodeHtml(
        payload.output
          || payload.error
          || (payload.ok === false ? "Tool failed." : "ok"),
      );
    }
  }

  sealStreamingToolCardsIncomplete(streamState) {
    if (!streamState?.toolCards) return;
    for (const card of streamState.toolCards.values()) {
      if (!card) continue;
      if (card.classList.contains("agent-ask-card")) {
        if (!card.classList.contains("is-sealed")) {
          card.classList.remove("is-pending", "is-submitting");
          card.classList.add("is-sealed", "is-error");
        }
        continue;
      }
      if (card.classList.contains("is-running")) {
        card.classList.remove("is-running");
        card.classList.add("is-error", "is-incomplete");
        card.open = false;
        const output = card.querySelector(".agent-tool-terminal-output");
        if (output) {
          output.classList.add("is-error");
          output.innerHTML = renderToolCodeHtml("Tool call did not complete (turn stopped).");
        }
      }
    }
  }

  createAskCard(callId, args, { sealed = false, output = "", ok = true, error = "" } = {}) {
    const question = typeof args?.question === "string" ? args.question : "Choose a response";
    const options = Array.isArray(args?.options) ? args.options : [];
    const multiSelect = Boolean(args?.multi_select);
    const allowFreeText = args?.allow_free_text !== false;
    const parsedAnswer = sealed ? parseAskAnswer(output) : null;

    const card = element("div", `agent-ask-card${sealed ? " is-sealed" : " is-pending"}${ok === false ? " is-error" : ""}`);
    card.dataset.callId = callId || "";
    card._toolArgs = args && typeof args === "object" ? args : {};

    const header = element("div", "agent-ask-header");
    header.append(
      element("span", "agent-ask-header-icon", "⚒"),
      element("span", "agent-ask-header-title", "Ask Question"),
    );
    card.appendChild(header);

    const body = element("div", "agent-ask-body");
    body.appendChild(element("div", "agent-ask-question", question));
    body.appendChild(element(
      "div",
      "agent-ask-hint",
      multiSelect ? "Choose one or more responses so I can continue the task." : "Choose one response so I can continue the task.",
    ));

    const optionsWrap = element("div", "agent-ask-options");
    const selected = new Set(
      sealed && parsedAnswer?.optionIds?.length
        ? parsedAnswer.optionIds
        : options.filter((option) => option?.default).map((option) => String(option.id)),
    );
    if (!multiSelect && selected.size > 1) {
      const first = [...selected][0];
      selected.clear();
      if (first) selected.add(first);
    }

    options.forEach((option) => {
      if (!option || typeof option !== "object") return;
      const id = String(option.id ?? "");
      const label = String(option.label ?? id);
      const row = element("button", `agent-ask-option${selected.has(id) ? " is-selected" : ""}`);
      row.type = "button";
      row.dataset.optionId = id;
      row.setAttribute("aria-pressed", selected.has(id) ? "true" : "false");
      if (sealed) row.disabled = true;

      const marker = element("span", `agent-ask-option-marker${multiSelect ? " is-check" : " is-radio"}`);
      const media = element("div", "agent-ask-option-media");
      if (typeof option.image === "string" && option.image.trim()) {
        const img = document.createElement("img");
        img.className = "agent-ask-option-image";
        img.src = option.image.trim();
        img.alt = "";
        media.appendChild(img);
      } else if (typeof option.icon === "string" && option.icon.trim()) {
        media.appendChild(element("span", "agent-ask-option-icon", option.icon.trim()));
      } else {
        media.appendChild(element("span", "agent-ask-option-icon is-empty", "•"));
      }

      const copy = element("div", "agent-ask-option-copy");
      const titleRow = element("div", "agent-ask-option-title-row");
      titleRow.appendChild(element("span", "agent-ask-option-label", label));
      if (option.default) titleRow.appendChild(element("span", "agent-ask-option-badge", "Recommended"));
      copy.appendChild(titleRow);
      if (typeof option.description === "string" && option.description.trim()) {
        copy.appendChild(element("div", "agent-ask-option-desc", option.description.trim()));
      }

      row.append(marker, media, copy);
      if (!sealed) {
        row.addEventListener("click", () => {
          if (card.classList.contains("is-submitting") || card.classList.contains("is-sealed")) return;
          if (multiSelect) {
            if (selected.has(id)) selected.delete(id);
            else selected.add(id);
          } else {
            selected.clear();
            selected.add(id);
            card.querySelectorAll(".agent-ask-option").forEach((node) => {
              node.classList.toggle("is-selected", node.dataset.optionId === id);
              node.setAttribute("aria-pressed", node.dataset.optionId === id ? "true" : "false");
            });
            const custom = card.querySelector(".agent-ask-custom");
            custom?.classList.remove("is-active");
            const textarea = card.querySelector(".agent-ask-textarea");
            if (textarea) textarea.value = "";
          }
          row.classList.toggle("is-selected", selected.has(id));
          row.setAttribute("aria-pressed", selected.has(id) ? "true" : "false");
          this.syncAskSendState(card, selected);
        });
      }
      optionsWrap.appendChild(row);
    });
    body.appendChild(optionsWrap);

    if (allowFreeText || (sealed && parsedAnswer?.via === "text")) {
      const custom = element("div", `agent-ask-custom${sealed && parsedAnswer?.via === "text" ? " is-active" : ""}`);
      const customToggle = element("button", "agent-ask-custom-toggle");
      customToggle.type = "button";
      customToggle.textContent = sealed && parsedAnswer?.via === "text" ? "Custom answer" : "Type answer...";
      customToggle.disabled = sealed;
      const textarea = document.createElement("textarea");
      textarea.className = "agent-ask-textarea";
      textarea.rows = 3;
      textarea.placeholder = "Type a different direction...";
      textarea.maxLength = 8000;
      if (sealed && parsedAnswer?.via === "text") {
        textarea.value = parsedAnswer.answer || "";
        textarea.disabled = true;
      }
      if (!sealed) {
        customToggle.addEventListener("click", () => {
          custom.classList.add("is-active");
          if (!multiSelect) {
            selected.clear();
            card.querySelectorAll(".agent-ask-option").forEach((node) => {
              node.classList.remove("is-selected");
              node.setAttribute("aria-pressed", "false");
            });
          }
          textarea.focus();
          this.syncAskSendState(card, selected);
        });
        textarea.addEventListener("input", () => this.syncAskSendState(card, selected));
      }
      custom.append(customToggle, textarea);
      body.appendChild(custom);
    }

    if (sealed) {
      const answerLine = element(
        "div",
        "agent-ask-answer",
        ok === false
          ? (error || "Ask question failed.")
          : `Answer: ${parsedAnswer?.answer || output || "—"}`,
      );
      body.appendChild(answerLine);
    } else {
      const actions = element("div", "agent-ask-actions");
      const send = element("button", "agent-ask-send");
      send.type = "button";
      send.innerHTML = `<span class="agent-ask-send-icon">✈</span><span>Send answer</span>`;
      send.addEventListener("click", () => void this.submitAskCard(card, selected));
      actions.append(
        send,
        element("span", "agent-ask-dismiss-hint", "Esc / Stop to dismiss"),
      );
      body.appendChild(actions);
      this.syncAskSendState(card, selected);
    }

    card.appendChild(body);
    return card;
  }

  syncAskSendState(card, selected) {
    const send = card.querySelector(".agent-ask-send");
    if (!send) return;
    const textarea = card.querySelector(".agent-ask-textarea");
    const customActive = card.querySelector(".agent-ask-custom")?.classList.contains("is-active");
    const hasText = Boolean(textarea?.value?.trim());
    const hasOptions = selected.size > 0;
    send.disabled = card.classList.contains("is-submitting") || (!hasOptions && !(customActive && hasText));
  }

  async submitAskCard(card, selected) {
    if (!this.answerAsk || !this.activeTraceId || card.classList.contains("is-submitting")) return;
    const callId = card.dataset.callId;
    if (!callId) return;
    const textarea = card.querySelector(".agent-ask-textarea");
    const customActive = card.querySelector(".agent-ask-custom")?.classList.contains("is-active");
    const text = textarea?.value?.trim() || "";
    const via = customActive && text ? "text" : "option";
    if (via === "option" && selected.size === 0) return;
    if (via === "text" && !text) return;

    card.classList.add("is-submitting");
    this.syncAskSendState(card, selected);
    try {
      await this.answerAsk({
        traceId: this.activeTraceId,
        callId,
        via,
        ...(via === "option" ? { optionIds: [...selected] } : { text }),
      });
      card.querySelectorAll("button, textarea").forEach((node) => {
        node.disabled = true;
      });
    } catch (error) {
      card.classList.remove("is-submitting");
      this.syncAskSendState(card, selected);
      this.notify(error instanceof Error ? error.message : "Could not send answer", "error");
    }
  }

  sealAskCard(card, payload) {
    card.classList.remove("is-pending", "is-submitting");
    card.classList.add("is-sealed");
    card.classList.toggle("is-error", payload.ok === false);
    card.querySelectorAll("button, textarea").forEach((node) => {
      node.disabled = true;
    });
    const parsed = parseAskAnswer(payload.output);
    let answerEl = card.querySelector(".agent-ask-answer");
    if (!answerEl) {
      answerEl = element("div", "agent-ask-answer");
      card.querySelector(".agent-ask-body")?.appendChild(answerEl);
    }
    answerEl.textContent = payload.ok === false
      ? (payload.error || "Ask question failed.")
      : `Answer: ${parsed?.answer || payload.output || "—"}`;
    if (parsed?.via === "option" && parsed.optionIds?.length) {
      const chosen = new Set(parsed.optionIds);
      card.querySelectorAll(".agent-ask-option").forEach((node) => {
        node.classList.toggle("is-selected", chosen.has(node.dataset.optionId));
      });
    }
    if (parsed?.via === "text") {
      const custom = card.querySelector(".agent-ask-custom");
      const textarea = card.querySelector(".agent-ask-textarea");
      custom?.classList.add("is-active");
      if (textarea) textarea.value = parsed.answer || "";
    }
  }

  createAcpPermissionCard(payload) {
    if (!payload?.requestId || !this.answerAcpPermission) return null;
    const traceId = payload.traceId || this.activeTraceId;
    const conversationId = this.conversation?.id;
    if (!conversationId) return null;
    const options = Array.isArray(payload.options) ? payload.options : [];
    if (options.length === 0) return null;

    const card = element("div", "agent-ask-card acp-permission-card is-pending");
    card.dataset.acpRequestId = String(payload.requestId);
    card.dataset.acpKind = "permission";

    const header = element("div", "agent-ask-header");
    header.append(
      element("span", "agent-ask-header-icon", "🛡"),
      element("span", "agent-ask-header-title", payload.toolTitle || "Permission required"),
    );
    card.appendChild(header);

    const body = element("div", "agent-ask-body");
    if (payload.detail) body.appendChild(element("div", "agent-ask-question", String(payload.detail)));
    body.appendChild(element("div", "agent-ask-hint", "Choose how to handle this action."));

    const optionsWrap = element("div", "agent-ask-options");
    for (const option of options) {
      if (!option || typeof option !== "object") continue;
      const optionId = String(option.optionId ?? option.id ?? "");
      const label = String(option.name ?? option.label ?? optionId);
      if (!optionId) continue;
      const row = element("button", "agent-ask-option");
      row.type = "button";
      row.dataset.optionId = optionId;
      const marker = element("span", "agent-ask-option-marker is-radio");
      const copy = element("div", "agent-ask-option-copy");
      copy.appendChild(element("span", "agent-ask-option-label", label));
      row.append(marker, copy);
      row.addEventListener("click", () => {
        if (card.classList.contains("is-submitting") || card.classList.contains("is-sealed")) return;
        void this.submitAcpPermissionCard(card, traceId, conversationId, optionId);
      });
      optionsWrap.appendChild(row);
    }
    body.appendChild(optionsWrap);
    card.appendChild(body);
    return card;
  }

  async submitAcpPermissionCard(card, traceId, conversationId, optionId) {
    if (!this.answerAcpPermission || card.classList.contains("is-submitting")) return;
    const requestId = card.dataset.acpRequestId;
    if (!requestId) return;
    card.classList.add("is-submitting");
    try {
      await this.answerAcpPermission({ traceId, conversationId, requestId, optionId });
      card.classList.remove("is-pending", "is-submitting");
      card.classList.add("is-sealed");
      card.querySelectorAll("button").forEach((node) => { node.disabled = true; });
      const chosen = new Set([optionId]);
      card.querySelectorAll(".agent-ask-option").forEach((node) => {
        node.classList.toggle("is-selected", chosen.has(node.dataset.optionId));
      });
      const answerEl = element("div", "agent-ask-answer", `Allowed: ${optionId}`);
      card.querySelector(".agent-ask-body")?.appendChild(answerEl);
    } catch (error) {
      card.classList.remove("is-submitting");
      this.notify(error instanceof Error ? error.message : "Could not answer permission", "error");
    }
  }

  createAcpAskCard(payload) {
    if (!payload?.requestId || !this.answerAcpAsk) return null;
    const traceId = payload.traceId || this.activeTraceId;
    const conversationId = this.conversation?.id;
    if (!conversationId) return null;
    const options = Array.isArray(payload.options) ? payload.options : [];
    const multiSelect = Boolean(payload.multiSelect);
    const allowFreeText = payload.allowFreeText !== false;
    const selected = new Set();

    const card = element("div", "agent-ask-card acp-ask-card is-pending");
    card.dataset.acpRequestId = String(payload.requestId);
    card.dataset.acpKind = "ask";
    card.dataset.acpTraceId = traceId;
    card.dataset.acpConversationId = conversationId;

    const header = element("div", "agent-ask-header");
    header.append(
      element("span", "agent-ask-header-icon", "⚒"),
      element("span", "agent-ask-header-title", "Ask Question"),
    );
    card.appendChild(header);

    const body = element("div", "agent-ask-body");
    body.appendChild(element("div", "agent-ask-question", String(payload.question || "Choose a response")));
    body.appendChild(element(
      "div",
      "agent-ask-hint",
      multiSelect ? "Choose one or more responses so I can continue the task." : "Choose one response so I can continue the task.",
    ));

    const optionsWrap = element("div", "agent-ask-options");
    for (const option of options) {
      if (!option || typeof option !== "object") continue;
      const optionId = String(option.optionId ?? option.id ?? "");
      const label = String(option.name ?? option.label ?? optionId);
      if (!optionId) continue;
      const row = element("button", `agent-ask-option${selected.has(optionId) ? " is-selected" : ""}`);
      row.type = "button";
      row.dataset.optionId = optionId;
      row.setAttribute("aria-pressed", "false");
      const marker = element("span", `agent-ask-option-marker${multiSelect ? " is-check" : " is-radio"}`);
      const copy = element("div", "agent-ask-option-copy");
      copy.appendChild(element("span", "agent-ask-option-label", label));
      row.append(marker, copy);
      row.addEventListener("click", () => {
        if (card.classList.contains("is-submitting") || card.classList.contains("is-sealed")) return;
        if (multiSelect) {
          if (selected.has(optionId)) selected.delete(optionId);
          else selected.add(optionId);
        } else {
          selected.clear();
          selected.add(optionId);
          card.querySelectorAll(".agent-ask-option").forEach((node) => {
            node.classList.toggle("is-selected", node.dataset.optionId === optionId);
            node.setAttribute("aria-pressed", node.dataset.optionId === optionId ? "true" : "false");
          });
        }
        row.classList.toggle("is-selected", selected.has(optionId));
        row.setAttribute("aria-pressed", selected.has(optionId) ? "true" : "false");
        this.syncAcpAskSendState(card, selected);
      });
      optionsWrap.appendChild(row);
    }
    body.appendChild(optionsWrap);

    if (allowFreeText) {
      const custom = element("div", "agent-ask-custom");
      const customToggle = element("button", "agent-ask-custom-toggle");
      customToggle.type = "button";
      customToggle.textContent = "Type answer...";
      const textarea = document.createElement("textarea");
      textarea.className = "agent-ask-textarea";
      textarea.rows = 3;
      textarea.placeholder = "Type a different direction...";
      textarea.maxLength = 8000;
      customToggle.addEventListener("click", () => {
        custom.classList.add("is-active");
        if (!multiSelect) {
          selected.clear();
          card.querySelectorAll(".agent-ask-option").forEach((node) => {
            node.classList.remove("is-selected");
            node.setAttribute("aria-pressed", "false");
          });
        }
        textarea.focus();
        this.syncAcpAskSendState(card, selected);
      });
      textarea.addEventListener("input", () => this.syncAcpAskSendState(card, selected));
      custom.append(customToggle, textarea);
      body.appendChild(custom);
    }

    const actions = element("div", "agent-ask-actions");
    const send = element("button", "agent-ask-send");
    send.type = "button";
    send.innerHTML = `<span class="agent-ask-send-icon">✈</span><span>Send answer</span>`;
    send.addEventListener("click", () => void this.submitAcpAskCard(card, selected));
    actions.append(send, element("span", "agent-ask-dismiss-hint", "Esc / Stop to dismiss"));
    body.appendChild(actions);
    card.appendChild(body);
    this.syncAcpAskSendState(card, selected);
    return card;
  }

  syncAcpAskSendState(card, selected) {
    const send = card.querySelector(".agent-ask-send");
    if (!send) return;
    const textarea = card.querySelector(".agent-ask-textarea");
    const customActive = card.querySelector(".agent-ask-custom")?.classList.contains("is-active");
    const hasText = Boolean(textarea?.value?.trim());
    send.disabled = card.classList.contains("is-submitting") || (selected.size === 0 && !(customActive && hasText));
  }

  async submitAcpAskCard(card, selected) {
    if (!this.answerAcpAsk || card.classList.contains("is-submitting")) return;
    const requestId = card.dataset.acpRequestId;
    const traceId = card.dataset.acpTraceId || this.activeTraceId;
    const conversationId = card.dataset.acpConversationId || this.conversation?.id;
    if (!requestId || !conversationId) return;
    const textarea = card.querySelector(".agent-ask-textarea");
    const customActive = card.querySelector(".agent-ask-custom")?.classList.contains("is-active");
    const text = textarea?.value?.trim() || "";
    const via = customActive && text ? "text" : "option";
    if (via === "option" && selected.size === 0) return;
    if (via === "text" && !text) return;

    card.classList.add("is-submitting");
    this.syncAcpAskSendState(card, selected);
    try {
      await this.answerAcpAsk({
        traceId,
        conversationId,
        requestId,
        ...(via === "option" ? { optionIds: [...selected] } : { text }),
      });
      card.classList.remove("is-pending", "is-submitting");
      card.classList.add("is-sealed");
      card.querySelectorAll("button, textarea").forEach((node) => { node.disabled = true; });
      const answerEl = element(
        "div",
        "agent-ask-answer",
        via === "text" ? `Answer: ${text}` : `Answer: ${[...selected].join(", ")}`,
      );
      card.querySelector(".agent-ask-body")?.appendChild(answerEl);
    } catch (error) {
      card.classList.remove("is-submitting");
      this.syncAcpAskSendState(card, selected);
      this.notify(error instanceof Error ? error.message : "Could not send answer", "error");
    }
  }

  sealStreamingMessage(message, meta) {
    if (!message) return;
    message.classList.remove("agent-pending");
    if (meta.status === "interrupted") message.classList.add("agent-message-interrupted");
    const mark = message.querySelector(".agent-message-mark");
    if (mark) mark.textContent = "✦";
    const metaLabel = message.querySelector(".agent-message-meta");
    if (metaLabel) metaLabel.textContent = meta.status === "interrupted" ? "Interrupted" : "NusaShell Agent";

    const identity = message.querySelector(".agent-message-identity");
    if (meta.steps?.length) {
      [...message.children].forEach((child) => {
        if (child !== identity) child.remove();
      });
      let lastStepModel = null;
      for (const step of meta.steps) {
        if (step.model && step.model !== lastStepModel) {
          const divider = this.modelDivider(step.model);
          if (divider) message.appendChild(divider);
          lastStepModel = step.model;
        }
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
      if (!message.querySelector(".agent-reasoning") && meta.reasoning?.trim()) {
        const disclosure = this.reasoningDisclosure(meta.reasoning);
        const bubble = message.querySelector(".agent-bubble");
        if (bubble) bubble.before(disclosure);
        else message.appendChild(disclosure);
      }

      if (!message.querySelector(".agent-tool-terminal, .agent-tool-stack") && meta.toolCalls?.length) {
        const activity = this.toolActivity(meta.toolCalls);
        const bubble = message.querySelector(".agent-bubble");
        if (bubble) bubble.before(activity);
        else message.appendChild(activity);
      }

      const bubble = message.querySelector(".agent-bubble");
      if (bubble) {
        const content = bubble.textContent || meta.text || meta.content || "";
        if (content) bubble.innerHTML = renderAssistantMarkdown(content);
      } else if (meta.text || meta.content) {
        const fallback = element("div", "agent-bubble");
        fallback.innerHTML = renderAssistantMarkdown(meta.text || meta.content || "");
        message.appendChild(fallback);
      }
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
    const copyText = meta.steps?.length
      ? meta.steps.filter((step) => step.type === "text").map((step) => step.content).join("\n\n")
      : (message.querySelector(".agent-bubble")?.textContent || meta.text || meta.content || "");
    copy.addEventListener("click", () => void this.copyMessage(copyText, copy));
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

function parseAskAnswer(output) {
  if (!output || typeof output !== "string") return null;
  try {
    const parsed = JSON.parse(output);
    const data = parsed?.result?.data ?? parsed?.data ?? parsed;
    if (!data || typeof data !== "object") return null;
    return {
      via: data.via === "text" ? "text" : "option",
      answer: typeof data.answer === "string" ? data.answer : "",
      optionIds: Array.isArray(data.optionIds) ? data.optionIds.map(String) : [],
    };
  } catch {
    return { via: "text", answer: output, optionIds: [] };
  }
}

function messageDetail(content) {
  return element("span", "agent-message-detail", content);
}

function shortModelName(model) {
  if (!model) return "";
  const parts = String(model).split("/");
  return parts[parts.length - 1] || model;
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
