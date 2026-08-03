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
import { estimateContextTokens, formatContextUsage, resolveContextBadgeTokens, shouldApplyAcpUiUpdate } from "./ai-model-ui.js";
import { inspectAttachmentContent, toDataUrl } from "./attachment-content.js";
import { CANVAS_ARTIFACT_MAX_SOURCE_BYTES, canvasArtifactId, resolveCanvasFence } from "./agent-canvas-detect.js";
import { bindCanvasZoom, renderArtifact } from "./agent-canvas-render.js";
import { subscribeSubagentEvents, subscribeSubagentStream } from "./subagent-event-helper.js";

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
    this.canvasEnabled = true;
    this.activeCanvasArtifact = null;
    this.activeSubagentRun = null;
    this.subagentStreamState = null;
    this.subagentStreamDisposer = null;
    this.activeSubagentCardStream = null;
    this.subagentEventDisposer = null;
  }

  async initialize() {
    if (!this.shell?.agentConversations) {
      this.notify("Conversation storage is unavailable. Restart NusaShell after rebuilding the preload.", "error");
      return;
    }
    this.bindEvents();
    this.bindCanvasControls();
    this.bindSubagentEvents();
    await this.loadCanvasEnabled();
    await this.refresh();
    if (this.conversations.length === 0) await this.create();
    else await this.open(this.conversations[0].id);
  }

  async loadCanvasEnabled() {
    try {
      const behavior = await this.shell?.appBehavior?.get();
      this.setCanvasEnabled(behavior ? behavior.canvasEnabled !== false : true);
    } catch {
      this.setCanvasEnabled(true);
    }
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
        conversationId: this.conversation?.id,
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
          if (card) {
            const next = this.updateStreamingToolCard(card, payload);
            if (next) streamState.toolCards.set(payload.callId, next);
          }
        },
        onContextUpdate: (payload) => {
          // Badge = approximate current prompt window fill, NOT cumulative
          // billing tokens. Pass the full event payload; the helper ignores
          // inputTokens (cumulative billing) so multi-round tool turns do not
          // inflate the badge ~N× the real window (BH-CTX-01/04).
          setContextStatus(resolveContextBadgeTokens({
            estimatedTokens: Number(payload?.estimatedTokens) || 0,
            inputTokens: Number(payload?.inputTokens) || 0,
            liveTokens,
          }));
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
        // The main process seals the assistant message off the renderer
        // critical path (via sealAgentTurn) so a renderer restart mid-turn
        // does not orphan the reply. Refresh from the store; if the sealed
        // message is missing (seal failed or no conversationId), fall back to
        // renderer-side append.
        this.conversation = await this.shell.agentConversations.get(this.conversation.id);
        const lastMessage = this.conversation?.messages.at(-1);
        const sealedByMain = lastMessage?.role === "assistant" && lastMessage?.traceId === result.traceId;
        if (!sealedByMain) {
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
        } else if (result.compaction) {
          // Main already saved the checkpoint; just refresh into memory.
          this.conversation = await this.shell.agentConversations.get(this.conversation.id);
        }
      } catch (error) {
        this.sealStreamingMessage(pending, result);
        status.textContent = "Response completed · local save failed";
        this.notify("The response completed but could not be saved locally.", "error");
        this.log("error", `Agent response persistence failed trace=${result.traceId}: ${error.message || String(error)}`);
        return;
      }
      if (result.compaction && !this.conversation?.checkpoint?.summary) {
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
      // refresh() already calls updateContextStatus() with an estimate from
      // persisted messages. Do NOT overwrite with result.usage.inputTokens —
      // that is cumulative billing across tool rounds, not the current window
      // fill, and would inflate the badge ~N× after multi-round turns.
      this.log("info", `Agent turn completed trace=${result.traceId} rounds=${result.rounds}`);
    } catch (error) {
      if (error.code === "AGENT_TURN_CANCELLED" && !turnEnded) {
        // Wait for the terminal turn_end event (published after in-flight
        // tools drain) before sealing, with a 2s fallback so the UI never
        // hangs on a missing event.
        await Promise.race([turnEndPromise, new Promise((r) => setTimeout(r, 2000))]);
      }
      const partial = error.details?.partial;
      const isCancel = error.code === "AGENT_TURN_CANCELLED";
      if (partial) {
        this.sealStreamingToolCardsIncomplete(streamState);
        this.sealStreamingMessage(pending, { ...partial, status: "interrupted" });
        if (pending && isCancel) pending.classList.add("agent-message-stopped");
        const interruptedMessage = {
          role: "assistant",
          content: isCancel
            ? `Turn stopped after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`
            : `Turn interrupted after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`,
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
          isCancel ? "Turn stopped." : `Turn failed: ${formatTurnError(error)}`,
          { error: true, retry: true },
        );
        status.textContent = isCancel ? "Turn stopped · ready to resume" : "Turn interrupted · ready to retry";
        this.log(isCancel ? "info" : "error", isCancel
          ? `Agent turn stopped trace=${this.activeTraceId}`
          : `Agent turn failed: ${formatTurnError(error)}`);
      } else if (isCancel) {
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
      } else {
        // Keep streamed UI visible even when the backend omitted a resume
        // snapshot (e.g. failure before any tool progress).
        const hasStream = Boolean(
          streamState
          && (streamState.streamedText || streamState.reasoningText || streamState.toolCards.size > 0),
        );
        if (pending && hasStream) {
          this.sealStreamingToolCardsIncomplete(streamState);
          this.sealStreamingMessage(pending, {
            content: streamState.streamedText || "",
            ...(streamState.reasoningText ? { reasoning: streamState.reasoningText } : {}),
          });
        } else {
          pending?.remove();
        }
        this.failedMessage = this.appendMessage(
          "assistant",
          `Turn failed: ${formatTurnError(error)}`,
          { error: true, retry: retryIsSafe },
        );
        status.textContent = retryIsSafe ? "Turn failed · ready to retry" : "Local conversation error";
        this.log("error", `Agent turn failed: ${formatTurnError(error)}`);
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
          if (card) {
            const next = this.updateStreamingToolCard(card, payload);
            if (next) streamState.toolCards.set(payload.callId, next);
          }
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
      this.failedMessage = this.appendMessage("assistant", `Turn failed: ${formatTurnError(error)}`, { error: true, retry: retryIsSafe });
      status.textContent = retryIsSafe ? "ACP turn failed · ready to retry" : "ACP turn error";
      this.log("error", `ACP turn failed: ${formatTurnError(error)}`);
    } finally {
      this.turnPending = false;
      this.activeTraceId = "";
      input.disabled = false;
      sendButton.disabled = false;
      stopButton.hidden = true;
      input.focus();
      // ACP turns never emitted a context badge update; refresh from persisted
      // messages so the badge reflects the current window fill after the turn.
      this.updateContextStatus();
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

  clearCanvasOnSwitch() {
    this.closeCanvasDrawerUi();
    this.activeCanvasArtifact = null;
  }

  updateWorkspaceLabel() {
    const label = $("#agent-workspace-label");
    if (!label) return;
    const ws = this.conversation?.workspace;
    // Handle both POSIX (/) and Windows (\) path separators — a simple
    // split("/") breaks on Windows paths like "D:\proj".
    label.textContent = ws ? ws.split(/[\\/]/).pop() || ws : "Home";
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
    const startedId = this.conversation.id;
    const providers = await this.shell.acpProviders.list();
    if (!shouldApplyAcpUiUpdate({ activeId: this.activeId, activeKind: this.conversation?.kind, startedId })) return;
    const descriptor = providers.find((p) => p.manifest.id === this.conversation.acp.providerId);
    if (!descriptor) return;
    try {
      const info = await this.ensureAcpSession(
        this.conversation.id,
        this.conversation.workspace,
        {
          providerId: descriptor.manifest.id,
          command: descriptor.config.command || descriptor.manifest.command,
          args: descriptor.config.args ?? descriptor.manifest.args,
          ...(descriptor.config.authMethodId || descriptor.manifest.authMethodId
            ? { authMethodId: descriptor.config.authMethodId || descriptor.manifest.authMethodId }
            : {}),
          ...(descriptor.config.preferredConfig || descriptor.manifest.preferredConfig
            ? { preferredConfig: descriptor.config.preferredConfig || descriptor.manifest.preferredConfig }
            : {}),
        },
      );
      if (!shouldApplyAcpUiUpdate({ activeId: this.activeId, activeKind: this.conversation?.kind, startedId })) return;
      this.acpConfigOptions = info?.configOptions ?? [];
      this.updateAcpModelLabel();
    } catch (error) {
      this.log("warn", `Failed to ensure ACP session: ${error.message || error}`);
    }
  }

  updateAcpModelLabel() {
    if (this.conversation?.kind !== "acp") return;
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
    const startedId = this.conversation.id;
    try {
      const info = await this.getAcpSessionInfo(this.conversation.id);
      if (!shouldApplyAcpUiUpdate({ activeId: this.activeId, activeKind: this.conversation?.kind, startedId })) return;
      this.acpConfigOptions = info?.configOptions ?? [];
      this.updateAcpModelLabel();
    } catch (error) {
      this.log("warn", `Failed to load ACP config options: ${error.message || error}`);
    }
  }

  async selectAcpConfigOption(configId, value) {
    if (this.conversation?.kind !== "acp" || !this.setAcpConfigOption) return;
    const startedId = this.conversation.id;
    try {
      const updated = await this.setAcpConfigOption(this.conversation.id, configId, value);
      if (!shouldApplyAcpUiUpdate({ activeId: this.activeId, activeKind: this.conversation?.kind, startedId })) return;
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
    this.conversation.messages.forEach((message, index) => this.appendMessage(message.role, message.content, { ...message, canvasMessageIndex: index }));
    this.detectOrphanedTurn();
    this.scrollToBottom();
    this.restoreCanvas();
    this.restoreSubpane();
  }

  /**
   * Surface an incomplete turn (trailing user message with no assistant reply)
   * as a retryable error banner. This happens when the renderer died mid-turn
   * before the main-side seal shipped; the user message is on disk but the
   * assistant reply was lost.
   */
  detectOrphanedTurn() {
    if (!this.conversation?.messages?.length) return;
    const last = this.conversation.messages.at(-1);
    if (last?.role !== "user") return;
    if (this.turnPending) return;
    this.failedMessage?.remove();
    this.failedMessage = this.appendMessage(
      "assistant",
      "Incomplete turn — the previous reply was lost when the app restarted. Retry to continue.",
      { error: true, retry: true },
    );
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
    if (role === "assistant" && !meta.pending && !meta.error) {
      this.enhanceCanvasFences(message, meta.canvasMessageIndex ?? this.currentMessageIndex());
    }
    return message;
  }

  currentMessageIndex() {
    const messages = this.conversation?.messages;
    return messages && messages.length > 0 ? messages.length - 1 : 0;
  }

  setCanvasEnabled(enabled) {
    this.canvasEnabled = Boolean(enabled);
    if (!this.canvasEnabled) this.closeCanvasSidebar();
  }

  bindCanvasControls() {
    $("#agent-canvas-close")?.addEventListener("click", () => this.closeCanvasSidebar());
    $("#agent-canvas-refresh")?.addEventListener("click", () => this.refreshCanvas());
    $("#agent-canvas-download")?.addEventListener("click", () => this.downloadCanvasSource());
    $("#agent-canvas-overlay")?.addEventListener("click", () => this.closeCanvasSidebar());
    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      const pane = $("#agent-canvas");
      if (!pane || pane.hidden || !pane.classList.contains("is-open")) return;
      this.closeCanvasSidebar();
    });
    $("#agent-subpane-close")?.addEventListener("click", () => this.closeSubpaneSidebar());
    $("#agent-subpane-overlay")?.addEventListener("click", () => this.closeSubpaneSidebar());
    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      const subpane = $("#agent-subpane");
      if (!subpane || subpane.hidden || !subpane.classList.contains("is-open")) return;
      this.closeSubpaneSidebar();
    });
  }

  bindSubagentEvents() {
    this.activeSubagentRun = null;
    this.subagentStreamState = null;
    this.subagentStreamDisposer = null;
    this.activeSubagentCardStream = null;
    this.subagentEventDisposer = subscribeSubagentEvents({
      onRunStarted: (p) => this.handleSubagentRunStarted(p),
      onRunEnded: (p) => this.handleSubagentRunEnded(p),
    });
  }

  handleSubagentRunStarted(p) {
    if (!this.conversation) return;
    const run = {
      id: `run-${p.runId}`,
      conversationId: this.conversation.id,
      sourceMessageId: String(this.conversation.messages?.length ?? 0),
      runId: p.runId,
      providerId: p.providerId,
      ...(p.title ? { title: p.title } : {}),
      prompt: p.prompt,
      status: "running",
      steps: [],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    };
    this.resetSubagentStreamState([], p.runId);
    this.shell.agentConversations.upsertSubagentRun(this.conversation.id, run).then((conv) => {
      this.conversation = conv;
      return this.shell.agentConversations.setActiveSubagentRun(this.conversation.id, run.runId);
    }).then((conv) => {
      this.conversation = conv;
    }).catch((err) => this.log?.("error", `Subagent run start persist failed: ${err}`));

    this.mountSubpane(run, { resumeStream: false, open: false });

    this.attachSubagentCardStream(p.runId);

    this.subagentStreamDisposer?.();
    this.subagentStreamDisposer = this.bindLiveSubagentStream(p.runId);
  }

  handleSubagentRunEnded(p) {
    this.subagentStreamDisposer?.();
    this.subagentStreamDisposer = null;

    const status = p.ok ? "ok" : "fail";
    this.setSubpaneStatus(`● ${status.toUpperCase()}`, p.ok ? "is-ok" : "is-fail");
    if (!p.ok && p.error) {
      this.setSubpaneError(p.error);
    }

    this.sealSubagentStreamSegment();
    const steps = this.snapshotSubagentSteps();
    if (this.activeSubagentRun) {
      this.activeSubagentRun = { ...this.activeSubagentRun, status, ...(p.summary ? { summary: p.summary } : {}), ...(p.error ? { error: p.error } : {}), ...(steps?.length ? { steps } : {}) };
    }
    this.renderSubagentStreamState();
    this.subagentStreamState = null;

    // Stop appending to the in-chat mini stream; the frozen tail stays
    // visible until the parent turn's tool card is replaced on tool_call_end.
    this.disposeSubagentCardStream();

    if (this.conversation) {
      this.shell.agentConversations.updateSubagentRunStatus(
        this.conversation.id,
        p.runId,
        status,
        {
          ...(p.summary ? { summary: p.summary } : {}),
          ...(p.error ? { error: p.error } : {}),
          ...(steps?.length ? { steps } : {}),
        },
      ).then((conv) => {
        this.conversation = conv;
      }).catch((err) => this.log?.("error", `Subagent run end persist failed: ${err}`));
    }
  }

  bindLiveSubagentStream(runId) {
    return subscribeSubagentStream(runId, {
      onDelta: (delta) => {
        this.appendSubpaneText(delta);
        this.appendCardStreamText(delta);
      },
      onReasoningDelta: (delta) => {
        this.appendSubpaneThought(delta);
        this.appendCardStreamThought(delta);
      },
      onToolCallStart: (params) => {
        const call = {
          id: params.callId,
          title: params.name,
          kind: params.kind || "unknown",
          status: params.status === "ok" ? "ok" : params.status === "fail" ? "fail" : "running",
          args: params.args,
          summary: summarizeToolArgs(params.args),
        };
        this.appendSubpaneToolCall(call, { persist: true });
        this.appendCardStreamToolCall(call);
      },
      onToolCallEnd: (params) => {
        const status = params.ok ? "ok" : "fail";
        this.updateSubpaneToolCall(params.callId, status, params.summary);
        this.updateCardStreamToolCall(params.callId, status, params.summary);
      },
      onPlan: (steps) => {
        this.appendSubpanePlan(steps, { persist: true });
        this.appendCardStreamPlan(steps);
      },
      onPermissionRequest: (payload) => {
        const card = this.createAcpPermissionCard(payload);
        const body = $("#agent-subpane-body");
        if (card && body) {
          body.appendChild(card);
          body.scrollTop = body.scrollHeight;
        }
      },
      onAskRequest: (payload) => {
        const card = this.createAcpAskCard(payload);
        const body = $("#agent-subpane-body");
        if (card && body) {
          body.appendChild(card);
          body.scrollTop = body.scrollHeight;
        }
      },
    });
  }

  openCanvasDrawerUi() {
    const pane = $("#agent-canvas");
    const overlay = $("#agent-canvas-overlay");
    if (!pane) return;
    pane.hidden = false;
    if (overlay) {
      overlay.hidden = false;
      // Next frame so opacity/transform transitions run after un-hiding.
      requestAnimationFrame(() => {
        overlay.classList.add("is-open");
        pane.classList.add("is-open");
      });
    } else {
      pane.classList.add("is-open");
    }
    $("#agent-canvas-close")?.focus();
  }

  closeCanvasDrawerUi() {
    const pane = $("#agent-canvas");
    const body = $("#agent-canvas-body");
    const overlay = $("#agent-canvas-overlay");
    pane?.classList.remove("is-open");
    overlay?.classList.remove("is-open");
    if (body) body.textContent = "";
    // Allow slide-out to finish before display:none, unless reduced motion.
    const hide = () => {
      if (pane) pane.hidden = true;
      if (overlay) overlay.hidden = true;
    };
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      hide();
      return;
    }
    window.setTimeout(hide, 260);
  }

  enhanceCanvasFences(messageEl, messageIndex) {
    if (!this.canvasEnabled || !messageEl) return;
    const conversationId = this.conversation?.id;
    if (!conversationId) return;
    const blocks = messageEl.querySelectorAll("pre > code");
    if (!blocks.length) return;
    let fenceIndex = 0;
    blocks.forEach((code) => {
      const langClass = [...code.classList].find((cls) => cls.startsWith("language-"));
      const lang = langClass ? langClass.slice("language-".length) : "";
      const rawSource = code.textContent ?? "";
      const resolved = resolveCanvasFence(lang, rawSource);
      if (!resolved) return;
      const pre = code.parentElement;
      if (!pre) return;
      const { kind, source } = resolved;
      const artifactId = canvasArtifactId(conversationId, String(messageIndex), fenceIndex);
      const tooLarge = source.length > CANVAS_ARTIFACT_MAX_SOURCE_BYTES;
      const title = `${kind} ${fenceIndex + 1}`;
      this.decorateCanvasFence(pre, code, { artifactId, kind, source, title, tooLarge, messageIndex, fenceIndex });
      fenceIndex += 1;
    });
  }

  decorateCanvasFence(pre, code, ctx) {
    if (ctx.kind === "html") {
      this.decorateHtmlFence(pre, code, ctx);
      return;
    }
    // svg / mermaid: auto-render inline; collapse the raw fence once render succeeds.
    const actions = element("div", "agent-canvas-fence-actions");
    const sidebar = element("button", "agent-canvas-fence-btn", "Sidebar");
    sidebar.type = "button";
    sidebar.addEventListener("click", () => void this.openCanvasSidebar(ctx));
    const showSource = element("button", "agent-canvas-fence-btn", "Show source");
    showSource.type = "button";
    const getPreview = () =>
      pre.parentElement?.querySelector(`.agent-canvas-inline[data-artifact-id="${cssEscape(ctx.artifactId)}"]`) ?? null;
    bindCanvasSourceToggle({ pre, showSource, getPreview });
    actions.append(sidebar, showSource);
    // Hide source optimistically; restore if render fails.
    pre.hidden = true;
    pre.after(actions);
    this.renderInlineCanvas(pre, code, ctx).then((ok) => {
      if (!ok) setCanvasSourceVisible({ pre, showSource, getPreview, visible: true });
    }).catch((error) => {
      setCanvasSourceVisible({ pre, showSource, getPreview, visible: true });
      this.log?.("error", `Canvas inline render failed: ${error.message || error}`);
    });
  }

  decorateHtmlFence(pre, code, ctx) {
    const lineCount = ctx.source.split("\n").length;
    const sizeHint = formatByteHint(ctx.source.length);
    const card = element("div", "agent-canvas-card");
    card.setAttribute("data-artifact-id", ctx.artifactId);

    const head = element("div", "agent-canvas-card-head");
    const badge = element("span", "agent-canvas-card-badge", ctx.kind.toUpperCase());
    const title = element("span", "agent-canvas-card-title", ctx.title);
    const meta = element("span", "agent-canvas-card-meta", `${lineCount} lines · ${sizeHint}`);
    head.append(badge, title, meta);

    const actions = element("div", "agent-canvas-card-actions");
    const sidebar = element("button", "agent-canvas-fence-btn", "Sidebar");
    sidebar.type = "button";
    sidebar.addEventListener("click", () => void this.openCanvasSidebar(ctx));
    const showSource = element("button", "agent-canvas-fence-btn", "Show source");
    showSource.type = "button";
    const getPreview = () =>
      pre.parentElement?.querySelector(`.agent-canvas-inline-preview[data-artifact-id="${cssEscape(ctx.artifactId)}"]`) ?? null;
    bindCanvasSourceToggle({ pre, showSource, getPreview });
    actions.append(sidebar, showSource);

    card.append(head, actions);
    // Collapse the source by default; Show source reveals it (and hides the preview).
    pre.hidden = true;
    pre.after(card);
    void this.mountInlineHtmlPreview(pre, card, ctx).catch((error) => {
      setCanvasSourceVisible({ pre, showSource, getPreview, visible: true });
      this.log?.("error", `Inline HTML render failed: ${error.message || error}`);
    });
  }

  async mountInlineHtmlPreview(pre, card, ctx) {
    const existing = pre.parentElement?.querySelector(`.agent-canvas-inline-preview[data-artifact-id="${cssEscape(ctx.artifactId)}"]`);
    if (existing) return;
    const result = await renderArtifact({ kind: ctx.kind, source: ctx.source });
    const container = element("div", "agent-canvas-inline-preview");
    container.setAttribute("data-artifact-id", ctx.artifactId);
    if (result.type === "html") {
      const iframe = document.createElement("iframe");
      iframe.setAttribute("sandbox", "allow-scripts");
      iframe.setAttribute("aria-label", ctx.title);
      iframe.srcdoc = result.srcdoc;
      container.appendChild(iframe);
    } else if (result.type === "error") {
      const errorBox = element("div", "agent-canvas-error", result.message || "Could not render this artifact.");
      container.appendChild(errorBox);
    }
    // Visual first (like svg/mermaid), then the card chrome, then the collapsed source.
    card.before(container);
  }

  async renderInlineCanvas(pre, code, ctx) {
    const container = element("div", "agent-canvas-inline");
    container.setAttribute("aria-label", ctx.title);
    container.setAttribute("data-artifact-id", ctx.artifactId);
    const result = await renderArtifact({ kind: ctx.kind, source: ctx.source });
    if (result.type === "svg" && result.svg) {
      container.innerHTML = result.svg;
      bindCanvasZoom(container);
      // Place diagram above the fence actions / collapsed source.
      const actions = pre.nextElementSibling?.classList?.contains("agent-canvas-fence-actions")
        ? pre.nextElementSibling
        : null;
      if (actions) actions.before(container);
      else pre.before(container);
      return true;
    }
    // Leave the original code block visible; do not crash on a bad diagram.
    return false;
  }

  async openCanvasSidebar(ctx) {
    if (!this.canvasEnabled || !this.conversation) return;
    if (ctx.tooLarge) {
      this.notify(`Artifact is larger than ${Math.round(CANVAS_ARTIFACT_MAX_SOURCE_BYTES / 1024)}KB and cannot be previewed.`, "error");
      return;
    }
    const conversationId = this.conversation.id;
    const timestamp = new Date().toISOString();
    const artifact = {
      id: ctx.artifactId,
      conversationId,
      sourceMessageId: String(ctx.messageIndex),
      fenceIndex: ctx.fenceIndex,
      kind: ctx.kind,
      title: ctx.title,
      source: ctx.source,
      createdAt: timestamp,
      updatedAt: timestamp,
    };
    try {
      this.conversation = await this.shell.agentConversations.upsertCanvasArtifact(conversationId, artifact);
      this.conversation = await this.shell.agentConversations.setActiveCanvasArtifact(conversationId, artifact.id);
    } catch (error) {
      this.notify(`Could not save canvas artifact: ${error.message || error}`, "error");
    }
    this.activeCanvasArtifact = artifact;
    this.mountCanvas(artifact);
  }

  restoreCanvas() {
    if (!this.canvasEnabled || !this.conversation) {
      this.closeCanvasSidebar();
      return;
    }
    const activeId = this.conversation.activeCanvasArtifactId;
    const artifact = this.conversation.canvasArtifacts?.find((item) => item.id === activeId);
    if (!artifact) {
      this.closeCanvasSidebar();
      return;
    }
    this.activeCanvasArtifact = artifact;
    this.mountCanvas(artifact);
  }

  restoreSubpane() {
    if (!this.conversation) return;
    const activeId = this.conversation.activeSubagentRunId;
    const run = this.conversation.subagentRuns?.find((item) => item.runId === activeId);
    if (!run) return;
    // Keep stream subscription for an in-flight run, but do not auto-open the drawer.
    if (run.status === "running") {
      this.mountSubpane(run, { resumeStream: true, open: false });
    }
  }

  async mountCanvas(artifact) {
    const pane = $("#agent-canvas");
    const body = $("#agent-canvas-body");
    const badge = $("#agent-canvas-badge");
    const title = $("#agent-canvas-title");
    const hint = $("#agent-canvas-hint");
    if (!pane || !body || !badge || !title) return;
    badge.textContent = artifact.kind.toUpperCase();
    title.textContent = artifact.title;
    body.textContent = "";
    if (hint) hint.hidden = true;
    this.openCanvasDrawerUi();
    this.activeCanvasArtifact = artifact;
    const result = await renderArtifact({ kind: artifact.kind, source: artifact.source });
    if (result.type === "html") {
      const iframe = document.createElement("iframe");
      iframe.setAttribute("sandbox", "allow-scripts");
      iframe.setAttribute("aria-label", artifact.title);
      iframe.srcdoc = result.srcdoc;
      body.appendChild(iframe);
    } else if (result.type === "svg" && result.svg) {
      const wrap = element("div", "agent-canvas-svg");
      wrap.innerHTML = result.svg;
      bindCanvasZoom(wrap);
      body.appendChild(wrap);
      if (hint) {
        hint.textContent = "Ctrl + scroll to zoom · double-click to reset · Esc to close";
        hint.hidden = false;
      }
    } else if (result.type === "error") {
      const errorBox = element("div", "agent-canvas-error", result.message || "Could not render this artifact.");
      body.appendChild(errorBox);
      if (hint) {
        hint.textContent = "The source is still available in the message.";
        hint.hidden = false;
      }
    }
  }

  closeCanvasSidebar() {
    this.closeCanvasDrawerUi();
    this.activeCanvasArtifact = null;
    if (this.conversation?.activeCanvasArtifactId) {
      void this.shell?.agentConversations?.setActiveCanvasArtifact(this.conversation.id, null).catch(() => undefined);
    }
  }

  refreshCanvas() {
    if (this.activeCanvasArtifact) void this.mountCanvas(this.activeCanvasArtifact);
  }

  downloadCanvasSource() {
    const artifact = this.activeCanvasArtifact;
    if (!artifact) return;
    const blob = new Blob([artifact.source], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${artifact.kind}-${artifact.fenceIndex + 1}.${artifact.kind === "mermaid" ? "mmd" : artifact.kind}`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
  }

  // ===== Subagent side pane =====

  openSubpaneDrawerUi() {
    const pane = $("#agent-subpane");
    const overlay = $("#agent-subpane-overlay");
    if (!pane) return;
    this.closeCanvasSidebar();
    pane.hidden = false;
    if (overlay) {
      overlay.hidden = false;
      requestAnimationFrame(() => {
        overlay.classList.add("is-open");
        pane.classList.add("is-open");
      });
    } else {
      pane.classList.add("is-open");
    }
    $("#agent-subpane-close")?.focus();
  }

  closeSubpaneDrawerUi() {
    const pane = $("#agent-subpane");
    const overlay = $("#agent-subpane-overlay");
    pane?.classList.remove("is-open");
    overlay?.classList.remove("is-open");
    const hide = () => {
      if (pane) pane.hidden = true;
      if (overlay) overlay.hidden = true;
    };
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      hide();
      return;
    }
    window.setTimeout(hide, 260);
  }

  closeSubpaneSidebar() {
    this.closeSubpaneDrawerUi();
    if (this.activeSubagentRun?.status === "running") return;
    this.activeSubagentRun = null;
    if (this.conversation?.activeSubagentRunId) {
      void this.shell?.agentConversations?.setActiveSubagentRun(this.conversation.id, null).catch(() => undefined);
    }
  }

  mountSubpane(run, options = {}) {
    const pane = $("#agent-subpane");
    const body = $("#agent-subpane-body");
    const badge = $("#agent-subpane-badge");
    const title = $("#agent-subpane-title");
    const status = $("#agent-subpane-status");
    if (!pane || !body) return;
    if (badge) badge.textContent = (run.providerId || "—").slice(0, 6).toUpperCase();
    if (title) title.textContent = run.title || "Subagent";
    if (status) {
      status.textContent = `● ${run.status.toUpperCase()}`;
      status.className = "agent-subpane-status";
      if (run.status === "running") status.classList.add("is-running");
      else if (run.status === "ok") status.classList.add("is-ok");
      else if (run.status === "fail" || run.status === "cancelled") status.classList.add("is-fail");
    }
    this.activeSubagentRun = run;
    if (run.status === "running") {
      if (!this.subagentStreamState || this.subagentStreamState.runId !== run.runId) {
        this.resetSubagentStreamState(run.steps ?? [], run.runId);
      }
      this.renderSubagentStreamState();
      if (options.resumeStream && !this.subagentStreamDisposer) {
        this.subagentStreamDisposer = this.bindLiveSubagentStream(run.runId);
      }
    } else {
      body.textContent = "";
      if (run.steps?.length) this.renderSubpaneSteps(run.steps);
      if (run.error) this.setSubpaneError(run.error);
      this.subagentStreamState = null;
      this.subagentStreamDisposer?.();
      this.subagentStreamDisposer = null;
    }
    if (options.open) this.openSubpaneDrawerUi();
  }

  resetSubagentStreamState(seedSteps = [], runId = this.activeSubagentRun?.runId) {
    // Live stream stays in memory while the (blocking) run is active.
    // Durable steps are flushed once on run end — no mid-stream disk writes,
    // no temp-file artifact spool (defer that until async subagent exists).
    this.subagentStreamState = {
      runId,
      steps: Array.isArray(seedSteps) ? [...seedSteps] : [],
      lastKind: null,
      textContent: "",
      thoughtContent: "",
      textEl: null,
      thoughtEl: null,
    };
  }

  sealSubagentStreamSegment() {
    const state = this.subagentStreamState;
    if (!state) return;
    if (state.lastKind === "text" && state.textContent.trim()) {
      state.steps.push({ type: "text", content: state.textContent });
    } else if (state.lastKind === "reasoning" && state.thoughtContent.trim()) {
      state.steps.push({ type: "reasoning", content: state.thoughtContent });
    }
    state.lastKind = null;
    state.textContent = "";
    state.thoughtContent = "";
    state.textEl = null;
    state.thoughtEl = null;
  }

  snapshotSubagentSteps() {
    const state = this.subagentStreamState;
    if (!state) return this.activeSubagentRun?.steps;
    const steps = [...state.steps];
    if (state.lastKind === "text" && state.textContent) {
      steps.push({ type: "text", content: state.textContent });
    } else if (state.lastKind === "reasoning" && state.thoughtContent) {
      steps.push({ type: "reasoning", content: state.thoughtContent });
    }
    return sanitizeAssistantSteps(steps) ?? steps;
  }

  renderSubpaneSteps(steps) {
    if (!Array.isArray(steps)) return;
    const body = $("#agent-subpane-body");
    if (!body) return;
    for (const step of steps) {
      if (step.type === "text" && typeof step.content === "string") {
        const el = element("div", "agent-subpane-text agent-bubble");
        el.innerHTML = renderAssistantMarkdown(step.content);
        body.appendChild(el);
      } else if (step.type === "reasoning" && typeof step.content === "string") {
        body.appendChild(this.reasoningDisclosure(step.content));
      } else if (step.type === "tool_calls" && Array.isArray(step.calls)) {
        body.appendChild(this.toolActivity(step.calls.map((call) => ({
          id: call.id,
          name: call.name,
          ok: call.ok !== false,
          ...(call.args ? { args: call.args } : {}),
          ...(call.output ? { output: call.output } : {}),
          ...(call.error ? { error: call.error } : {}),
        }))));
      } else if (step.type === "plan" && Array.isArray(step.steps)) {
        this.appendSubpanePlan(step.steps, { persist: false });
      }
    }
  }

  renderSubagentStreamState() {
    const body = $("#agent-subpane-body");
    const state = this.subagentStreamState;
    if (!body || !state) return;
    body.textContent = "";
    this.renderSubpaneSteps(state.steps);
    if (state.lastKind === "text" && state.textContent) {
      state.textEl = element("div", "agent-subpane-text agent-bubble");
      state.textEl.innerHTML = renderAssistantMarkdown(state.textContent);
      body.appendChild(state.textEl);
    } else if (state.lastKind === "reasoning" && state.thoughtContent) {
      state.thoughtEl = this.createStreamingReasoningBlock();
      const content = state.thoughtEl.querySelector(".agent-reasoning-content");
      if (content) content.innerHTML = renderReasoningMarkdown(state.thoughtContent);
      body.appendChild(state.thoughtEl);
    }
    body.scrollTop = body.scrollHeight;
  }

  appendSubpaneThought(delta) {
    if (!this.subagentStreamState) this.resetSubagentStreamState([]);
    const state = this.subagentStreamState;
    if (state.lastKind !== "reasoning") {
      this.sealSubagentStreamSegment();
      state.lastKind = "reasoning";
      state.thoughtContent = "";
      state.thoughtEl = null;
    }
    state.thoughtContent += delta;
    const body = $("#agent-subpane-body");
    if (!body) return;
    if (!state.thoughtEl) {
      state.thoughtEl = this.createStreamingReasoningBlock();
      body.appendChild(state.thoughtEl);
    }
    const content = state.thoughtEl.querySelector(".agent-reasoning-content");
    if (content) content.innerHTML = renderReasoningMarkdown(state.thoughtContent);
    body.scrollTop = body.scrollHeight;
  }

  appendSubpaneText(delta) {
    if (!this.subagentStreamState) this.resetSubagentStreamState([]);
    const state = this.subagentStreamState;
    if (state.lastKind !== "text") {
      this.sealSubagentStreamSegment();
      state.lastKind = "text";
      state.textContent = "";
      state.textEl = null;
    }
    state.textContent += delta;
    const body = $("#agent-subpane-body");
    if (!body) return;
    if (!state.textEl) {
      state.textEl = element("div", "agent-subpane-text agent-bubble");
      body.appendChild(state.textEl);
    }
    state.textEl.innerHTML = renderAssistantMarkdown(state.textContent);
    body.scrollTop = body.scrollHeight;
  }

  appendSubpaneToolCall(call, options = {}) {
    const body = $("#agent-subpane-body");
    if (!body) return;
    const args = call.args && typeof call.args === "object" && !Array.isArray(call.args) ? call.args : undefined;
    const hasArgs = args && Object.keys(args).length > 0;
    if (options.persist !== false) {
      if (!this.subagentStreamState) this.resetSubagentStreamState([]);
      this.sealSubagentStreamSegment();
      const state = this.subagentStreamState;
      state.steps.push({
        type: "tool_calls",
        calls: [{
          id: call.id,
          name: call.title || "tool",
          ok: call.status !== "fail",
          ...(hasArgs ? { args } : {}),
          ...(call.summary ? { output: String(call.summary).slice(0, 12_000) } : {}),
        }],
      });
    }
    const terminal = this.toolTerminal({
      id: call.id,
      name: call.title || "tool",
      ok: call.status !== "fail",
      ...(hasArgs ? { args } : {}),
      ...(call.summary ? { output: call.summary } : {}),
    });
    if (call.status === "running" || call.status === "pending") {
      terminal.classList.add("is-running");
      terminal.classList.remove("is-success", "is-error");
      const meta = terminal.querySelector(".agent-tool-terminal-meta");
      if (meta) meta.textContent = "Running";
    }
    terminal.dataset.callId = call.id;
    body.appendChild(terminal);
    body.scrollTop = body.scrollHeight;
  }

  updateSubpaneToolCall(callId, status, summary) {
    const body = $("#agent-subpane-body");
    if (!body) return;
    const el = body.querySelector(`.agent-tool-terminal[data-call-id="${CSS.escape(callId)}"]`);
    if (el) {
      el.classList.remove("is-running", "is-success", "is-error");
      if (status === "ok") el.classList.add("is-success");
      else if (status === "fail") el.classList.add("is-error");
      const meta = el.querySelector(".agent-tool-terminal-meta");
      if (meta) meta.textContent = status === "ok" ? "OK" : status === "fail" ? "FAIL" : "Running";
      if (summary) {
        const output = el.querySelector(".agent-tool-terminal-output");
        if (output) {
          output.innerHTML = renderToolCodeHtml(String(summary).slice(0, 12_000));
          output.classList.toggle("is-error", status === "fail");
        }
      }
    }
    const state = this.subagentStreamState;
    if (state) {
      for (let i = state.steps.length - 1; i >= 0; i -= 1) {
        const step = state.steps[i];
        if (step.type !== "tool_calls") continue;
        const call = step.calls.find((item) => item.id === callId);
        if (!call) continue;
        const nextCalls = step.calls.map((item) => (
          item.id === callId
            ? {
                ...item,
                ok: status !== "fail",
                ...(summary ? { output: String(summary).slice(0, 12_000) } : {}),
              }
            : item
        ));
        state.steps[i] = { type: "tool_calls", calls: nextCalls };
        break;
      }
    }
  }

  appendSubpanePlan(steps, options = {}) {
    const body = $("#agent-subpane-body");
    if (!body) return;
    if (options.persist !== false) {
      if (!this.subagentStreamState) this.resetSubagentStreamState([]);
      this.sealSubagentStreamSegment();
      const state = this.subagentStreamState;
      state.steps = state.steps.filter((step) => step.type !== "plan");
      state.steps.push({
        type: "plan",
        steps: (steps ?? []).map((step) => ({
          text: String(step.text ?? ""),
          ...(step.done ? { done: true } : {}),
        })),
      });
    }
    const existing = body.querySelector(".agent-subpane-plan");
    if (existing) existing.remove();
    const plan = element("div", "agent-subpane-plan");
    plan.append(element("div", "agent-subpane-plan-title", "Plan"));
    for (const step of steps) {
      const stepEl = element("div", `agent-subpane-plan-step ${step.done ? "agent-subpane-plan-step-done" : "agent-subpane-plan-step-pending"}`);
      stepEl.textContent = `${step.done ? "✓" : "○"} ${step.text}`;
      plan.appendChild(stepEl);
    }
    body.appendChild(plan);
    body.scrollTop = body.scrollHeight;
  }

  setSubpaneError(message) {
    const body = $("#agent-subpane-body");
    if (!body) return;
    const existing = body.querySelector(".agent-subpane-error");
    if (existing) existing.remove();
    const el = element("div", "agent-subpane-error", formatSubagentError(message));
    body.appendChild(el);
    body.scrollTop = body.scrollHeight;
  }

  setSubpaneStatus(statusText, cls) {
    const status = $("#agent-subpane-status");
    if (!status) return;
    status.textContent = statusText;
    status.className = "agent-subpane-status";
    if (cls) status.classList.add(cls);
  }

  // ---- In-chat subagent card mini activity stream ----
  // Mirrors the full subpane log into a compact, scrollable viewport inside
  // the running subagent card. One event fan-out, two views (subpane + card).

  attachSubagentCardStream(runId) {
    const card = document.querySelector(`.agent-subagent-card[data-streaming-subagent="1"]`)
      || document.querySelector(`.agent-subagent-card[data-run-id="${CSS.escape(runId)}"]`);
    if (!card) return null;
    card.dataset.runId = runId;
    let stream = card.querySelector(".agent-subagent-card-stream");
    if (!stream) {
      stream = element("div", "agent-subagent-card-stream");
      stream.setAttribute("aria-label", "Subagent live activity");
      stream.addEventListener("click", (event) => event.stopPropagation());
      stream.addEventListener("scroll", () => {
        const state = this.activeSubagentCardStream;
        if (!state || state.el !== stream) return;
        state.pinned = stream.scrollTop + stream.clientHeight >= stream.scrollHeight - 4;
      });
      card.append(stream);
    }
    const state = {
      runId,
      card,
      el: stream,
      lastKind: null,
      textContent: "",
      thoughtContent: "",
      textRow: null,
      thoughtRow: null,
      toolRows: new Map(),
      pinned: true,
    };
    this.activeSubagentCardStream = state;
    return state;
  }

  stickCardStreamToBottom(state) {
    if (!state?.el || !state.pinned) return;
    state.el.scrollTop = state.el.scrollHeight;
  }

  appendCardStreamRow(state, kind, mark, text, { key = null } = {}) {
    if (!state?.el) return null;
    const row = element("div", `agent-subagent-card-stream-row is-${kind}`);
    row.append(
      element("span", "agent-subagent-card-stream-mark", mark),
      element("span", "agent-subagent-card-stream-text", text),
    );
    state.el.appendChild(row);
    if (key) state.toolRows.set(key, row);
    this.pruneCardStreamRows(state);
    this.stickCardStreamToBottom(state);
    return row;
  }

  pruneCardStreamRows(state) {
    if (!state?.el) return;
    const max = 50;
    const rows = state.el.querySelectorAll(".agent-subagent-card-stream-row");
    if (rows.length <= max) return;
    const drop = rows.length - max;
    for (let i = 0; i < drop; i += 1) rows[i].remove();
  }

  sealCardStreamSegment(state) {
    if (!state) return;
    state.lastKind = null;
    state.textContent = "";
    state.thoughtContent = "";
    state.textRow = null;
    state.thoughtRow = null;
  }

  appendCardStreamThought(delta) {
    const state = this.activeSubagentCardStream;
    if (!state) return;
    if (state.lastKind !== "reasoning") {
      this.sealCardStreamSegment(state);
      state.lastKind = "reasoning";
      state.thoughtContent = "";
      state.thoughtRow = this.appendCardStreamRow(state, "thinking", "⌁", "Thinking…");
    }
    state.thoughtContent += delta;
    if (state.thoughtRow) {
      const text = state.thoughtRow.querySelector(".agent-subagent-card-stream-text");
      if (text) text.textContent = truncateCardStreamLine(state.thoughtContent, 120);
    }
    this.stickCardStreamToBottom(state);
  }

  appendCardStreamText(delta) {
    const state = this.activeSubagentCardStream;
    if (!state) return;
    if (state.lastKind !== "text") {
      this.sealCardStreamSegment(state);
      state.lastKind = "text";
      state.textContent = "";
      state.textRow = this.appendCardStreamRow(state, "text", "·", "");
    }
    state.textContent += delta;
    if (state.textRow) {
      const text = state.textRow.querySelector(".agent-subagent-card-stream-text");
      if (text) text.textContent = truncateCardStreamLine(state.textContent, 160);
    }
    this.stickCardStreamToBottom(state);
  }

  appendCardStreamToolCall(call) {
    const state = this.activeSubagentCardStream;
    if (!state) return;
    this.sealCardStreamSegment(state);
    const meta = summarizeToolArgs(call.args) || (call.status === "fail" ? "failed" : "running");
    const mark = call.status === "fail" ? "✕" : call.status === "ok" ? "✓" : "›";
    const label = `${call.title || "tool"} ${meta}`.trim();
    this.appendCardStreamRow(state, "tool", mark, label, { key: call.id });
  }

  updateCardStreamToolCall(callId, status, summary) {
    const state = this.activeSubagentCardStream;
    if (!state) return;
    const row = state.toolRows.get(callId);
    if (!row) return;
    row.classList.remove("is-running", "is-ok", "is-fail");
    row.classList.add(status === "ok" ? "is-ok" : status === "fail" ? "is-fail" : "is-running");
    const mark = row.querySelector(".agent-subagent-card-stream-mark");
    if (mark) mark.textContent = status === "fail" ? "✕" : status === "ok" ? "✓" : "›";
    if (summary) {
      const text = row.querySelector(".agent-subagent-card-stream-text");
      if (text) {
        const base = text.dataset.label || text.textContent?.split(" — ")[0] || text.textContent || "";
        if (!text.dataset.label) text.dataset.label = base;
        text.textContent = `${base} — ${truncateCardStreamLine(String(summary), 80)}`;
      }
    }
    this.stickCardStreamToBottom(state);
  }

  appendCardStreamPlan(steps) {
    const state = this.activeSubagentCardStream;
    if (!state) return;
    this.sealCardStreamSegment(state);
    const done = (steps ?? []).filter((s) => s.done).length;
    const total = (steps ?? []).length;
    this.appendCardStreamRow(state, "plan", "📋", `Plan ${done}/${total}`);
  }

  disposeSubagentCardStream() {
    this.activeSubagentCardStream = null;
  }

  renderSubagentCard(run) {
    const card = element("div", "agent-subagent-card");
    card.dataset.runId = run.runId || "";
    const head = element("div", "agent-subagent-card-head");
    head.addEventListener("click", () => {
      const latest = this.conversation?.subagentRuns?.find((item) => item.runId === run.runId) ?? run;
      if (this.conversation) {
        void this.shell.agentConversations.setActiveSubagentRun(this.conversation.id, latest.runId).catch(() => undefined);
      }
      const body = $("#agent-subpane-body");
      const samePreparedRun = this.activeSubagentRun?.runId === latest.runId
        && (body?.childNodes.length ?? 0) > 0;
      if (samePreparedRun) {
        this.openSubpaneDrawerUi();
        return;
      }
      this.mountSubpane(latest, { resumeStream: latest.status === "running", open: true });
    });
    const meta = element("div", "agent-subagent-card-meta");
    meta.append(
      element("span", "agent-subagent-card-badge", (run.providerId || "—").slice(0, 6).toUpperCase()),
      element("span", "agent-subagent-card-title", run.title || "Subagent run"),
    );
    const statusEl = element("span", `agent-subagent-card-status ${run.status === "running" ? "is-running" : run.status === "ok" ? "is-ok" : run.status === "fail" || run.status === "cancelled" ? "is-fail" : ""}`, `● ${run.status.toUpperCase()}`);
    head.append(meta, statusEl);
    card.appendChild(head);
    if (run.status === "running") {
      const stream = element("div", "agent-subagent-card-stream");
      stream.setAttribute("aria-label", "Subagent live activity");
      stream.addEventListener("click", (event) => event.stopPropagation());
      stream.addEventListener("scroll", () => {
        const state = this.activeSubagentCardStream;
        if (!state || state.el !== stream) return;
        state.pinned = stream.scrollTop + stream.clientHeight >= stream.scrollHeight - 4;
      });
      card.append(stream);
    }
    if (run.summary) {
      const summaryEl = element("div", "agent-subagent-card-summary");
      summaryEl.innerHTML = renderAssistantMarkdown(run.summary);
      card.append(summaryEl);
    }
    if (run.error) {
      card.append(element("div", "agent-subagent-card-error", run.error));
    }
    return card;
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
      } else if (toolCall.name === "subagent") {
        const card = this.createSubagentToolCard(toolCall);
        if (card) stack.appendChild(card);
      } else {
        stack.appendChild(this.toolTerminal(toolCall));
      }
    });
    return stack;
  }

  createSubagentToolCard(toolCall) {
    let result = {};
    try {
      result = typeof toolCall.output === "string" ? JSON.parse(toolCall.output) : (toolCall.output ?? {});
    } catch { /* not JSON */ }
    if (!result || typeof result !== "object") result = {};
    const runId = result.runId;
    const providerId = result.providerId || toolCall.args?.provider_id || "—";
    const title = toolCall.args?.title || result.title || "Subagent run";
    const status = result.ok === true ? "ok" : result.ok === false ? "fail" : "running";
    const summary = result.summary || "";
    const error = formatSubagentError(result.error) || formatSubagentError(toolCall.error);
    const run = {
      runId: runId || toolCall.id,
      providerId,
      title,
      status,
      ...(summary ? { summary } : {}),
      ...(error ? { error } : {}),
    };
    return this.renderSubagentCard(run);
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
    if (name === "subagent") {
      const title = typeof args?.title === "string" && args.title.trim() ? args.title.trim() : "Subagent run";
      const providerId = typeof args?.provider_id === "string" && args.provider_id ? args.provider_id : "…";
      const card = this.renderSubagentCard({
        runId: callId,
        providerId,
        title,
        status: "running",
      });
      card.dataset.callId = callId;
      card.dataset.streamingSubagent = "1";
      if (args && typeof args === "object") card._toolArgs = args;
      return card;
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
      return card;
    }
    if (card.dataset.streamingSubagent === "1" || card.classList.contains("agent-subagent-card")) {
      const sealed = this.createSubagentToolCard({
        id: payload.callId || card.dataset.callId,
        name: "subagent",
        ok: payload.ok !== false,
        args: payload.args && typeof payload.args === "object" ? payload.args : card._toolArgs,
        output: payload.output,
        error: payload.error,
      });
      if (sealed) {
        card.replaceWith(sealed);
        return sealed;
      }
      return card;
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
    return card;
  }

  sealStreamingToolCardsIncomplete(streamState) {
    if (!streamState?.toolCards) return;
    for (const [callId, card] of streamState.toolCards.entries()) {
      if (!card) continue;
      if (card.classList.contains("agent-ask-card")) {
        if (!card.classList.contains("is-sealed")) {
          card.classList.remove("is-pending", "is-submitting");
          card.classList.add("is-sealed", "is-error");
        }
        continue;
      }
      if (card.dataset.streamingSubagent === "1" || (card.classList.contains("agent-subagent-card") && card.querySelector(".agent-subagent-card-status.is-running"))) {
        const sealed = this.createSubagentToolCard({
          id: callId,
          name: "subagent",
          ok: false,
          args: card._toolArgs,
          error: "Subagent run did not finish before the parent turn ended.",
        });
        if (sealed) {
          card.replaceWith(sealed);
          streamState.toolCards.set(callId, sealed);
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
        const meta = card.querySelector(".agent-tool-terminal-meta");
        if (meta) meta.textContent = "Incomplete";
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
    const hasOptions = selected.size > 0;
    const hasText = customActive && text.length > 0;
    if (!hasOptions && !hasText) return;

    // When both options and custom text are present (multi-select + free text),
    // send both — the backend combines them into a single answer.
    const via = hasOptions ? "option" : "text";
    card.classList.add("is-submitting");
    this.syncAskSendState(card, selected);
    try {
      await this.answerAsk({
        traceId: this.activeTraceId,
        callId,
        via,
        ...(hasOptions ? { optionIds: [...selected] } : {}),
        ...(hasText ? { text } : {}),
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
    // Prefer the ACP session conversationId from the event (subagent runs use
    // `subagent:<runId>`, not the parent chat id).
    const conversationId = payload.conversationId || this.conversation?.id;
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
    if (meta.status !== "interrupted") {
      this.enhanceCanvasFences(message, this.currentMessageIndex());
    }
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

function truncateCardStreamLine(text, max = 120) {
  const flat = String(text ?? "").replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max)}…` : flat;
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
      text: typeof data.text === "string" ? data.text : "",
    };
  } catch {
    return { via: "text", answer: output, optionIds: [], text: "" };
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

function formatByteHint(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Show source replaces the inline preview (does not stack under it).
 * @param {{ pre: HTMLElement, showSource: HTMLButtonElement, getPreview: () => HTMLElement | null | undefined, visible: boolean }} opts
 */
function setCanvasSourceVisible({ pre, showSource, getPreview, visible }) {
  pre.classList.add("agent-canvas-source");
  pre.hidden = !visible;
  const preview = getPreview?.() ?? null;
  if (preview) preview.hidden = visible;
  showSource.textContent = visible ? "Hide source" : "Show source";
  showSource.setAttribute("aria-expanded", String(visible));
}

/**
 * @param {{ pre: HTMLElement, showSource: HTMLButtonElement, getPreview: () => HTMLElement | null | undefined }} opts
 */
function bindCanvasSourceToggle(opts) {
  opts.pre.classList.add("agent-canvas-source");
  opts.showSource.setAttribute("aria-expanded", "false");
  opts.showSource.addEventListener("click", () => {
    const willShowSource = opts.pre.hidden;
    setCanvasSourceVisible({ ...opts, visible: willShowSource });
  });
}

function cssEscape(value) {
  if (typeof CSS !== "undefined" && typeof CSS.escape === "function") return CSS.escape(value);
  return String(value).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
}

function formatTurnError(error) {
  const message = error?.message || "Unknown error";
  const cause = typeof error?.details?.cause === "string" ? error.details.cause.trim() : "";
  if (!cause || message.includes(cause)) return message;
  return `${message}: ${cause}`;
}

/** Coerce subagent tool/event error payloads (string or `{message}`) for UI text. */
function formatSubagentError(error) {
  if (!error) return "";
  if (typeof error === "string") return error;
  if (typeof error === "object" && typeof error.message === "string" && error.message) return error.message;
  try {
    return JSON.stringify(error);
  } catch {
    return String(error);
  }
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
