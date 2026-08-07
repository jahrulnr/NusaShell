import {
  buildAgentContext,
  buildContinueContext,
  classifyTurnError,
  hasToolResumeSnapshot,
  composerTextareaSize,
  formatMessageTimestamp,
  formatSubagentError,
  formatToolOutput,
  formatToolTerminalInput,
  formatTurnError,
  getConversationRoomMetadata,
  mergeCompactionCheckpoint,
  mergeConversationMessages,
  renderAssistantMarkdown,
  renderReasoningMarkdown,
  renderToolCodeHtml,
  sanitizeAssistantSteps,
  searchConversations,
  summarizeToolArgs,
  toConversationToolCall,
} from "./agent-conversation-ui.js";
import { estimateContextTokens, effectiveContextWindow, formatContextUsage, resolveContextBadgeTokens, shouldApplyAcpUiUpdate } from "./ai-model-ui.js";
import { inspectAttachmentContent, toDataUrl } from "./attachment-content.js";
import { CANVAS_ARTIFACT_MAX_SOURCE_BYTES, canvasArtifactId, extractCanvasCandidates, resolveCanvasFence } from "./agent-canvas-detect.js";
import { clampCanvasDrawerWidth } from "./agent-canvas-layout.js";
import { bindCanvasZoom, renderArtifact } from "./agent-canvas-render.js";
import { bindLazyCanvasReveal } from "./agent-canvas-lazy.js";
import { subscribeSubagentEvents, subscribeSubagentStream } from "./subagent-event-helper.js";
import { SubagentRunLifecycle } from "./subagent-run-lifecycle.js";
import { AgentTodoStrip } from "./agent-todo-strip.js";
import { AgentToolJobStrip } from "./agent-tool-job-strip.js";
import { CompletionSteerer } from "./completion-steerer.js";
import { subscribeToolJobEvents } from "./turn-event-helper.js";

const CANVAS_DRAWER_WIDTH_KEY = "nusashell.agent.canvas.drawer-width";

export class AgentConversationController {
  constructor({ shell, runTurn, cancelTurn, answerAsk, getActiveModel, getActiveEffort, getVisionMode, notify, log, runAcpTurn, cancelAcpTurn, answerAcpPermission, answerAcpAsk, getAcpSessionInfo, setAcpConfigOption, ensureAcpSession, refreshModelPicker, getActiveTurn, deleteTodos, getMaxInputTokens }) {
    this.shell = shell;
    this.runTurn = runTurn;
    this.cancelTurn = cancelTurn;
    this.answerAsk = answerAsk;
    this.getActiveModel = getActiveModel;
    this.getActiveEffort = getActiveEffort || (() => "auto");
    this.getMaxInputTokens = getMaxInputTokens;
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
    this.getActiveTurn = getActiveTurn;
    this.deleteTodos = deleteTodos;
    this.acpConfigOptions = [];
    this.conversation = null;
    this.conversations = [];
    this.activeId = "";
    // Invalidates stale conversation loads when the user clicks rooms quickly.
    this.openGeneration = 0;
    this.pendingDeleteId = "";
    /** Per-conversation set of conversation IDs with an in-flight turn. */
    this.pendingTurnConversations = new Set();
    /** Brief re-entry guard during submit() before the conversation ID is known. */
    this._submitInFlight = false;
    /** Per-conversation trace IDs for in-flight or recently active turns. */
    this.activeTraceIds = new Map();
    /** Per-conversation live stream state for in-flight streaming turns. */
    this.liveStreamStates = new Map();
    /** Per-conversation auto-continue abort flags. */
    this.autoContinueAbortedConvs = new Set();
    /** Per-conversation "user requested stop" flag — gates streaming paint so
     *  deltas arriving while the backend cancel settles are not painted (#44). */
    this.stopRequestedConvs = new Set();
    /** Per-conversation in-flight cancel promises — makes repeated Stop clicks
     *  idempotent (second click returns the same promise). */
    this.stopInFlight = new Map();
    /** Conversation that owns the in-flight parent submit() (paint gate). */
    this.turnOwnerConversationId = null;
    this.failedMessage = null;
    this.attachments = [];
    this.composerInputWidth = 0;
    this.composerResizeObserver = null;
    /** True while an IME composition is in progress on the composer (#46). */
    this.inputComposing = false;
    /** Cached computed text metrics (font/line-height/padding) for the composer. */
    this.composerMetrics = null;
    /** Hidden off-thread mirror used to measure wrapped height without a full-document layout. */
    this._composerMirror = null;
    this._composerResizeRaf = 0;
    this._scrollSettledRaf = 0;
    this._lastContextKey = "";
    this._lastContextText = null;
    this.canvasEnabled = true;
    this.activeCanvasArtifact = null;
    this.canvasDrawerWidth = null;
    this.canvasRenderCache = new Map();
    this.canvasReturnFocus = null;
    this.subpaneReturnFocus = null;
    // Streaming may grow scrollHeight between user scroll events. Keep the
    // follow state separately so a later delta cannot mistake that growth for
    // the user leaving the bottom.
    this.threadShouldStickToBottom = true;
    this.subpaneShouldStickToBottom = true;
    this.subagentLifecycle = new SubagentRunLifecycle(log);
  }

  // Proxy subagent lifecycle fields for backward-compatible access.
  // Reads and writes go through the lifecycle object so state transitions
  // are centralized, while rendering methods can still access fields directly.
  get activeSubagentRun() { return this.subagentLifecycle.activeRun; }
  set activeSubagentRun(v) { this.subagentLifecycle.activeRun = v; }
  get subagentStreamState() { return this.subagentLifecycle.streamState; }
  set subagentStreamState(v) { this.subagentLifecycle.streamState = v; }
  get subagentStreamDisposer() { return this.subagentLifecycle.streamDisposer; }
  set subagentStreamDisposer(v) { this.subagentLifecycle.streamDisposer = v; }
  get activeSubagentCardStream() { return this.subagentLifecycle.cardStream; }
  set activeSubagentCardStream(v) { this.subagentLifecycle.cardStream = v; }
  get subagentEventDisposer() { return this.subagentLifecycle.eventDisposer; }
  set subagentEventDisposer(v) { this.subagentLifecycle.eventDisposer = v; }
  get subagentOwnerConversationId() { return this.subagentLifecycle.ownerConversationId; }
  set subagentOwnerConversationId(v) { this.subagentLifecycle.ownerConversationId = v; }

  /**
   * Backwards-compatible boolean view of pendingTurnConversations.
   * Returns true when ANY conversation has an in-flight turn. Prefer
   * isConversationRunning(id) for per-conversation guards.
   */
  get turnPending() { return this.pendingTurnConversations.size > 0; }
  set turnPending(value) {
    if (value) {
      const id = this.turnOwnerConversationId || this.conversation?.id;
      if (id) this.pendingTurnConversations.add(id);
    } else {
      this.pendingTurnConversations.clear();
    }
  }
  /** True when the given conversation has an in-flight turn. */
  isConversationRunning(conversationId) {
    return Boolean(conversationId) && this.pendingTurnConversations.has(conversationId);
  }

  /**
   * Assign a store-fresh conversation snapshot to `this.conversation` without
   * dropping a message that is already visible (#47). While a turn is still
   * being sealed asynchronously, a later append in the same conversation may
   * return a snapshot that has not yet included the just-sealed assistant
   * reply; blindly overwriting would make it disappear from the thread.
   */
  assignConversationFromStore(conversationId, fresh) {
    if (!conversationId || !fresh) return;
    if (this.conversation?.id !== conversationId) {
      this.conversation = fresh;
      return;
    }
    const mergedMessages = mergeConversationMessages(this.conversation.messages, fresh.messages);
    if (mergedMessages !== fresh.messages) {
      this.conversation = { ...fresh, messages: mergedMessages };
    } else {
      this.conversation = fresh;
    }
  }

  /**
   * Stream-cadence cleanup (ticket #34): rAF/text/reasoning renders and the
   * streaming-canvas 350ms timer are scheduled from async turn callbacks. If a
   * turn is cancelled or the room is switched while a frame/timer is pending,
   * the stale callback would still run and touch a `streamState` (and DOM)
   * that no longer belongs to the active room. These helpers store the ids on
   * the streamState itself so callbacks can be cancelled on stop/switch.
   */

  /** Schedule a streaming paint, cancelling any previously pending paint of the same kind. */
  scheduleStreamingPaint(streamState, kind, callback) {
    const idKey = kind === "reasoning" ? "rafIdReasoning" : "rafIdText";
    if (streamState[idKey]) cancelAnimationFrame(streamState[idKey]);
    const paint = () => {
      streamState[idKey] = requestAnimationFrame(() => {
        streamState[idKey] = 0;
        if (kind === "text") streamState.lastTextPaintAt = performance.now();
        callback();
      });
    };
    // Markdown rendering reparses the complete live answer. Keep a large
    // answer from monopolizing the renderer when the provider emits tiny,
    // high-frequency deltas; the final durable render still happens at turn
    // completion. A room switch cancels this timer in cancelStreamingPaint.
    if (kind === "text" && streamState.lastTextPaintAt) {
      const wait = Math.max(0, 50 - (performance.now() - streamState.lastTextPaintAt));
      if (wait > 0) {
        streamState.textPaintTimer = window.setTimeout(() => {
          streamState.textPaintTimer = 0;
          paint();
        }, wait);
        return streamState.textPaintTimer;
      }
    }
    paint();
    return streamState[idKey];
  }

  /** Cancel a streaming paint of a given kind, if one is pending. */
  cancelStreamingPaint(streamState, kind) {
    const idKey = kind === "reasoning" ? "rafIdReasoning" : "rafIdText";
    if (kind === "text" && streamState.textPaintTimer) {
      window.clearTimeout(streamState.textPaintTimer);
      streamState.textPaintTimer = 0;
    }
    if (streamState[idKey]) {
      cancelAnimationFrame(streamState[idKey]);
      streamState[idKey] = 0;
    }
  }

  /** Cancel text+reasoning rAF and the streaming canvas timer on a streamState. */
  disposeStreamingCadence(streamState) {
    if (!streamState) return;
    this.cancelStreamingPaint(streamState, "text");
    this.cancelStreamingPaint(streamState, "reasoning");
    // A cancelled rAF/timer will never execute the callback that clears these
    // flags. Reset them here or the next delta after a room switch will be
    // treated as already scheduled forever.
    streamState.textRenderPending = false;
    streamState.reasoningRenderPending = false;
    if (streamState.canvasRenderTimer) {
      window.clearTimeout(streamState.canvasRenderTimer);
      streamState.canvasRenderTimer = 0;
    }
  }

  /** Cancel timers for the currently stored live stream state (room switch / stop). */
  disposeStreamTimersForRoom() {
    const state = this.liveStreamState;
    if (state) this.disposeStreamingCadence(state);
  }

  /**
   * Per-conversation traceId. Getter returns the traceId for the currently
   * viewed conversation; setter stores under the turn owner (or active conv).
   * Empty string clears the entry. Falls back to the "" bucket when no
   * conversation is active (test/seed compatibility).
   */
  get activeTraceId() { return this.activeTraceIds.get(this.activeId) ?? ""; }
  set activeTraceId(value) {
    const key = this.turnOwnerConversationId || this.activeId || "";
    if (value) this.activeTraceIds.set(key, value);
    else this.activeTraceIds.delete(key);
  }

  /**
   * Per-conversation live stream state. Getter returns the stream state for
   * the currently viewed conversation; setter stores under the turn owner.
   * Falls back to the "" bucket when no conversation is active.
   */
  get liveStreamState() { return this.liveStreamStates.get(this.activeId) ?? null; }
  set liveStreamState(value) {
    const key = this.turnOwnerConversationId || this.activeId || "";
    if (value) this.liveStreamStates.set(key, value);
    else this.liveStreamStates.delete(key);
  }

  /**
   * Per-conversation auto-continue abort flag. Getter checks the currently
   * viewed conversation; setter toggles the active conversation's entry.
   * Falls back to the "" bucket when no conversation is active.
   */
  get autoContinueAborted() { return this.autoContinueAbortedConvs.has(this.activeId || ""); }
  set autoContinueAborted(value) {
    const key = this.activeId || "";
    if (value) this.autoContinueAbortedConvs.add(key);
    else this.autoContinueAbortedConvs.delete(key);
  }

  /** True when a user-initiated stop has been requested for the conversation. */
  isStopRequested(conversationId) {
    return Boolean(conversationId) && this.stopRequestedConvs.has(conversationId);
  }
  requestStop(conversationId) {
    if (conversationId) this.stopRequestedConvs.add(conversationId);
  }
  clearStop(conversationId) {
    if (conversationId) {
      this.stopRequestedConvs.delete(conversationId);
      this.stopInFlight.delete(conversationId);
    }
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

  async create(options, { bypassTurnGuard = false } = {}) {
    if (this.turnPending && !bypassTurnGuard) return;
    if (this.conversation?.messages.length === 0 && !options) {
      $("#agent-input")?.focus();
      return;
    }
    this.conversation = await this.shell.agentConversations.create(options);
    this.activeId = this.conversation.id;
    this.resetComposerForConversation(this.conversation.id);
    this.renderThread();
    this.mountTodoStrip(this.conversation.id);
    this.updateWorkspaceLabel();
    this.updateRoomInfo();
    this.updateContextStatus();
    this.updateAcpStatus();
    await this.refresh();
    $("#agent-input")?.focus();
  }

  async submit({ retry = false } = {}) {
    this.autoContinueAborted = false;
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    const stopButton = $("#agent-stop-btn");
    const status = $("#agent-provider-status");
    const text = input?.value.trim();
    const currentConversationId = this.conversation?.id ?? "";
    if ((!retry && !text && this.attachments.length === 0) || this.isConversationRunning(currentConversationId) || this._submitInFlight) return;
    // A1: acquire the submit mutex BEFORE any await so a second Ctrl+Enter
    // during `create()` / `append()` cannot start a parallel turn.
    this._submitInFlight = true;
    let ownerConversationId = "";
    let ownerConversation = null;
    try {
      if (!this.conversation) await this.create(undefined, { bypassTurnGuard: true });

      // Keep the turn bound to the conversation it started in. The user may
      // switch to, or create, another conversation while this turn streams.
      ownerConversationId = this.conversation?.id || "";
      ownerConversation = this.conversation;
      if (!ownerConversationId || !ownerConversation) {
        this._submitInFlight = false;
        return;
      }

      if (this.conversation?.kind === "acp") {
        this.pendingTurnConversations.add(ownerConversationId);
        this._submitInFlight = false;
        this.turnOwnerConversationId = ownerConversationId;
        return await this.submitAcp({ text, retry, ownerConversationId, ownerConversation });
      }
    } catch (error) {
      this._submitInFlight = false;
      if (ownerConversationId) this.pendingTurnConversations.delete(ownerConversationId);
      throw error;
    }

    let pending = null;
    let selectedModel = null;
    let retryIsSafe = false;
    let streamState = null;
    let turnEndResolve = null;
    const turnEndPromise = new Promise((resolve) => { turnEndResolve = resolve; });
    let turnEnded = false;
    let sealedResult = null;
    try {
      this.turnOwnerConversationId = ownerConversationId;
      this.pendingTurnConversations.add(ownerConversationId);
      this._submitInFlight = false;
      this.activeTraceId = crypto.randomUUID();
      input.disabled = true;
      sendButton.disabled = true;
      stopButton.hidden = false;
      this.failedMessage?.remove();
      this.failedMessage = null;
      if (!retry) {
        const attachments = [...this.attachments];
        ownerConversation = await this.shell.agentConversations.append(ownerConversationId, {
          role: "user",
          content: text,
          ...(attachments.length ? { attachments } : {}),
        });
        if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
        const savedMessage = ownerConversation.messages.at(-1);
        this.appendMessage("user", text, savedMessage ?? { attachments });
        input.value = "";
        this.resizeComposerInput();
        this.attachments = [];
        this.renderAttachments();
        await this.refresh();
      }
      retryIsSafe = true;

      const lastDurable = ownerConversation.messages.at(-1);
      const isInterrupted = retry && lastDurable?.status === "interrupted";
      // Tool resume only when tools actually settled — inject-only resumeMessages
      // after a pre-tool provider fail must not block text Continue.
      const resumeFrom = isInterrupted
        && hasToolResumeSnapshot(lastDurable)
        && Array.isArray(lastDurable.resumeMessages)
        && lastDurable.resumeMessages.length
        ? lastDurable
        : null;
      // Text continue: interrupted with non-empty partial body, no tool graph.
      const continueFrom = isInterrupted && !resumeFrom && typeof lastDurable.content === "string" && lastDurable.content.trim()
        ? lastDurable
        : null;

      pending = this.createStreamingMessage();
      selectedModel = this.getActiveModel();
      // Tool resume → resumeMessages + resume: true.
      // Text continue → buildContinueContext (base + partial + steer), no resume.
      // Normal → buildAgentContext.
      const turnMessages = resumeFrom
        ? resumeFrom.resumeMessages
        : continueFrom
          ? buildContinueContext(ownerConversation)
          : buildAgentContext(ownerConversation);
      const baseTokens = estimateContextTokens(turnMessages);
      let liveTokens = baseTokens;
      // Ticket #41: live badge uses the same effective denominator as the
      // backend compaction threshold (min(global cap, model window)).
      const liveEffectiveWindow = effectiveContextWindow(
        selectedModel?.contextWindow ?? 0,
        this.getMaxInputTokens?.(),
      );
      const setContextStatus = (tokens) => {
        liveTokens = Math.max(liveTokens, tokens);
        if (selectedModel) status.textContent = formatContextUsage(liveTokens, liveEffectiveWindow);
      };
      setContextStatus(baseTokens);
      // Ticket #40: bound the model + a user stop request must freeze painting.
      const canPaint = () => this.conversation?.id === ownerConversationId && Boolean(streamState.message?.isConnected) && !this.isStopRequested(ownerConversationId);
      streamState = {
        message: pending,
        // Ticket #40: bind the model that actually drives this turn so the
        // badge (updateContextStatus) does not follow a global picker change.
        modelKey: selectedModel?.key ?? null,
        contextWindow: selectedModel?.contextWindow ?? 0,
        lastKind: null,
        reasoningEl: null,
        reasoningText: "",
        toolCards: new Map(),
        textBubble: null,
        streamedText: "",
        textRenderPending: false,
        reasoningRenderPending: false,
        rafIdText: 0,
        rafIdReasoning: 0,
        canvasRenderTimer: 0,
      };
      this.liveStreamState = streamState;
      const appendStreamChild = (node) => {
        if (!canPaint()) return;
        streamState.message.appendChild(node);
        this.scrollToBottom();
      };
      const result = await this.runTurn(turnMessages, {
        traceId: this.activeTraceId,
        workspace: ownerConversation.workspace,
        conversationId: ownerConversationId,
        // Ticket #38: thread the room-bound model + effort so the actual turn
        // uses the conversation's binding, not a stale global re-read.
        modelKey: selectedModel?.key,
        effort: this.getActiveEffort?.(),
        ...(resumeFrom ? { resume: true } : {}),
        onDelta: (delta) => {
          // Reduce every delta even while this room is backgrounded. Only
          // DOM painting is visibility-gated; dropping the delta here makes
          // room switching look like the stream stopped.
          if (streamState.lastKind !== "text") {
            streamState.streamedText = "";
            streamState.lastKind = "text";
            if (canPaint()) {
              streamState.textBubble = element("div", "agent-bubble");
              appendStreamChild(streamState.textBubble);
            }
          }
          streamState.streamedText += delta;
          // Coalesce markdown re-render to one per animation frame.
          // Without this, renderAssistantMarkdown (markdown-it + DOMPurify)
          // runs on every token — O(n²) parsing that freezes the main thread
          // and makes streaming appear as "spawn per message".
          if (canPaint() && !streamState.textRenderPending) {
            streamState.textRenderPending = true;
            this.scheduleStreamingPaint(streamState, "text", () => {
              streamState.textRenderPending = false;
              if (streamState.textBubble && canPaint()) {
                streamState.textBubble.innerHTML = renderAssistantMarkdown(streamState.streamedText);
                this.scheduleStreamingCanvasEnhancement(streamState, ownerConversationId);
                this.scrollToBottom();
              }
            });
          }
          if (canPaint()) setContextStatus(liveTokens + Math.ceil(delta.length / 4));
        },
        onReasoningDelta: (delta) => {
          if (streamState.lastKind !== "reasoning") {
            streamState.reasoningText = "";
            streamState.lastKind = "reasoning";
            if (canPaint()) {
              streamState.reasoningEl = this.createStreamingReasoningBlock();
              appendStreamChild(streamState.reasoningEl);
            }
          }
          streamState.reasoningText += delta;
          if (canPaint() && !streamState.reasoningRenderPending) {
            streamState.reasoningRenderPending = true;
            this.scheduleStreamingPaint(streamState, "reasoning", () => {
              streamState.reasoningRenderPending = false;
              const content = streamState.reasoningEl?.querySelector(".agent-reasoning-content");
              if (content && canPaint()) {
                content.innerHTML = renderReasoningMarkdown(streamState.reasoningText);
                this.scrollToBottom();
              }
            });
          }
          if (canPaint()) setContextStatus(liveTokens + Math.ceil(delta.length / 4));
        },
        onToolCallStart: (payload) => {
          streamState.lastKind = "tool";
          if (!canPaint()) return;
          const card = this.createStreamingToolCard(payload.callId, payload.name, payload.args);
          streamState.toolCards.set(payload.callId, card);
          appendStreamChild(card);
        },
        onToolCallEnd: (payload) => {
          if (!canPaint()) return;
          const card = streamState.toolCards.get(payload.callId);
          if (card) {
            const next = this.updateStreamingToolCard(card, payload);
            if (next) streamState.toolCards.set(payload.callId, next);
          }
        },
        onAskRequest: (payload) => {
          if (!canPaint()) return;
          streamState.lastKind = "tool";
          const callId = payload.callId;
          const args = {
            question: payload.question,
            options: payload.options,
            allow_free_text: payload.allowFreeText === true,
            multi_select: payload.multiSelect === true,
          };
          const card = this.createAskCard(callId, args, { sealed: false });
          const existing = streamState.toolCards.get(callId);
          if (existing?.parentNode) existing.replaceWith(card);
          else appendStreamChild(card);
          streamState.toolCards.set(callId, card);
          this.log("info", `Waiting for confirmation call=${callId}`);
          status.textContent = "Waiting for confirmation…";
          this.scrollToBottom();
        },
        onContextUpdate: (payload) => {
          if (!canPaint()) return;
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
          if (!canPaint()) return;
          const btn = $("#agent-stop-btn");
          if (btn) btn.classList.add("is-stopping");
        },
        onStreamGap: (traceId, streamSeq) => {
          // C2: a stream sequence gap means events were dropped. Rehydrate
          // from the backend projection rather than rendering with holes.
          this.log?.("warn", `Stream gap at streamSeq=${streamSeq} trace=${traceId} — projection will reconcile on room focus`);
          if (this.conversation?.id === ownerConversationId) {
            this.surfaceStreamGap(traceId, streamSeq);
            void this.restoreActiveTurnUi().then(() => this.clearStreamGapStatus());
          }
        },
      });
      retryIsSafe = false;

      try {
        // The main process seals the assistant message off the renderer
        // critical path (via sealAgentTurn) so a renderer restart mid-turn
        // does not orphan the reply. Refresh from the store; if the sealed
        // message is missing (seal failed or no conversationId), fall back to
        // renderer-side append.
        ownerConversation = await this.shell.agentConversations.get(ownerConversationId);
        if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
        const lastMessage = ownerConversation?.messages.at(-1);
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
          ownerConversation = resumeFrom
            ? await this.shell.agentConversations.replaceLastInterrupted(ownerConversationId, assistantMessage)
            : await this.shell.agentConversations.append(ownerConversationId, assistantMessage);
          if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
        } else if (result.compaction) {
          // Main already saved the checkpoint; just refresh into memory.
          ownerConversation = await this.shell.agentConversations.get(ownerConversationId);
          if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
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
            ownerConversation.checkpoint,
            result.compaction,
            ownerConversation.messages.length,
          );
          ownerConversation = await this.shell.agentConversations.saveCheckpoint(ownerConversationId, checkpoint);
          if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
        } catch (error) {
          this.log("error", `Agent checkpoint persistence failed trace=${result.traceId}: ${error.message || String(error)}`);
        }
      }
      const savedMessage = ownerConversation.messages.at(-1);
      this.sealStreamingMessage(pending, savedMessage ?? result);
      await this.refresh();
      // refresh() already calls updateContextStatus() with an estimate from
      // persisted messages. Do NOT overwrite with result.usage.inputTokens —
      // that is cumulative billing across tool rounds, not the current window
      // fill, and would inflate the badge ~N× after multi-round turns.
      this.log("info", `Agent turn completed trace=${result.traceId} rounds=${result.rounds}`);
      sealedResult = result;
    } catch (error) {
      if (error.code === "AGENT_TURN_CANCELLED" && !turnEnded) {
        // Wait for the terminal turn_end event (published after in-flight
        // tools drain) before sealing, with a 2s fallback so the UI never
        // hangs on a missing event.
        await Promise.race([turnEndPromise, new Promise((r) => setTimeout(r, 2000))]);
      }
      const partial = error.details?.partial;
      const isCancel = error.code === "AGENT_TURN_CANCELLED";
      const isMaxRounds = error.code === "AGENT_MAX_TOOL_ROUNDS";
      const sealedByMain = error.details?.sealedInterrupted === true;
      if (partial) {
        this.sealStreamingToolCardsIncomplete(streamState);
        this.sealStreamingMessage(pending, { ...partial, status: "interrupted" });
        if (pending && isCancel) pending.classList.add("agent-message-stopped");
        // content = partial body first; never overwrite with stub when there
        // is body. The stub is only a fallback when nothing was streamed.
        const streamedText = partial.text?.trim() || streamState?.streamedText || "";
        const hasTools = Array.isArray(partial.toolCalls) && partial.toolCalls.length > 0;
        const stub = isCancel
          ? `Turn stopped after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`
          : isMaxRounds
            ? `Tool-round limit reached after ${partial.rounds} round${partial.rounds === 1 ? "" : "s"}. Resume to continue the work.`
            : `Turn interrupted after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`;
        // Main process seals interrupted + resumeMessages off the renderer path
        // (via sealAgentInterrupted). Prefer store; fall back to renderer append
        // only when main did not seal (no conversationId or seal failure).
        try {
          ownerConversation = await this.shell.agentConversations.get(ownerConversationId);
          if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
          const lastDurableAfter = ownerConversation?.messages.at(-1);
          const mainSealed =
            sealedByMain
            || (lastDurableAfter?.status === "interrupted" && lastDurableAfter?.traceId === partial.traceId);
          if (!mainSealed) {
            const interruptedMessage = {
              role: "assistant",
              content: streamedText || stub,
              status: "interrupted",
              interruptReason: isCancel ? "cancel" : isMaxRounds ? "max_rounds" : "provider",
              traceId: partial.traceId,
              model: partial.model,
              rounds: partial.rounds,
              steps: sanitizeAssistantSteps(partial.steps),
              ...(hasTools
                ? { toolCalls: partial.toolCalls.map(toConversationToolCall) }
                : {}),
              ...(hasTools && Array.isArray(partial.messages) && partial.messages.length
                ? { resumeMessages: partial.messages }
                : {}),
            };
            ownerConversation = resumeFrom
              ? await this.shell.agentConversations.replaceLastInterrupted(ownerConversationId, interruptedMessage)
              : await this.shell.agentConversations.append(ownerConversationId, interruptedMessage);
            if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
          }
        } catch (persistError) {
          this.log("error", `Interrupted assistant persistence failed: ${persistError.message || String(persistError)}`);
        }
        // Status copy reflects whether Resume (tools) or Continue (text) applies.
        const durableInterrupted = ownerConversation?.messages.at(-1);
        const durableHasToolResume = hasToolResumeSnapshot(durableInterrupted);
        const resumeLabel = hasTools || durableHasToolResume
          ? "ready to resume"
          : streamedText
            ? "ready to continue"
            : "ready to retry";
        // Semantic primary-label to match the status copy (#45).
        const retryLabel = hasTools || durableHasToolResume
          ? "Resume"
          : streamedText
            ? "Continue"
            : "Retry";
        this.failedMessage = this.appendMessage(
          "assistant",
          isCancel
            ? "Turn stopped."
            : isMaxRounds
              ? "Tool-round limit reached · ready to resume"
              : `Turn failed: ${formatTurnError(error)}`,
          // Interrupted/cancel keeps a retry affordance; label follows the
          // active class (Resume for tools, Continue for text) (#45).
          { error: true, retry: true, retryLabel },
        );
        status.textContent = isCancel
          ? `Turn stopped · ${resumeLabel}`
          : isMaxRounds
            ? "Tool-round limit · ready to resume"
            : `Turn interrupted · ${resumeLabel}`;
        this.log(isCancel || isMaxRounds ? "info" : "error", isCancel
          ? `Agent turn stopped trace=${this.activeTraceId}`
          : isMaxRounds
            ? `Agent turn hit tool-round limit trace=${this.activeTraceId}`
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
        // snapshot (e.g. failure before any tool progress). Main may still
        // have sealed interrupted while IPC dropped details.partial.
        try {
          ownerConversation = await this.shell.agentConversations.get(ownerConversationId);
          if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
        } catch {
          // ignore refresh failure
        }
        const mainInterrupted = ownerConversation?.messages.at(-1);
        const hasMainInterrupt = mainInterrupted?.status === "interrupted";
        const hasStream = Boolean(
          streamState
          && (streamState.streamedText || streamState.reasoningText || streamState.toolCards.size > 0),
        );
        if (pending && (hasStream || hasMainInterrupt)) {
          this.sealStreamingToolCardsIncomplete(streamState);
          this.sealStreamingMessage(pending, {
            content: streamState?.streamedText || mainInterrupted?.content || "",
            status: hasMainInterrupt ? "interrupted" : undefined,
            ...(streamState?.reasoningText ? { reasoning: streamState.reasoningText } : {}),
            ...(mainInterrupted?.rounds !== undefined ? { rounds: mainInterrupted.rounds } : {}),
            ...(mainInterrupted?.traceId ? { traceId: mainInterrupted.traceId } : {}),
          });
        } else {
          pending?.remove();
        }
        const canToolResume = hasMainInterrupt && hasToolResumeSnapshot(mainInterrupted)
          && Array.isArray(mainInterrupted.resumeMessages) && mainInterrupted.resumeMessages.length;
        const canTextContinue = hasMainInterrupt
          && typeof mainInterrupted.content === "string"
          && mainInterrupted.content.trim();
        const canResume = Boolean(canToolResume || canTextContinue);
        // Classify provider/transport failures for copy + retry semantics (#45).
        const cls = classifyTurnError(error);
        const classification = cls.category;
        const isSuperseded = classification === "superseded";
        const isRateLimited = classification === "rate_limited";
        // Semantic primary label: interrupted-with-tools → Resume,
        // interrupted-text → Continue; otherwise default copy per classification.
        const retryLabel = canToolResume ? "Resume" : canTextContinue ? "Continue" : (cls.label || "Retry");
        const safeRetry = !isSuperseded && !cls.retryable
          ? false
          : (retryIsSafe || canResume) && !isSuperseded;
        const cooldownMs = isRateLimited ? 8000 : 0;
        // Keep the familiar failure copy; only superseded replaces it.
        const failureCopy = isSuperseded ? cls.message : `Turn failed: ${formatTurnError(error)}`;
        this.failedMessage = this.appendMessage(
          "assistant",
          failureCopy,
          {
            error: true,
            retry: safeRetry,
            retryLabel,
            retryCooldownMs: cooldownMs,
          },
        );
        status.textContent = canToolResume
          ? "Turn interrupted · ready to resume"
          : canTextContinue
            ? "Turn interrupted · ready to continue"
            : isSuperseded
              ? "Turn superseded by a newer turn"
              : isRateLimited
                ? "Rate limited · try again in a moment"
                : safeRetry
                  ? "Turn failed · ready to retry"
                  : "Local conversation error";
        this.log("error", `Agent turn failed: ${formatTurnError(error)}`);
      }
    } finally {
      // Use the closure-captured ownerConversationId (from submit's local
      // scope), not this.turnOwnerConversationId, so concurrent turns in
      // other conversations don't clear each other's globals.
      this.pendingTurnConversations.delete(ownerConversationId);
      this.activeTraceIds.delete(ownerConversationId);
      this.liveStreamStates.delete(ownerConversationId);
      this.clearStop(ownerConversationId);
      this._submitInFlight = false;
      if (this.turnOwnerConversationId === ownerConversationId) {
        this.turnOwnerConversationId = null;
      }
      if (this.conversation?.id === ownerConversationId) {
        input.disabled = false;
        this.updateSendAvailability();
        stopButton.hidden = true;
        stopButton.classList.remove("is-stopping");
        input.focus();
      }
    }
    // Outer multi-turn auto-continue: after a successful sealed turn, if the
    // backend says there are still open todos and the chain budget has not
    // been exhausted, start the next turn without a user message. The chain
    // aborts on Stop, user input, or conversation switch.
    if (sealedResult?.autoContinue?.shouldContinue && ownerConversationId) {
      await this.runAutoContinueChain(
        ownerConversationId,
        sealedResult.autoContinue.continuesUsed + 1,
        sealedResult.autoContinue.maxAutoContinues,
      );
    }
  }

  /**
   * Run the auto-continue chain: successive turns with no user message, each
   * carrying an incrementing autoContinueIndex. The backend injects the
   * continue steering prompt and attaches a fresh autoContinue decision to
   * each result. The chain stops when shouldContinue is false, the user
   * interacts (Stop / new message / conversation switch), or a turn fails.
   *
   * @param {string} conversationId
   * @param {number} startIndex - the autoContinueIndex for the first chained turn
   * @param {number} maxAutoContinues - from the previous result's decision (0 = unlimited)
   */
  async runAutoContinueChain(conversationId, startIndex, maxAutoContinues = 10) {
    let index = startIndex;
    // Ticket #40/room model: snapshot the model once at chain start. Reaching
    // into the global picker per iteration lets a mid-chain model switch in
    // another room silently re-target the remaining turns.
    const chainModel = this.getActiveModel?.() || null;
    const chainEffectiveWindow = effectiveContextWindow(
      chainModel?.contextWindow ?? 0,
      this.getMaxInputTokens?.(),
    );
    while (true) {
      // Abort guards — checked before acquiring the mutex.
      if (this.conversation?.id !== conversationId) return;
      if (this.isConversationRunning(conversationId)) return; // user started a new turn
      if (this.autoContinueAborted) { this.autoContinueAborted = false; return; }

      const input = $("#agent-input");
      const sendButton = $("#agent-send-btn");
      const stopButton = $("#agent-stop-btn");
      const status = $("#agent-provider-status");
      let pending = null;
      let streamState = null;
      let turnEnded = false;
      let turnEndResolve = null;
      const turnEndPromise = new Promise((resolve) => { turnEndResolve = resolve; });
      let sealedResult = null;

      this.pendingTurnConversations.add(conversationId);
      this.turnOwnerConversationId = conversationId;
      try {
        let conv = await this.shell.agentConversations.get(conversationId);
        if (this.conversation?.id !== conversationId) return;
        this.conversation = conv;
        if (!conv) return;

        this.activeTraceId = crypto.randomUUID();
        input.disabled = true;
        sendButton.disabled = true;
        stopButton.hidden = false;
        const chainLabel = maxAutoContinues === 0
          ? `Continuing tasks… (${index})`
          : `Continuing tasks… (${index}/${maxAutoContinues})`;
        status.textContent = chainLabel;

        const selectedModel = chainModel;
        const turnMessages = buildAgentContext(conv);
        const baseTokens = estimateContextTokens(turnMessages);
        let liveTokens = baseTokens;
        const setContextStatus = (tokens) => {
          liveTokens = Math.max(liveTokens, tokens);
          if (selectedModel) status.textContent = `${formatContextUsage(liveTokens, chainEffectiveWindow)} · ${chainLabel}`;
        };
        setContextStatus(baseTokens);
        const canPaint = () => this.conversation?.id === conversationId && Boolean(streamState.message?.isConnected) && !this.isStopRequested(conversationId);
        pending = this.createStreamingMessage();
        streamState = {
          message: pending,
          // Ticket #40: snapshot the model at chain start; a global picker
          // change mid-chain must not silently re-target later iterations.
          modelKey: selectedModel?.key ?? null,
          contextWindow: selectedModel?.contextWindow ?? 0,
          lastKind: null,
          reasoningEl: null,
          reasoningText: "",
          toolCards: new Map(),
          textBubble: null,
          streamedText: "",
          textRenderPending: false,
          reasoningRenderPending: false,
          rafIdText: 0,
          rafIdReasoning: 0,
          canvasRenderTimer: 0,
        };
        this.liveStreamState = streamState;
        const appendStreamChild = (node) => {
          if (!canPaint()) return;
          streamState.message.appendChild(node);
          this.scrollToBottom();
        };

        const result = await this.runTurn(turnMessages, {
          traceId: this.activeTraceId,
          workspace: conv.workspace,
          conversationId,
          autoContinueIndex: index,
          modelKey: chainModel?.key,
          effort: this.getActiveEffort?.(),
          onDelta: (delta) => {
            // Keep reducing the background turn; only its DOM surface may be
            // detached while another room is active.
            if (streamState.lastKind !== "text") {
              streamState.streamedText = "";
              streamState.lastKind = "text";
              if (canPaint()) {
                streamState.textBubble = element("div", "agent-bubble");
                appendStreamChild(streamState.textBubble);
              }
            }
            streamState.streamedText += delta;
            if (canPaint() && !streamState.textRenderPending) {
              streamState.textRenderPending = true;
              this.scheduleStreamingPaint(streamState, "text", () => {
                streamState.textRenderPending = false;
                if (streamState.textBubble && canPaint()) {
                  streamState.textBubble.innerHTML = renderAssistantMarkdown(streamState.streamedText);
                  this.scheduleStreamingCanvasEnhancement(streamState, conversationId);
                  this.scrollToBottom();
                }
              });
            }
            if (canPaint()) setContextStatus(liveTokens + Math.ceil(delta.length / 4));
          },
          onReasoningDelta: (delta) => {
            if (streamState.lastKind !== "reasoning") {
              streamState.reasoningText = "";
              streamState.lastKind = "reasoning";
              if (canPaint()) {
                streamState.reasoningEl = this.createStreamingReasoningBlock();
                appendStreamChild(streamState.reasoningEl);
              }
            }
            streamState.reasoningText += delta;
            if (canPaint() && !streamState.reasoningRenderPending) {
              streamState.reasoningRenderPending = true;
              this.scheduleStreamingPaint(streamState, "reasoning", () => {
                streamState.reasoningRenderPending = false;
                const content = streamState.reasoningEl?.querySelector(".agent-reasoning-content");
                if (content && canPaint()) {
                  content.innerHTML = renderReasoningMarkdown(streamState.reasoningText);
                  this.scrollToBottom();
                }
              });
            }
            if (canPaint()) setContextStatus(liveTokens + Math.ceil(delta.length / 4));
          },
          onToolCallStart: (payload) => {
            streamState.lastKind = "tool";
            if (!canPaint()) return;
            const card = this.createStreamingToolCard(payload.callId, payload.name, payload.args);
            streamState.toolCards.set(payload.callId, card);
            appendStreamChild(card);
          },
          onToolCallEnd: (payload) => {
            if (!canPaint()) return;
            const card = streamState.toolCards.get(payload.callId);
            if (card) {
              const next = this.updateStreamingToolCard(card, payload);
              if (next) streamState.toolCards.set(payload.callId, next);
            }
          },
          onAskRequest: (payload) => {
            if (!canPaint()) return;
            streamState.lastKind = "tool";
            const callId = payload.callId;
            const args = {
              question: payload.question,
              options: payload.options,
              allow_free_text: payload.allowFreeText === true,
              multi_select: payload.multiSelect === true,
            };
            const card = this.createAskCard(callId, args, { sealed: false });
            const existing = streamState.toolCards.get(callId);
            if (existing?.parentNode) existing.replaceWith(card);
            else appendStreamChild(card);
            streamState.toolCards.set(callId, card);
            this.log("info", `Waiting for confirmation call=${callId}`);
            status.textContent = "Waiting for confirmation…";
            this.scrollToBottom();
          },
          onContextUpdate: (payload) => {
            if (!canPaint()) return;
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
            if (!canPaint()) return;
            const btn = $("#agent-stop-btn");
            if (btn) btn.classList.add("is-stopping");
          },
          onStreamGap: (traceId, streamSeq) => {
            this.log?.("warn", `Stream gap at streamSeq=${streamSeq} trace=${traceId} — projection will reconcile on room focus`);
            if (this.conversation?.id === conversationId) {
              this.surfaceStreamGap(traceId, streamSeq);
              void this.restoreActiveTurnUi().then(() => this.clearStreamGapStatus());
            }
          },
        });

        // Seal the result — main process may have already persisted via
        // sealAgentTurn, so refresh from the store first.
        conv = await this.shell.agentConversations.get(conversationId);
        if (this.conversation?.id === conversationId) this.conversation = conv;
        const lastMessage = conv?.messages.at(-1);
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
          conv = await this.shell.agentConversations.append(conversationId, assistantMessage);
          if (this.conversation?.id === conversationId) this.conversation = conv;
        }
        const savedMessage = conv.messages.at(-1);
        this.sealStreamingMessage(pending, savedMessage ?? result);
        await this.refresh();
        this.log("info", `Auto-continue ${index} completed trace=${result.traceId} rounds=${result.rounds}`);
        sealedResult = result;

        if (!result.autoContinue?.shouldContinue) {
          this.log("info", `Auto-continue chain ended: ${result.autoContinue?.reason ?? "unknown"} after ${index} continuation(s)`);
          status.textContent = "Idle";
          break;
        }
        index++;
      } catch (error) {
        if (error.code === "AGENT_TURN_CANCELLED" && !turnEnded) {
          await Promise.race([turnEndPromise, new Promise((r) => setTimeout(r, 2000))]);
        }
        const partial = error.details?.partial;
        const isCancel = error.code === "AGENT_TURN_CANCELLED";
        const sealedByMain = error.details?.sealedInterrupted === true;
        if (partial) {
          this.sealStreamingToolCardsIncomplete(streamState);
          this.sealStreamingMessage(pending, { ...partial, status: "interrupted" });
          if (pending && isCancel) pending.classList.add("agent-message-stopped");
          const streamedText = partial.text?.trim() || streamState?.streamedText || "";
          const hasTools = Array.isArray(partial.toolCalls) && partial.toolCalls.length > 0;
          const stub = isCancel
            ? `Auto-continue stopped after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`
            : `Auto-continue interrupted after ${partial.rounds} tool round${partial.rounds === 1 ? "" : "s"}.`;
          try {
            let conv = await this.shell.agentConversations.get(conversationId);
            const last = conv?.messages.at(-1);
            const mainSealed = sealedByMain || (last?.status === "interrupted" && last?.traceId === partial.traceId);
            if (!mainSealed) {
              const interruptedMessage = {
                role: "assistant",
                content: streamedText || stub,
                status: "interrupted",
                interruptReason: isCancel ? "cancel" : "provider",
                traceId: partial.traceId,
                model: partial.model,
                rounds: partial.rounds,
                steps: sanitizeAssistantSteps(partial.steps),
                ...(hasTools
                  ? { toolCalls: partial.toolCalls.map(toConversationToolCall) }
                  : {}),
                ...(hasTools && Array.isArray(partial.messages) && partial.messages.length
                  ? { resumeMessages: partial.messages }
                  : {}),
              };
              conv = await this.shell.agentConversations.append(conversationId, interruptedMessage);
            }
            if (this.conversation?.id === conversationId) this.assignConversationFromStore(conversationId, conv);
          } catch (persistError) {
            this.log("error", `Auto-continue interrupted persistence failed: ${persistError.message || String(persistError)}`);
          }
        } else if (isCancel) {
          this.sealStreamingToolCardsIncomplete(streamState);
          if (pending && streamState?.streamedText) {
            this.sealStreamingMessage(pending, { content: streamState.streamedText });
            pending.classList.add("agent-message-stopped");
          } else {
            pending?.remove();
          }
        } else {
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
            `Auto-continue failed: ${formatTurnError(error)}`,
            { error: true, retry: true },
          );
        }
        status.textContent = isCancel ? "Auto-continue stopped" : "Auto-continue failed · ready to retry";
        this.log(isCancel ? "info" : "error", isCancel
          ? `Auto-continue stopped at index=${index} trace=${this.activeTraceId}`
          : `Auto-continue failed at index=${index}: ${formatTurnError(error)}`);
        break;
      } finally {
        this.pendingTurnConversations.delete(conversationId);
        this.activeTraceIds.delete(conversationId);
        this.liveStreamStates.delete(conversationId);
        this.clearStop(conversationId);
        if (this.turnOwnerConversationId === conversationId) {
          this.turnOwnerConversationId = null;
        }
        if (this.conversation?.id === conversationId) {
          input.disabled = false;
          this.updateSendAvailability();
          stopButton.hidden = true;
          stopButton.classList.remove("is-stopping");
          input.focus();
        }
      }
    }
  }

  async submitAcp({ text, retry, ownerConversationId = this.conversation?.id, ownerConversation = this.conversation }) {
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    const stopButton = $("#agent-stop-btn");
    const status = $("#agent-provider-status");
    // A1: submit() adds the conversation to pendingTurnConversations before
    // calling us; only reject when called directly (should not happen, but
    // guard for safety).
    if (!this.isConversationRunning(ownerConversationId) || !ownerConversationId || !ownerConversation?.acp) return;

    let pending = null;
    let retryIsSafe = false;
    try {
      this.activeTraceId = crypto.randomUUID();
      input.disabled = true;
      sendButton.disabled = true;
      stopButton.hidden = false;
      this.failedMessage?.remove();
      this.failedMessage = null;
      if (!retry) {
        ownerConversation = await this.shell.agentConversations.append(ownerConversationId, {
          role: "user",
          content: text,
        });
        if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
        this.appendMessage("user", text);
        input.value = "";
        this.resizeComposerInput();
        await this.refresh();
      }
      retryIsSafe = true;

      pending = this.createStreamingMessage();
      const streamState = { message: pending, textBubble: null, streamedText: "", reasoningEl: null, reasoningText: "", lastKind: null, toolCards: new Map(), toolCalls: [], steps: [], textRenderPending: false, reasoningRenderPending: false, rafIdText: 0, rafIdReasoning: 0, canvasRenderTimer: 0 };
      const canPaint = () => this.conversation?.id === ownerConversationId && !this.isStopRequested(ownerConversationId);
      const appendStreamChild = (node) => {
        if (!canPaint()) return;
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
        conversationId: ownerConversationId,
        workspace: ownerConversation.workspace,
        providerId: ownerConversation.acp.providerId,
        onDelta: (delta) => {
          if (!canPaint()) return;
          if (streamState.lastKind !== "text") {
            sealStep();
            streamState.textBubble = element("div", "agent-bubble");
            streamState.streamedText = "";
            streamState.message?.appendChild(streamState.textBubble);
            this.scrollToBottom();
          }
          streamState.lastKind = "text";
          streamState.streamedText += delta;
          if (!streamState.textRenderPending) {
            streamState.textRenderPending = true;
            this.scheduleStreamingPaint(streamState, "text", () => {
              streamState.textRenderPending = false;
              if (streamState.textBubble && canPaint()) {
                streamState.textBubble.innerHTML = renderAssistantMarkdown(streamState.streamedText);
                this.scrollToBottom();
              }
            });
          }
        },
        onReasoningDelta: (delta) => {
          if (!canPaint()) return;
          if (streamState.lastKind !== "reasoning") {
            sealStep();
            streamState.reasoningEl = this.createStreamingReasoningBlock();
            streamState.reasoningText = "";
            streamState.message?.appendChild(streamState.reasoningEl);
            this.scrollToBottom();
          }
          streamState.lastKind = "reasoning";
          streamState.reasoningText += delta;
          if (!streamState.reasoningRenderPending) {
            streamState.reasoningRenderPending = true;
            this.scheduleStreamingPaint(streamState, "reasoning", () => {
              streamState.reasoningRenderPending = false;
              const content = streamState.reasoningEl?.querySelector(".agent-reasoning-content");
              if (content && canPaint()) {
                content.innerHTML = renderReasoningMarkdown(streamState.reasoningText);
                this.scrollToBottom();
              }
            });
          }
        },
        onToolCallStart: (payload) => {
          if (!canPaint()) return;
          if (streamState.lastKind !== "tool") sealStep();
          streamState.lastKind = "tool";
          const card = this.createStreamingToolCard(payload.callId, payload.name, payload.args);
          streamState.toolCards.set(payload.callId, card);
          streamState.toolCalls.push({ id: payload.callId, name: payload.name, ok: true, args: payload.args });
          streamState.steps.push({ type: "tool_calls", calls: [{ id: payload.callId, name: payload.name, ok: true, args: payload.args }] });
          appendStreamChild(card);
        },
        onToolCallEnd: (payload) => {
          if (!canPaint()) return;
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
          if (!canPaint()) return;
          this.sealStreamingToolCardsIncomplete(streamState);
        },
        onPermissionRequest: (payload) => {
          if (!canPaint()) return;
          const card = this.createAcpPermissionCard(payload);
          if (card) appendStreamChild(card);
        },
        onAskRequest: (payload) => {
          if (!canPaint()) return;
          const card = this.createAcpAskCard(payload);
          if (card) appendStreamChild(card);
        },
        onStreamGap: (traceId, streamSeq) => {
          // C2: ACP has no ActiveTurn projection yet (C6 deferred); log + surface.
          this.log?.("warn", `ACP stream gap at streamSeq=${streamSeq} trace=${traceId} — content may be incomplete`);
          this.surfaceStreamGap(traceId, streamSeq);
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

      ownerConversation = await this.shell.agentConversations.append(ownerConversationId, {
        role: "assistant",
        content: fullText || "Done.",
        traceId: this.activeTraceId,
        model: ownerConversation.acp.providerId,
        reasoning: fullReasoning || undefined,
        ...(streamState.toolCalls.length ? { toolCalls: streamState.toolCalls } : {}),
        ...(streamState.steps.length ? { steps: streamState.steps } : {}),
      });
      if (this.conversation?.id === ownerConversationId) this.assignConversationFromStore(ownerConversationId, ownerConversation);
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
      // Use the parameter ownerConversationId, not this.turnOwnerConversationId,
      // so concurrent turns in other conversations don't clear each other's globals.
      this.pendingTurnConversations.delete(ownerConversationId);
      this.activeTraceIds.delete(ownerConversationId);
      this.clearStop(ownerConversationId);
      if (this.turnOwnerConversationId === ownerConversationId) {
        this.turnOwnerConversationId = null;
      }
      if (this.conversation?.id === ownerConversationId) {
        input.disabled = false;
        this.updateSendAvailability();
        stopButton.hidden = true;
        stopButton.classList.remove("is-stopping");
        input.focus();
      }
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
    input.addEventListener("input", () => {
      this.scheduleComposerResize();
      this.updateSendAvailability();
    });
    // Track IME composition so a candidate-confirm Enter (and the Send button)
    // does not submit a half-composed prompt (#46). During composition the
    // keydown handler must skip intercepting Enter.
    input.addEventListener("compositionstart", () => { this.inputComposing = true; this.updateSendAvailability(); });
    input.addEventListener("compositionend", () => { this.inputComposing = false; this.updateSendAvailability(); });
    input.addEventListener("keydown", (event) => {
      // IME composition: Enter confirms a candidate — never intercept.
      if (event.isComposing || event.keyCode === 229) return;
      // Newline semantics: plain Enter and Shift+Enter keep the default
      // textarea newline; only Ctrl/Cmd+Enter submits (#46).
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
    $("#agent-room-info-trigger")?.addEventListener("click", () => this.toggleRoomInfo());
    $("#agent-room-info-close")?.addEventListener("click", () => this.toggleRoomInfo(false));
    document.addEventListener("pointerdown", (event) => {
      const info = $("#agent-room-info");
      if (!info || info.hidden || !info.contains(event.target)) this.toggleRoomInfo(false, { focusTrigger: false });
    });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") this.toggleRoomInfo(false);
    });
    $("#agent-thread").addEventListener("scroll", (event) => {
      const thread = event.currentTarget;
      this.threadShouldStickToBottom = this.isThreadAtBottom(thread);
    }, { passive: true });
    $("#agent-stop-btn").addEventListener("click", () => void this.stop());
    $("#agent-attach-btn").addEventListener("click", () => $("#agent-file-input").click());
    $("#agent-file-input").addEventListener("change", (event) => void this.addAttachments(event.target.files));
    $("#agent-new-conversation").addEventListener("click", () => this.runUiAction(
      this.create(undefined, { bypassTurnGuard: true }),
      "Could not create conversation",
    ));
    $("#agent-mobile-conversations-btn")?.addEventListener("click", () => this.toggleMobileConversations());
    $("#agent-mobile-conversations-overlay")?.addEventListener("click", () => this.toggleMobileConversations(false));
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
          ? "has image input disabled in runtime settings."
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

  scheduleComposerResize() {
    // Debounce the per-keystroke resize so typing does not force a layout
    // (getComputedStyle + scrollHeight) on every input event (#33). One rAF
    // per burst coalesces the work; resizes triggered by submit/observer call
    // resizeComposerInput() directly and stay synchronous.
    if (this._composerResizeRaf) return;
    this._composerResizeRaf = requestAnimationFrame(() => {
      this._composerResizeRaf = 0;
      this.resizeComposerInput();
    });
  }

  /**
   * Reflect whether the composer can send on #agent-send-btn (#46). The button
   * is disabled when the input is empty (nothing to send), during an active IME
   * composition, or while a turn owns the room (kept separate from this flag).
   */
  updateSendAvailability() {
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    if (!input || !sendButton) return;
    const hasContent = Boolean(input.value?.trim()) || this.attachments.length > 0;
    sendButton.disabled = !hasContent || this.inputComposing;
  }

  resizeComposerInput() {
    const input = $("#agent-input");
    if (!input) return;

    // Cache computed text metrics once (line-height/padding are static CSS),
    // so the typing hot path never runs a synchronous style recalc. Re-read
    // only when width/font changed (handled via the mirror re-init below).
    if (!this.composerMetrics) {
      const style = window.getComputedStyle(input);
      this.composerMetrics = {
        lineHeight: Number.parseFloat(style.lineHeight) || 19,
        paddingTop: Number.parseFloat(style.paddingTop) || 0,
        paddingBottom: Number.parseFloat(style.paddingBottom) || 0,
      };
    }
    const { lineHeight, paddingTop, paddingBottom } = this.composerMetrics;

    // Measure wrapped height on an isolated hidden mirror (same font + width),
    // NOT on the live textarea. The live textarea lives inside the flex column
    // that also holds the thread (1000+ tool cards), so reading its scrollHeight
    // forces a full-document synchronous layout per burst. The mirror is an
    // off-thread absolute element, so it only lays out a tiny subtree (#43).
    let mirror = this._composerMirror;
    if (!mirror) {
      mirror = document.createElement("textarea");
      mirror.setAttribute("aria-hidden", "true");
      mirror.style.cssText =
        "position:absolute;left:-9999px;top:0;visibility:hidden;height:auto;overflow:hidden;" +
        "white-space:pre-wrap;word-wrap:break-word;resize:none;box-sizing:border-box;";
      mirror.style.fontFamily = "inherit";
      mirror.style.fontSize = "13px";
      mirror.style.lineHeight = `${lineHeight}px`;
      mirror.style.paddingTop = `${paddingTop}px`;
      mirror.style.paddingBottom = `${paddingBottom}px`;
      document.body.appendChild(mirror);
      this._composerMirror = mirror;
    }
    // Keep the mirror width in sync with the composer (ResizeObserver updates
    // composerInputWidth); width changes affect wrapping so re-measure always.
    const width = this.composerInputWidth || input.clientWidth || 500;
    if (mirror.style.width !== `${width}px`) mirror.style.width = `${width}px`;
    mirror.value = input.value + "\n"; // trailing newline so the last line wraps

    const currentScrollHeight = mirror.scrollHeight;
    const size = composerTextareaSize({
      scrollHeight: currentScrollHeight,
      lineHeight,
      paddingTop,
      paddingBottom,
    });
    const heightPx = `${size.height}px`;
    // Avoid DOM churn: only write when the resulting value actually changed.
    if (input.style.height !== heightPx) input.style.height = heightPx;
    if (input.style.overflowY !== size.overflowY) input.style.overflowY = size.overflowY;
  }

  async stop() {
    // The conversation that owns the currently-streaming turn. Stop gates
    // painting for this conversation, so deltas still arriving while the
    // backend cancel settles are NOT painted (#44).
    const stopConversationId = this.turnOwnerConversationId || this.activeId || this.conversation?.id || "";
    this.requestStop(stopConversationId);

    this.disposeStreamingCadence(this.liveStreamState);
    this.autoContinueAborted = true;
    const button = $("#agent-stop-btn");
    const statusEl = $("#agent-provider-status");
    // Immediate, synchronous feedback — do not wait for the async cancel.
    if (statusEl) statusEl.textContent = "Stopping…";

    // Idempotent Stop: if a cancel is already in flight for this conversation,
    // a second click returns the same promise and issues no duplicate request.
    const inFlight = this.stopInFlight.get(stopConversationId);
    if (inFlight) return inFlight;

    const activeSubagent = this.conversation?.subagentRuns?.find((r) => r.status === "running");
    let cancelPromise = null;
    if (this.conversation?.kind === "acp" && this.activeTraceId && this.cancelAcpTurn) {
      cancelPromise = this.cancelAcpTurn(this.activeTraceId, this.conversation.id);
    } else if (activeSubagent && this.cancelAcpTurn) {
      // Subagent runs use runId as the ACP traceId and `subagent:<runId>` as
      // the backend conversation id; cancel the subagent, not the parent turn.
      cancelPromise = this.cancelAcpTurn(activeSubagent.runId, `subagent:${activeSubagent.runId}`);
    } else if (this.turnPending && this.activeTraceId && this.cancelTurn) {
      cancelPromise = this.cancelTurn(this.activeTraceId);
    }
    if (!cancelPromise) {
      // Nothing cancellable — don't leave the stop flag/status stuck.
      this.clearStop(stopConversationId);
      if (statusEl) statusEl.textContent = "Idle";
      return;
    }
    if (button) {
      button.disabled = true;
      button.classList.add("is-stopping");
    }
    const runCancel = (async () => {
      try {
        await cancelPromise;
      } catch (error) {
        this.notify(`Could not stop the turn: ${error.message || error}`, "error");
      } finally {
        // Cancel request settled. Reset the visual feedback (button + status).
        // The paint gate (isStopRequested) stays armed until the owning turn's
        // finally clears it, so late in-flight deltas are still not painted
        // while the backend winds the stream down (#44).
        if (statusEl) statusEl.textContent = "Idle";
        if (button) {
          button.disabled = false;
          button.classList.remove("is-stopping");
        }
      }
    })();
    this.stopInFlight.set(stopConversationId, runCancel);
    return runCancel;
  }

  async refresh() {
    this.conversations = [...await this.shell.agentConversations.list()];
    this.renderList();
    this.updateRoomInfo();
  }

  async open(conversationId) {
    const generation = ++this.openGeneration;
    if (conversationId === this.activeId && this.conversation) return;
    const conversation = await this.shell.agentConversations.get(conversationId);
    if (generation !== this.openGeneration) return;
    if (!conversation) {
      await this.refresh();
      return;
    }
    this.todoStrip?.dispose();
    this.conversation = conversation;
    this.activeId = conversation.id;
    this.resetComposerForConversation(conversation.id);
    this.renderThread();
    this.renderList();
    this.updateContextStatus();
    this.updateWorkspaceLabel();
    this.updateRoomInfo();
    this.refreshModelPicker?.();
    this.updateAcpStatus();
    this.mountTodoStrip(conversation.id);
    void this.restoreRunningTurnState();
  }

  mountTodoStrip(conversationId) {
    this.todoStrip?.dispose();
    if (conversationId && this.deleteTodos) {
      this.todoStrip = new AgentTodoStrip({
        conversationId,
        onDelete: (id) => this.deleteTodos(conversationId, [id]),
      });
      this.todoStrip.mount();
    } else {
      this.todoStrip = null;
      const strip = document.getElementById("agent-todo-strip");
      if (strip) strip.hidden = true;
    }
    this.toolJobStrip?.dispose();
    this.completionSteerer?.dispose();
    if (conversationId) {
      this.toolJobStrip = new AgentToolJobStrip({ conversationId });
      this.toolJobStrip.mount();
      this.completionSteerer = new CompletionSteerer({
        conversationId,
        isIdle: () => !this.isConversationRunning(conversationId),
        startTurn: (message) => this.steerTurn(message),
        log: (msg) => this.log?.(msg),
      });
      this.toolJobEventDisposer?.();
      this.toolJobEventDisposer = subscribeToolJobEvents({
        conversationId,
        onJobEnded: (p) => this.completionSteerer?.onJobEnded(p),
      });
    } else {
      this.toolJobStrip = null;
      this.completionSteerer = null;
      this.toolJobEventDisposer?.();
      this.toolJobEventDisposer = null;
      const strip = document.getElementById("agent-tool-job-strip");
      if (strip) strip.hidden = true;
    }
  }

  /**
   * Auto-start a follow-up turn with a synthetic system message (completion steering).
   * Called by CompletionSteerer when a background job ends and the conversation is idle.
   */
  async steerTurn(message) {
    if (this.isConversationRunning(this.conversation?.id)) return;
    if (!this.conversation) return;
    const input = $("#agent-input");
    if (input) {
      input.value = message;
      input.dispatchEvent(new Event("input", { bubbles: true }));
    }
    await this.submit();
  }

  /**
   * Restore turnPending/activeTraceId when returning to a conversation whose
   * turn is still running in the backend (e.g. after switching away and back
   * per B2). For ACP conversations, query the live session state; for any
   * conversation with a running subagent run, surface the stop button so B3's
   * stop() can cancel it.
   */
  async restoreRunningTurnState() {
    if (!this.conversation || this.activeId !== this.conversation.id) return;
    const stopButton = $("#agent-stop-btn");
    const hasRunningSubagent = Boolean(this.conversation.subagentRuns?.some((r) => r.status === "running"));
    if (this.conversation.kind === "acp" && this.getAcpSessionInfo && !this.isConversationRunning(this.conversation.id)) {
      try {
        const info = await this.getAcpSessionInfo(this.conversation.id);
        if (this.activeId !== this.conversation.id) return;
        if (info?.state === "running" && info.traceId) {
          this.pendingTurnConversations.add(this.conversation.id);
          this.activeTraceId = info.traceId;
          if (stopButton) stopButton.hidden = false;
          this.updateAcpStatus();
          return;
        }
      } catch (error) {
        this.log("warn", `Failed to restore ACP running state: ${error.message || error}`);
      }
    }
    if (hasRunningSubagent && stopButton) {
      stopButton.hidden = false;
    }
  }

  /**
   * A turn may keep running after its conversation is no longer visible.
   * Composer controls are scoped to the visible conversation, not to the
   * renderer-wide turn mutex.
   */
  resetComposerForConversation(conversationId) {
    const isOwner = this.isConversationRunning(conversationId) && this.turnOwnerConversationId === conversationId;
    const input = $("#agent-input");
    const sendButton = $("#agent-send-btn");
    const stopButton = $("#agent-stop-btn");
    if (isOwner) {
      if (input) input.disabled = true;
      if (sendButton) sendButton.disabled = true;
      if (stopButton) stopButton.hidden = false;
      return;
    }
    if (input) input.disabled = false;
    // Reflect empty/IME state rather than force-enable (#46).
    this.updateSendAvailability();
    if (stopButton) {
      stopButton.hidden = true;
      stopButton.disabled = false;
      stopButton.classList.remove("is-stopping");
    }
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

  updateRoomInfo() {
    const info = $("#agent-room-info");
    const title = $("#agent-room-info-title");
    const toolCount = $("#agent-room-tool-count");
    const compactionCount = $("#agent-room-compaction-count");
    const id = $("#agent-room-id");
    const copy = $("#agent-room-id-copy");
    if (!info || !title || !toolCount || !compactionCount || !id || !copy) return;
    const metadata = getConversationRoomMetadata(this.conversation);
    const hasRoom = Boolean(metadata.conversationId);
    info.hidden = !hasRoom;
    title.textContent = this.conversation?.title || "Conversation details";
    toolCount.textContent = String(metadata.toolCallCount);
    compactionCount.textContent = String(metadata.compactionCount);
    id.textContent = hasRoom ? metadata.conversationId : "—";
    copy.disabled = !hasRoom;
    copy.title = hasRoom ? "Copy conversation ID" : "No conversation selected";
    copy.onclick = hasRoom ? () => void this.copyMessage(metadata.conversationId, copy) : null;
    if (!hasRoom) this.toggleRoomInfo(false, { focusTrigger: false });
  }

  toggleRoomInfo(force, { focusTrigger = true } = {}) {
    const info = $("#agent-room-info");
    const trigger = $("#agent-room-info-trigger");
    const popover = $("#agent-room-info-popover");
    if (!info || !trigger || !popover) return;
    const open = typeof force === "boolean" ? force : popover.hidden;
    popover.hidden = !open;
    trigger.setAttribute("aria-expanded", String(open));
    if (!open && focusTrigger) trigger.focus({ preventScroll: true });
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
      const isRunning = this.isConversationRunning(this.conversation.id);
      chip.textContent = isRunning ? "● RUNNING" : "● IDLE";
      chip.className = `acp-status-chip ${isRunning ? "is-running" : "is-idle"}`;
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
    if (triggerLabel) {
      triggerLabel.textContent = `${modelName} · ACP`;
      const trigger = $("#agent-model-trigger");
      if (trigger) trigger.title = triggerLabel.textContent;
    }
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
    if (!status) return;
    if (status.classList.contains("is-stream-gap")) return;

    // Ticket #40: prefer the model bound to the in-flight turn (snapshot) so
    // the badge denominator does not jump when the global picker changes
    // mid-turn or right after Stop. Idle falls back to the active model.
    const stream = this.liveStreamState;
    const streamModelKey = stream?.modelKey || null;
    const streamWindow = stream?.contextWindow;
    const selectedModel = streamModelKey
      ? { key: streamModelKey, contextWindow: streamWindow }
      : this.getActiveModel?.() || null;

    if (!selectedModel) {
      status.textContent = "Choose a model";
      return;
    }
    // Ticket #41: the badge must agree with the backend compaction threshold,
    // which runs `min(settings.maxInputTokens, modelWindow)`. Expose the same
    // effective denominator for both idle and live so it does not "1M vs 200k".
    const globalCap = this.getMaxInputTokens?.();
    const effectiveWindow = effectiveContextWindow(selectedModel.contextWindow, globalCap);
    const modelKey = selectedModel.key ?? "";

    // Cache the estimate keyed by (conversation, message count, checkpoint,
    // model, phase, effective window). refresh() and open() call
    // updateContextStatus() repeatedly after every turn with an unchanged
    // thread; re-running buildAgentContext + token estimate each time is
    // O(thread) wasted work (#33). Including the model key + phase means a
    // model switch or stream start/end invalidates stale badge text even when
    // the message count is unchanged (#40).
    const conv = this.conversation;
    const phase = streamModelKey ? "live" : "idle";
    const cacheKey = conv
      ? `${conv.id}:${conv.messages.length}:${conv.checkpoint?.compactedMessageCount ?? 0}:${modelKey}:${phase}:${effectiveWindow}`
      : "";
    if (cacheKey && this._lastContextKey === cacheKey && this._lastContextText !== null) {
      status.textContent = this._lastContextText;
      return;
    }
    // Dual-space: badge reflects the model-facing context (buildAgentContext
    // with checkpoint applied), not the raw full transcript. Fall back to the
    // full thread estimate only when the provider path is empty (e.g. a fresh
    // conversation with no checkpoint and no messages yet).
    const providerTokens = estimateContextTokens(buildAgentContext(conv));
    const threadTokens = estimateContextTokens(conv?.messages || []);
    const tokens = providerTokens > 0 ? providerTokens : threadTokens;
    status.textContent = formatContextUsage(
      tokens,
      effectiveWindow,
    );
    this._lastContextKey = cacheKey;
    this._lastContextText = status.textContent;
  }

  /**
   * Surface a stream sequence gap on the agent status bar so the user can
   * see that content may be incomplete without opening logs. Only fires for
   * the active traceId to avoid noise from stale/other turns.
   */
  surfaceStreamGap(traceId, streamSeq) {
    if (traceId !== this.activeTraceId) return;
    const status = $("#agent-provider-status");
    if (!status) return;
    status.textContent = `Stream incomplete · reconciling… (seq ${streamSeq})`;
    status.classList.add("is-stream-gap");
  }

  /**
   * Clear the stream-gap status indicator after reconcile (turn end, context
   * refresh, or next successful event). Restores normal context usage text.
   */
  clearStreamGapStatus() {
    const status = $("#agent-provider-status");
    if (!status || !status.classList.contains("is-stream-gap")) return;
    status.classList.remove("is-stream-gap");
    this.updateContextStatus();
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
    open.addEventListener("click", () => {
      this.toggleMobileConversations(false);
      this.runUiAction(this.open(conversation.id), "Could not open conversation");
    });
    const remove = element("button", "agent-conversation-delete", "×");
    remove.type = "button";
    remove.setAttribute("aria-label", `Delete ${conversation.title}`);
    remove.addEventListener("click", () => this.openDeleteDialog(conversation.id));
    row.append(open, remove);
    return row;
  }

  toggleMobileConversations(force) {
    const shell = $("#agent-shell");
    const button = $("#agent-mobile-conversations-btn");
    const overlay = $("#agent-mobile-conversations-overlay");
    if (!shell || !button || !overlay) return;
    const open = typeof force === "boolean" ? force : !shell.classList.contains("is-conversations-open");
    shell.classList.toggle("is-conversations-open", open);
    button.setAttribute("aria-expanded", String(open));
    overlay.hidden = !open;
    if (open) $("#agent-conversation-search")?.focus();
    else button.focus({ preventScroll: true });
  }

  isThreadAtBottom(thread = $("#agent-thread")) {
    if (!thread) return true;
    return thread.scrollHeight - thread.scrollTop - thread.clientHeight <= 4;
  }

  isSubpaneAtBottom(body = $("#agent-subpane-body")) {
    if (!body) return true;
    return body.scrollHeight - body.scrollTop - body.clientHeight <= 4;
  }

  scrollSubpaneToBottom({ force = false } = {}) {
    const body = $("#agent-subpane-body");
    if (!body || (!force && !this.subpaneShouldStickToBottom)) return;
    const apply = () => {
      if (!force && !this.subpaneShouldStickToBottom) return;
      body.scrollTop = body.scrollHeight;
    };
    apply();
    if (typeof requestAnimationFrame === "function") requestAnimationFrame(apply);
  }

  scrollToBottom({ force = false } = {}) {
    const thread = $("#agent-thread");
    if (!thread || (!force && !this.threadShouldStickToBottom)) return;
    const apply = () => {
      if (!force && !this.threadShouldStickToBottom) return;
      thread.scrollTop = thread.scrollHeight;
    };
    // Apply immediately so background-window frame throttling cannot make the
    // reader fall behind. A single rAF then settles any pending layout; rapid
    // calls within the same frame are coalesced (no per-call double rAF).
    apply();
    if (typeof requestAnimationFrame === "function" && !this._scrollSettledRaf) {
      this._scrollSettledRaf = requestAnimationFrame(() => {
        this._scrollSettledRaf = 0;
        apply();
      });
    }
  }

  renderThread() {
    const thread = $("#agent-thread");
    if (!thread) return;
    this.clearAcpAttentionStack();
    // A room load/switch is an explicit navigation event: start at the latest
    // message regardless of where the previous room was scrolled.
    this.threadShouldStickToBottom = true;
    // Chat switches destroy the DOM; drop live card-stream references so a
    // later restore can re-attach to recreated nodes without writing to
    // detached ones. Also dispose subagent event subscriptions so handlers
    // for the previous conversation do not fire into the new one (A3) and
    // cancel any pending streaming rAF/canvas timers for the old room (#34).
    this.disposeSubagentCardStream();
    this.rebindSubagentEvents();
    this.disposeStreamTimersForRoom();
    thread.textContent = "";
    this.failedMessage = null;
    this.updateAcpStatus();
    if (!this.conversation?.messages.length) {
      const empty = element("div", "agent-empty");
      empty.id = "agent-empty";
      if (this.conversation?.kind === "acp") {
        empty.innerHTML = "<div class=\"agent-empty-mark\">✦</div><h2>Start a conversation</h2><p>This conversation uses an external provider.</p>";
      } else {
        empty.innerHTML = "<div class=\"agent-empty-mark\">✦</div><h2>Start a conversation</h2><p>Choose a model, then tell the shell what you need.</p>";
      }
      thread.appendChild(empty);
      this.restoreRunningSubagentUi();
      this.restoreCanvas();
      this.restoreSubpane();
      return;
    }
    this.conversation.messages.forEach((message, index) => {
      // Insert a Codex-style "Context compacted" marker at the checkpoint
      // boundary so the user knows older turns were summarized for the model
      // but are still visible in the transcript (dual-space contract).
      if (this.conversation?.checkpoint?.summary && index === this.conversation.checkpoint.compactedMessageCount) {
        this.appendCompactionMarker(this.conversation.checkpoint);
      }
      this.appendMessage(message.role, message.content, { ...message, canvasMessageIndex: index });
    });
    void this.restoreActiveTurnUi();
    this.restoreRunningSubagentUi();
    this.scrollToBottom({ force: true });
    this.restoreCanvas();
    this.restoreSubpane();
  }

  /**
   * Rehydrate the parent Working draft from the application-layer active turn
   * projection after a chat switch (or late open while a turn is running).
   */
  async restoreActiveTurnUi() {
    if (!this.conversation || this.conversation.kind === "acp" || !this.getActiveTurn) {
      this.detectOrphanedTurn();
      return;
    }
    const conversationId = this.conversation.id;
    let snap;
    try {
      snap = await this.getActiveTurn(conversationId);
    } catch (error) {
      this.log?.("warn", `getActiveTurn failed: ${error.message || error}`);
      this.detectOrphanedTurn();
      return;
    }
    if (!snap || this.conversation?.id !== conversationId) {
      this.detectOrphanedTurn();
      return;
    }
    if (snap.status !== "running" && snap.status !== "awaiting_input") {
      this.detectOrphanedTurn();
      return;
    }

    this.activeTraceId = snap.traceId;
    this.pendingTurnConversations.add(conversationId);
    this.turnOwnerConversationId = conversationId;
    this.resetComposerForConversation(conversationId);
    const stopButton = $("#agent-stop-btn");
    if (stopButton) stopButton.hidden = false;

    // Prefer reusing an already-built pending message (live turn still painting).
    let host = document.querySelector("#agent-thread article.agent-message.agent-pending");
    if (!host) {
      host = this.createStreamingMessage();
    }
    if (!host) return;

    // Drop previous draft body (keep identity chrome) and rebuild from SoT.
    const identity = host.querySelector(".agent-message-identity");
    host.textContent = "";
    if (identity) host.appendChild(identity);

    for (const step of snap.steps ?? []) {
      if (step.type === "reasoning" && step.content?.trim()) {
        host.appendChild(this.reasoningDisclosure(step.content));
      } else if (step.type === "text" && step.content) {
        const bubble = element("div", "agent-bubble");
        bubble.innerHTML = renderAssistantMarkdown(step.content);
        host.appendChild(bubble);
      } else if (step.type === "tool_calls" && Array.isArray(step.calls)) {
        host.appendChild(this.toolActivity(step.calls.map((call) => ({
          id: call.id,
          name: call.name,
          ok: call.ok !== false,
          ...(call.args ? { args: call.args } : {}),
          ...(call.output ? { output: call.output } : {}),
          ...(call.result !== undefined ? { result: call.result } : {}),
          ...(call.error ? { error: call.error } : {}),
        }))));
      }
    }
    for (const tool of snap.openTools ?? []) {
      const card = this.createStreamingToolCard(tool.id, tool.name, tool.args);
      if (tool.status !== "running") {
        this.updateStreamingToolCard(card, {
          callId: tool.id,
          name: tool.name,
          ok: tool.status !== "fail",
          args: tool.args,
          output: tool.output,
          error: tool.error,
        });
      }
      host.appendChild(card);
    }
    if (snap.streaming?.content) {
      if (snap.streaming.kind === "reasoning") {
        const el = this.createStreamingReasoningBlock();
        const content = el.querySelector(".agent-reasoning-content");
        if (content) content.innerHTML = renderReasoningMarkdown(snap.streaming.content);
        host.appendChild(el);
        if (this.liveStreamState?.message && this.turnOwnerConversationId === conversationId) {
          this.liveStreamState.reasoningEl = el;
          this.liveStreamState.reasoningText = snap.streaming.content;
          this.liveStreamState.lastKind = "reasoning";
        }
      } else {
        const bubble = element("div", "agent-bubble");
        bubble.innerHTML = renderAssistantMarkdown(snap.streaming.content);
        host.appendChild(bubble);
        if (this.liveStreamState?.message && this.turnOwnerConversationId === conversationId) {
          this.liveStreamState.textBubble = bubble;
          this.liveStreamState.streamedText = snap.streaming.content;
          this.liveStreamState.lastKind = "text";
        }
      }
    }

    // Rebind the still-running submit() painter to the recreated host so live
    // deltas paint again after a chat switch (DOM was wiped by renderThread).
    if (this.liveStreamState && this.turnOwnerConversationId === conversationId && this.activeTraceId === snap.traceId) {
      this.liveStreamState.message = host;
      this.liveStreamState.toolCards = new Map();
      host.querySelectorAll("[data-call-id], [data-streaming-subagent]").forEach((card) => {
        const callId = card.dataset.callId || card.dataset.runId;
        if (callId) this.liveStreamState.toolCards.set(callId, card);
      });
    }
    this.scrollToBottom();
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

  appendCompactionMarker(checkpoint) {
    const thread = $("#agent-thread");
    if (!thread) return;
    const marker = element("div", "agent-compaction-marker");
    marker.setAttribute("role", "status");
    marker.setAttribute("aria-label", "Context compacted");
    marker.textContent = "Context compacted — older turns summarized for the model";
    thread.appendChild(marker);
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
      const retryLabel = meta.retryLabel || "Retry";
      const retry = element("button", "agent-retry-btn", retryLabel);
      retry.type = "button";
      if (meta.retryDisabled) retry.disabled = true;
      // Rate-limit cooldown: keep the button disabled with a countdown (#45).
      if (typeof meta.retryCooldownMs === "number" && meta.retryCooldownMs > 0) {
        this.startRetryCooldown(retry, retryLabel, meta.retryCooldownMs);
      } else {
        retry.addEventListener("click", () => void this.submit({ retry: true }));
      }
      actions.prepend(retry);
    }
    footer.appendChild(actions);
    message.appendChild(footer);

    thread.appendChild(message);
    this.scrollToBottom();
    if (role === "assistant" && !meta.pending && !meta.error) {
      this.enhanceCodeFences(message, meta.canvasMessageIndex ?? this.currentMessageIndex());
    }
    return message;
  }

  startRetryCooldown(button, label, cooldownMs) {
    // Simple countdown backoff so a rate-limited turn is not spammed. Re-enables
    // with a countdown in the label, then restores the click handler (#45).
    let remaining = Math.ceil(cooldownMs / 1000);
    button.disabled = true;
    const tick = () => {
      if (remaining <= 0) {
        button.disabled = false;
        button.textContent = label;
        button.addEventListener("click", () => void this.submit({ retry: true }));
        return;
      }
      button.textContent = `${label} (${remaining}s)`;
      remaining -= 1;
      window.setTimeout(tick, 1000);
    };
    tick();
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
    this.bindCanvasResize();
    document.addEventListener("keydown", (event) => {
      const pane = $("#agent-canvas");
      if (!pane || pane.hidden || !pane.classList.contains("is-open")) return;
      if (event.key === "Tab") {
        this.trapDrawerFocus(event, pane);
        return;
      }
      if (event.key !== "Escape") return;
      this.closeCanvasSidebar();
    });
    $("#agent-subpane-close")?.addEventListener("click", () => this.closeSubpaneSidebar());
    $("#agent-subpane-overlay")?.addEventListener("click", () => this.closeSubpaneSidebar());
    $("#agent-subpane-body")?.addEventListener("scroll", (event) => {
      this.subpaneShouldStickToBottom = this.isSubpaneAtBottom(event.currentTarget);
    }, { passive: true });
    document.addEventListener("keydown", (event) => {
      const subpane = $("#agent-subpane");
      if (!subpane || subpane.hidden || !subpane.classList.contains("is-open")) return;
      if (event.key === "Tab") {
        this.trapDrawerFocus(event, subpane);
        return;
      }
      if (event.key !== "Escape") return;
      this.closeSubpaneSidebar();
    });
  }

  bindCanvasResize() {
    const handle = $("#agent-canvas-resize");
    if (!handle) return;
    try {
      const saved = window.localStorage.getItem(CANVAS_DRAWER_WIDTH_KEY);
      if (saved) this.setCanvasDrawerWidth(Number(saved), { persist: false });
    } catch {
      // Storage is optional; resizing still works in restricted profiles.
    }

    handle.addEventListener("pointerdown", (event) => {
      if (event.button !== 0) return;
      event.preventDefault();
      const startX = event.clientX;
      const startWidth = this.getCanvasDrawerWidth();
      const move = (moveEvent) => {
        this.setCanvasDrawerWidth(startWidth + startX - moveEvent.clientX);
      };
      const stop = () => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", stop);
        window.removeEventListener("pointercancel", stop);
        document.body.classList.remove("agent-canvas-resizing");
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", stop, { once: true });
      window.addEventListener("pointercancel", stop, { once: true });
      document.body.classList.add("agent-canvas-resizing");
    });
    handle.addEventListener("keydown", (event) => {
      if (event.key !== "ArrowLeft" && event.key !== "ArrowRight" && event.key !== "Home" && event.key !== "End") return;
      event.preventDefault();
      const current = this.getCanvasDrawerWidth();
      const next = event.key === "ArrowLeft"
        ? current + 24
        : event.key === "ArrowRight"
          ? current - 24
          : event.key === "Home" ? 960 : 360;
      this.setCanvasDrawerWidth(next);
    });
    window.addEventListener("resize", () => {
      if (this.canvasDrawerWidth !== null) this.setCanvasDrawerWidth(this.canvasDrawerWidth, { persist: false });
    });
  }

  getCanvasDrawerWidth() {
    if (this.canvasDrawerWidth !== null) return this.canvasDrawerWidth;
    return clampCanvasDrawerWidth(560);
  }

  setCanvasDrawerWidth(width, { persist = true } = {}) {
    const pane = $("#agent-canvas");
    const next = clampCanvasDrawerWidth(width);
    this.canvasDrawerWidth = next;
    pane?.style.setProperty("--agent-canvas-width", `${next}px`);
    if (!persist) return;
    try {
      window.localStorage.setItem(CANVAS_DRAWER_WIDTH_KEY, String(next));
    } catch {
      // Storage is optional; the current session keeps the live width.
    }
  }

  bindSubagentEvents() {
    this.subagentLifecycle.reset();
    this.rebindSubagentEvents();
  }

  /**
   * Re-subscribe to subagent lifecycle events without resetting stream state.
   * Called by renderThread() after disposing the previous subscription so
   * subagent.run_started/ended events for the new conversation are not
   * silently dropped. Stream state (subagentStreamState, activeSubagentCardStream,
   * subagentOwnerConversationId) is preserved so restoreRunningSubagentUi can
   * rebuild the in-chat card from the live snapshot.
   */
  rebindSubagentEvents() {
    this.subagentLifecycle._conversationId = this.conversation?.id;
    this.subagentLifecycle.rebindEvents(() => subscribeSubagentEvents({
      onRunStarted: (p) => this.handleSubagentRunStarted(p),
      onRunEnded: (p) => this.handleSubagentRunEnded(p),
    }));
  }

  isViewingSubagentOwner() {
    // When no owner is tracked (unit tests / early path), keep painting the
    // shared subpane so callers don't need to mock conversation ownership.
    if (!this.subagentOwnerConversationId) return true;
    return Boolean(this.conversation?.id && this.conversation.id === this.subagentOwnerConversationId);
  }

  handleSubagentRunStarted(p) {
    if (!this.conversation) return;
    const ownerId = this.conversation.id;
    this.subagentOwnerConversationId = ownerId;
    const run = {
      id: `run-${p.runId}`,
      conversationId: ownerId,
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
    this.shell.agentConversations.upsertSubagentRun(ownerId, run).then((conv) => {
      // Only mirror store results into local state when still viewing the owner.
      if (this.conversation?.id === ownerId) this.conversation = conv;
      return this.shell.agentConversations.setActiveSubagentRun(ownerId, run.runId);
    }).then((conv) => {
      if (this.conversation?.id === ownerId) this.conversation = conv;
    }).catch((err) => this.log?.("error", `Subagent run start persist failed: ${err}`));

    this.mountSubpane(run, { resumeStream: false, open: false });

    this.attachSubagentCardStream(p.runId);

    this.subagentLifecycle.startRun(() => this.bindLiveSubagentStream(p.runId));
  }

  handleSubagentRunEnded(p) {
    this.subagentLifecycle.endRunDisposeStream();

    const status = p.ok ? "ok" : "fail";
    if (this.isViewingSubagentOwner()) {
      this.setSubpaneStatus(`● ${status.toUpperCase()}`, p.ok ? "is-ok" : "is-fail");
      if (!p.ok && p.error) {
        this.setSubpaneError(p.error);
      }
    }

    this.sealSubagentStreamSegment();
    const steps = this.snapshotSubagentSteps();
    if (this.activeSubagentRun) {
      this.activeSubagentRun = { ...this.activeSubagentRun, status, ...(p.summary ? { summary: p.summary } : {}), ...(p.error ? { error: p.error } : {}), ...(steps?.length ? { steps } : {}) };
    }
    if (this.isViewingSubagentOwner()) {
      this.renderSubagentStreamState();
    }
    const ownerId = this.subagentOwnerConversationId || this.conversation?.id || null;
    this.subagentLifecycle.endRunClearState();

    // Stop appending to the in-chat mini stream; the frozen tail stays
    // visible until the parent turn's tool card is replaced on tool_call_end.
    this.disposeSubagentCardStream();

    if (ownerId) {
      this.shell.agentConversations.updateSubagentRunStatus(
        ownerId,
        p.runId,
        status,
        {
          ...(p.summary ? { summary: p.summary } : {}),
          ...(p.error ? { error: p.error } : {}),
          ...(steps?.length ? { steps } : {}),
        },
      ).then((conv) => {
        if (this.conversation?.id === ownerId) this.conversation = conv;
      }).catch((err) => this.log?.("error", `Subagent run end persist failed: ${err}`));
    }
  }

  bindLiveSubagentStream(runId) {
    return subscribeSubagentStream(runId, {
      onDelta: (delta) => {
        this.appendSubpaneText(delta);
        if (this.isViewingSubagentOwner()) this.appendCardStreamText(delta);
      },
      onReasoningDelta: (delta) => {
        this.appendSubpaneThought(delta);
        if (this.isViewingSubagentOwner()) this.appendCardStreamThought(delta);
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
        if (this.isViewingSubagentOwner()) this.appendCardStreamToolCall(call);
      },
      onToolCallEnd: (params) => {
        const status = params.ok ? "ok" : "fail";
        this.updateSubpaneToolCall(params.callId, status, params.summary);
        if (this.isViewingSubagentOwner()) this.updateCardStreamToolCall(params.callId, status, params.summary);
      },
      onPlan: (steps) => {
        this.appendSubpanePlan(steps, { persist: true });
        if (this.isViewingSubagentOwner()) this.appendCardStreamPlan(steps);
      },
      onPermissionRequest: (payload) => {
        if (!this.isViewingSubagentOwner()) return;
        const card = this.createAcpPermissionCard(payload);
        if (card) this.mountAcpAttentionCard(card);
      },
      onAskRequest: (payload) => {
        if (!this.isViewingSubagentOwner()) return;
        const card = this.createAcpAskCard(payload);
        if (card) this.mountAcpAttentionCard(card);
      },
    });
  }

  openCanvasDrawerUi() {
    const pane = $("#agent-canvas");
    const overlay = $("#agent-canvas-overlay");
    if (!pane) return;
    this.canvasReturnFocus = document.activeElement instanceof HTMLElement && !pane.contains(document.activeElement)
      ? document.activeElement
      : this.canvasReturnFocus;
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
    const returnFocus = this.canvasReturnFocus;
    this.canvasReturnFocus = null;
    pane?.classList.remove("is-open");
    overlay?.classList.remove("is-open");
    if (body) body.textContent = "";
    // Allow slide-out to finish before display:none, unless reduced motion.
    const hide = () => {
      if (pane) pane.hidden = true;
      if (overlay) overlay.hidden = true;
      if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true });
    };
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      hide();
      return;
    }
    window.setTimeout(hide, 260);
  }

  enhanceCodeFences(messageEl, messageIndex, { onlyCompleteCanvas = false, completeCanvasKeys = null } = {}) {
    if (!messageEl) return;
    const conversationId = this.conversation?.id;
    const blocks = messageEl.querySelectorAll("pre > code");
    if (!blocks.length) return;
    let canvasFenceIndex = 0;
    const allowedCanvasKeys = onlyCompleteCanvas
      ? (completeCanvasKeys ?? new Set(extractCanvasCandidates(messageEl.querySelector(".agent-bubble")?.textContent ?? "")
        .filter((candidate) => candidate.complete)
        .map((candidate) => `${candidate.kind}\u0000${candidate.source}`)))
      : null;
    blocks.forEach((code) => {
      const pre = code.parentElement;
      if (!pre || pre.dataset.codeActionsBound === "true") return;
      const langClass = [...code.classList].find((cls) => cls.startsWith("language-"));
      const lang = langClass ? langClass.slice("language-".length) : "";
      const rawSource = code.textContent ?? "";
      const resolved = this.canvasEnabled ? resolveCanvasFence(lang, rawSource) : null;
      const canvasKey = resolved ? `${resolved.kind}\u0000${resolved.source}` : "";
      if (resolved && conversationId && (!allowedCanvasKeys || allowedCanvasKeys.has(canvasKey))) {
        const { kind, source } = resolved;
        const artifactId = canvasArtifactId(conversationId, String(messageIndex), canvasFenceIndex);
        const tooLarge = source.length > CANVAS_ARTIFACT_MAX_SOURCE_BYTES;
        const title = `${kind} ${canvasFenceIndex + 1}`;
        this.decorateCanvasFence(pre, code, {
          artifactId,
          kind,
          source,
          title,
          tooLarge,
          messageIndex,
          fenceIndex: canvasFenceIndex,
        });
        canvasFenceIndex += 1;
        return;
      }
      this.decorateRawCodeFence(pre, rawSource);
    });
  }

  decorateRawCodeFence(pre, source) {
    const actions = element("div", "agent-code-actions");
    const copy = iconButton("Copy code", copyIcon());
    copy.addEventListener("click", () => void this.copyMessage(source, copy));
    actions.appendChild(copy);
    pre.dataset.codeActionsBound = "true";
    pre.after(actions);
  }

  decorateCanvasFence(pre, code, ctx) {
    if (ctx.kind === "html") {
      this.decorateHtmlFence(pre, code, ctx);
      return;
    }
    // svg / mermaid: lazy inline render — a placeholder is shown until the
    // fence approaches the viewport, avoiding eager render of every diagram in
    // a long thread. The raw fence collapses once the render succeeds.
    const actions = element("div", "agent-canvas-fence-actions");
    const download = iconButton("Download source", downloadIcon());
    download.classList.add("agent-code-action");
    download.addEventListener("click", () => this.downloadFenceSource(ctx.source, ctx.kind, ctx.fenceIndex));
    const sidebar = element("button", "agent-canvas-fence-btn", "Sidebar");
    sidebar.type = "button";
    sidebar.addEventListener("click", () => void this.openCanvasSidebar(ctx));
    const showSource = element("button", "agent-canvas-fence-btn", "Show source");
    showSource.type = "button";
    const getPreview = () =>
      pre.parentElement?.querySelector(`.agent-canvas-inline[data-artifact-id="${cssEscape(ctx.artifactId)}"]`) ?? null;
    bindCanvasSourceToggle({ pre, showSource, getPreview });
    actions.append(download, sidebar, showSource);
    pre.dataset.codeActionsBound = "true";
    // Do not hide the source until the lazy render actually happens; hide it
    // inside the reveal so the fallback restores it on failure.
    pre.after(actions);

    const getContainer = () =>
      pre.parentElement?.querySelector(`.agent-canvas-inline[data-artifact-id="${cssEscape(ctx.artifactId)}"]`) ?? null;
    bindLazyCanvasReveal({
      host: pre,
      getContainer,
      root: document.getElementById("agent-thread"),
      onReveal: () => {
        pre.hidden = true;
        return this.renderInlineCanvas(pre, code, ctx).then((ok) => {
          if (!ok) setCanvasSourceVisible({ pre, showSource, getPreview, visible: true });
        }).catch((error) => {
          setCanvasSourceVisible({ pre, showSource, getPreview, visible: true });
          this.log?.("error", `Canvas inline render failed: ${error.message || error}`);
        });
      },
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
    const download = iconButton("Download source", downloadIcon());
    download.classList.add("agent-code-action");
    download.addEventListener("click", () => this.downloadFenceSource(ctx.source, ctx.kind, ctx.fenceIndex));
    const sidebar = element("button", "agent-canvas-fence-btn", "Sidebar");
    sidebar.type = "button";
    sidebar.addEventListener("click", () => void this.openCanvasSidebar(ctx));
    const showSource = element("button", "agent-canvas-fence-btn", "Show source");
    showSource.type = "button";
    const getPreview = () =>
      pre.parentElement?.querySelector(`.agent-canvas-inline-preview[data-artifact-id="${cssEscape(ctx.artifactId)}"]`) ?? null;
    bindCanvasSourceToggle({ pre, showSource, getPreview });
    actions.append(download, sidebar, showSource);
    pre.dataset.codeActionsBound = "true";

    card.append(head, actions);
    // The source stays visible until the artifact is revealed.
    pre.after(card);
    const getPreviewAfter = () =>
      pre.parentElement?.querySelector(`.agent-canvas-inline-preview[data-artifact-id="${cssEscape(ctx.artifactId)}"]`) ?? null;
    bindLazyCanvasReveal({
      host: pre,
      getContainer: () => getPreviewAfter() ?? null,
      root: document.getElementById("agent-thread"),
      onReveal: () => {
        pre.hidden = true;
        return this.mountInlineHtmlPreview(pre, card, ctx).catch((error) => {
          setCanvasSourceVisible({ pre, showSource, getPreview, visible: true });
          this.log?.("error", `Inline HTML render failed: ${error.message || error}`);
        });
      },
    });
  }

  async mountInlineHtmlPreview(pre, card, ctx) {
    const existing = pre.parentElement?.querySelector(`.agent-canvas-inline-preview[data-artifact-id="${cssEscape(ctx.artifactId)}"]`);
    if (existing) return;
    const result = await this.renderCanvasArtifact(ctx);
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
    const result = await this.renderCanvasArtifact(ctx);
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
    // Contained failure card — never mount Mermaid's body-level error diagram.
    if (result.type === "error") {
      container.classList.add("is-error");
      const errorBox = element("div", "agent-canvas-error", result.message || "Could not render this diagram. Showing source below.");
      container.appendChild(errorBox);
      const actions = pre.nextElementSibling?.classList?.contains("agent-canvas-fence-actions")
        ? pre.nextElementSibling
        : null;
      if (actions) actions.before(container);
      else pre.before(container);
      return false;
    }
    // Leave the original code block visible; do not crash on a bad diagram.
    return false;
  }

  /**
   * Render a canvas artifact with a bounded source cache. Streaming markdown
   * rebuilds the code fence DOM frequently; reusing the SVG keeps live preview
   * responsive without invoking Mermaid for unchanged source.
   */
  async renderCanvasArtifact(ctx) {
    const key = `${ctx.kind}\u0000${ctx.source}`;
    const cached = this.canvasRenderCache.get(key);
    if (cached) return cached;
    const renderPromise = renderArtifact({ kind: ctx.kind, source: ctx.source });
    this.canvasRenderCache.set(key, renderPromise);
    while (this.canvasRenderCache.size > 24) {
      this.canvasRenderCache.delete(this.canvasRenderCache.keys().next().value);
    }
    return renderPromise;
  }

  scheduleStreamingCanvasEnhancement(streamState, conversationId) {
    if (!this.canvasEnabled || !conversationId || streamState.canvasRenderTimer) return;
    const candidates = extractCanvasCandidates(streamState.streamedText);
    const completeCanvasKeys = new Set(candidates
      .filter((candidate) => candidate.complete && candidate.kind === "mermaid")
      .map((candidate) => `${candidate.kind}\u0000${candidate.source}`));
    if (!completeCanvasKeys.size) return;
    streamState.canvasRenderTimer = window.setTimeout(() => {
      streamState.canvasRenderTimer = 0;
      if (this.conversation?.id !== conversationId || !streamState.message?.isConnected) return;
      this.enhanceCodeFences(streamState.message, this.currentMessageIndex(), { onlyCompleteCanvas: true, completeCanvasKeys });
    }, 350);
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
    const run = this.conversation.subagentRuns?.find((item) => item.runId === activeId)
      ?? this.conversation.subagentRuns?.find((item) => item.status === "running");
    if (!run) {
      // Different conversation: leave live stream subscribed but clear foreign body
      // only when this conversation does not own the live run.
      if (!this.isViewingSubagentOwner()) {
        this.closeSubpaneDrawerUi();
      }
      return;
    }
    // Keep stream subscription for an in-flight run, but do not auto-open the drawer.
    if (run.status === "running") {
      this.mountSubpane(run, { resumeStream: true, open: false });
    } else if (this.conversation.activeSubagentRunId === run.runId) {
      this.mountSubpane(run, { open: false });
    }
  }

  /**
   * Rehydrate a live subagent card after a chat switch (renderThread wipes the
   * pending Working message that hosted the original card).
   */
  restoreRunningSubagentUi() {
    if (!this.conversation) return;
    const running = (this.conversation.subagentRuns ?? []).filter((run) => run.status === "running");
    if (running.length === 0) return;

    const thread = $("#agent-thread");
    if (!thread) return;
    $("#agent-empty")?.remove();

    let host = thread.querySelector("article.agent-message.agent-pending");
    if (!host) {
      host = this.createStreamingMessage();
    }
    if (!host) return;

    for (const run of running) {
      let card = host.querySelector(`.agent-subagent-card[data-run-id="${CSS.escape(run.runId)}"]`);
      if (!card) {
        card = this.renderSubagentCard({
          runId: run.runId,
          providerId: run.providerId,
          title: run.title || "Subagent run",
          status: "running",
        });
        card.dataset.streamingSubagent = "1";
        host.appendChild(card);
      }
      if (this.subagentStreamState?.runId === run.runId || this.subagentOwnerConversationId === this.conversation.id) {
        this.attachSubagentCardStream(run.runId);
        this.rebuildCardStreamFromState();
      }
    }
  }

  rebuildCardStreamFromState() {
    const cardState = this.activeSubagentCardStream;
    const streamState = this.subagentStreamState;
    if (!cardState?.el || !streamState || streamState.runId !== cardState.runId) return;
    cardState.el.textContent = "";
    cardState.lastKind = null;
    cardState.textContent = "";
    cardState.thoughtContent = "";
    cardState.textRow = null;
    cardState.thoughtRow = null;
    cardState.toolRows = new Map();

    for (const step of streamState.steps ?? []) {
      if (step.type === "reasoning" && typeof step.content === "string" && step.content.trim()) {
        this.appendCardStreamThought(step.content);
        this.sealCardStreamSegment(cardState);
      } else if (step.type === "text" && typeof step.content === "string" && step.content.trim()) {
        this.appendCardStreamText(step.content);
        this.sealCardStreamSegment(cardState);
      } else if (step.type === "tool_calls" && Array.isArray(step.calls)) {
        for (const call of step.calls) {
          this.appendCardStreamToolCall({
            id: call.id,
            title: call.name,
            status: call.ok === false ? "fail" : "ok",
            args: call.args,
          });
          if (call.output) this.updateCardStreamToolCall(call.id, call.ok === false ? "fail" : "ok", call.output);
        }
      } else if (step.type === "plan" && Array.isArray(step.steps)) {
        this.appendCardStreamPlan(step.steps);
      }
    }
    if (streamState.lastKind === "reasoning" && streamState.thoughtContent) {
      this.appendCardStreamThought(streamState.thoughtContent);
    } else if (streamState.lastKind === "text" && streamState.textContent) {
      this.appendCardStreamText(streamState.textContent);
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
    this.downloadFenceSource(artifact.source, artifact.kind, artifact.fenceIndex);
  }

  downloadFenceSource(source, kind, fenceIndex = 0) {
    const blob = new Blob([source], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${kind}-${fenceIndex + 1}.${kind === "mermaid" ? "mmd" : kind}`;
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
    this.subpaneReturnFocus = document.activeElement instanceof HTMLElement && !pane.contains(document.activeElement)
      ? document.activeElement
      : this.subpaneReturnFocus;
    this.subpaneShouldStickToBottom = true;
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
    const returnFocus = this.subpaneReturnFocus;
    this.subpaneReturnFocus = null;
    pane?.classList.remove("is-open");
    overlay?.classList.remove("is-open");
    const hide = () => {
      if (pane) pane.hidden = true;
      if (overlay) overlay.hidden = true;
      if (returnFocus?.isConnected) returnFocus.focus({ preventScroll: true });
    };
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      hide();
      return;
    }
    window.setTimeout(hide, 260);
  }

  trapDrawerFocus(event, pane) {
    const focusable = [...pane.querySelectorAll("button, [href], input, textarea, select, [tabindex]:not([tabindex=\"-1\"])")]
      .filter((node) => !node.disabled && !node.hidden && node.getClientRects().length > 0);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable.at(-1);
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
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

    // Prefer the live ACP runId when a still-running stream is bound. Streaming
    // cards initially close over the parent tool callId; remounting with that
    // synthetic id used to reset the real stream and leave the sidebar empty.
    let effectiveRun = run;
    if (
      run?.status === "running"
      && this.subagentStreamState?.runId
      && this.subagentStreamState.runId !== run.runId
      && this.subagentStreamDisposer
      && (this.activeSubagentRun?.status === "running" || this.subagentOwnerConversationId === this.conversation?.id)
    ) {
      effectiveRun = {
        ...run,
        runId: this.subagentStreamState.runId,
        ...(this.activeSubagentRun?.providerId ? { providerId: this.activeSubagentRun.providerId } : {}),
        ...(this.activeSubagentRun?.title ? { title: this.activeSubagentRun.title } : {}),
      };
    }

    if (badge) badge.textContent = (effectiveRun.providerId || "—").slice(0, 6).toUpperCase();
    if (title) title.textContent = effectiveRun.title || "Subagent";
    if (status) {
      status.textContent = `● ${effectiveRun.status.toUpperCase()}`;
      status.className = "agent-subpane-status";
      if (effectiveRun.status === "running") status.classList.add("is-running");
      else if (effectiveRun.status === "ok") status.classList.add("is-ok");
      else if (effectiveRun.status === "fail" || effectiveRun.status === "cancelled") status.classList.add("is-fail");
    }
    this.activeSubagentRun = effectiveRun;
    if (effectiveRun.status === "running") {
      if (!this.subagentStreamState || this.subagentStreamState.runId !== effectiveRun.runId) {
        this.resetSubagentStreamState(effectiveRun.steps ?? [], effectiveRun.runId);
      }
      // Live DOM nodes (textEl/thoughtEl/tool terminals) may be detached after a
      // thread re-render; always rebuild from the durable snapshot before show.
      this.renderSubagentStreamState();
      if (options.resumeStream && !this.subagentStreamDisposer) {
        this.subagentStreamDisposer = this.bindLiveSubagentStream(effectiveRun.runId);
      }
    } else {
      body.textContent = "";
      if (effectiveRun.steps?.length) this.renderSubpaneSteps(effectiveRun.steps);
      if (effectiveRun.error) this.setSubpaneError(effectiveRun.error);
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
      textRenderTimer: 0,
    };
  }

  sealSubagentStreamSegment() {
    const state = this.subagentStreamState;
    if (!state) return;
    if (state.textRenderTimer) {
      window.clearTimeout(state.textRenderTimer);
      state.textRenderTimer = 0;
    }
    if (state.lastKind === "text" && state.textEl) {
      state.textEl.innerHTML = renderAssistantMarkdown(state.textContent);
    }
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
    this.scrollSubpaneToBottom({ force: true });
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
    if (!this.isViewingSubagentOwner()) return;
    const body = $("#agent-subpane-body");
    if (!body) return;
    if (!state.thoughtEl || !body.contains(state.thoughtEl)) {
      state.thoughtEl = this.createStreamingReasoningBlock();
      body.appendChild(state.thoughtEl);
    }
    const content = state.thoughtEl.querySelector(".agent-reasoning-content");
    if (content) content.innerHTML = renderReasoningMarkdown(state.thoughtContent);
    this.scrollSubpaneToBottom();
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
    if (!this.isViewingSubagentOwner()) return;
    const body = $("#agent-subpane-body");
    if (!body) return;
    if (!state.textEl || !body.contains(state.textEl)) {
      state.textEl = element("div", "agent-subpane-text agent-bubble");
      body.appendChild(state.textEl);
    }
    // Keep short replies immediate for responsive feedback. Once a parallel
    // subagent produces a large answer, avoid reparsing the full answer for
    // every tiny delta; this callback runs synchronously on the IPC event
    // fan-out path and can otherwise starve the parent turn's deltas.
    if (state.textContent.length < 4096) {
      state.textEl.innerHTML = renderAssistantMarkdown(state.textContent);
    } else if (!state.textRenderTimer) {
      state.textRenderTimer = window.setTimeout(() => {
        state.textRenderTimer = 0;
        if (state.lastKind === "text" && state.textEl) {
          state.textEl.innerHTML = renderAssistantMarkdown(state.textContent);
        }
      }, 50);
    }
    this.scrollSubpaneToBottom();
  }

  appendSubpaneToolCall(call, options = {}) {
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
    if (!this.isViewingSubagentOwner()) return;
    const body = $("#agent-subpane-body");
    if (!body) return;
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
    this.scrollSubpaneToBottom();
  }

  updateSubpaneToolCall(callId, status, summary) {
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
    if (!this.isViewingSubagentOwner()) return;
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
  }

  appendSubpanePlan(steps, options = {}) {
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
    if (!this.isViewingSubagentOwner()) return;
    const body = $("#agent-subpane-body");
    if (!body) return;
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

  appendCardStreamRow(state, kind, mark, text, { key = null, markdown = false } = {}) {
    if (!state?.el) return null;
    const row = element("div", `agent-subagent-card-stream-row is-${kind}`);
    const markEl = element("span", "agent-subagent-card-stream-mark", mark);
    // Markdown HTML uses block tags — must live in a div, not a span.
    const textEl = element(markdown ? "div" : "span", "agent-subagent-card-stream-text");
    if (markdown) textEl.innerHTML = renderAssistantMarkdown(text);
    else textEl.textContent = text;
    row.append(markEl, textEl);
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
      state.thoughtRow = this.appendCardStreamRow(state, "thinking", "⌁", "Thinking…", { markdown: true });
    }
    state.thoughtContent += delta;
    if (state.thoughtRow) {
      const text = state.thoughtRow.querySelector(".agent-subagent-card-stream-text");
      if (text) text.innerHTML = renderReasoningMarkdown(truncateCardStreamMarkdown(state.thoughtContent, 240));
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
      state.textRow = this.appendCardStreamRow(state, "text", "·", "", { markdown: true });
    }
    state.textContent += delta;
    if (state.textRow) {
      const text = state.textRow.querySelector(".agent-subagent-card-stream-text");
      if (text) text.innerHTML = renderAssistantMarkdown(truncateCardStreamMarkdown(state.textContent, 320));
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
    const row = this.appendCardStreamRow(state, "tool", mark, label, { key: call.id });
    if (row) {
      const text = row.querySelector(".agent-subagent-card-stream-text");
      if (text) text.dataset.label = label;
    }
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
        const base = text.dataset.label || stripMarkdownOneLine(text.textContent?.split(" — ")[0] || text.textContent || "");
        if (!text.dataset.label) text.dataset.label = base;
        // Tool results can be long markdown; card row stays a compact one-liner.
        text.textContent = `${base} — ${stripMarkdownOneLine(String(summary), 80)}`;
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
      // Prefer dataset (rewritten to the real ACP runId on run_started) over
      // the closed-over tool callId that streaming cards are created with.
      const resolvedRunId = card.dataset.runId || run.runId;
      const latest = this.conversation?.subagentRuns?.find((item) => item.runId === resolvedRunId)
        ?? (this.activeSubagentRun?.runId === resolvedRunId || this.subagentStreamState?.runId === resolvedRunId
          ? { ...run, ...this.activeSubagentRun, runId: this.subagentStreamState?.runId || resolvedRunId, status: "running" }
          : { ...run, runId: resolvedRunId });
      if (this.conversation) {
        void this.shell.agentConversations.setActiveSubagentRun(this.conversation.id, latest.runId).catch(() => undefined);
      }
      const body = $("#agent-subpane-body");
      const samePreparedRun = this.activeSubagentRun?.runId === latest.runId
        && (body?.childNodes.length ?? 0) > 0
        && (!this.subagentStreamState || this.subagentStreamState.runId === latest.runId);
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
      // Ticket #42: a successful subagent run must keep a sealed card in the
      // thread (like terminal tools) instead of being removed. renderSubagentCard
      // already supports status "ok" + summary; only the ok/null early-return was
      // dropping the card from the DOM.
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
    this.scrollToBottom();
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
      { open: false, running: true },
    );
    if (args && typeof args === "object") card._toolArgs = args;
    return card;
  }

  updateStreamingToolCard(card, payload) {
    if (card.classList.contains("agent-ask-card")) {
      this.sealAskCard(card, payload);
      this.scrollToBottom();
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
        this.scrollToBottom();
        return sealed;
      }
      card.remove();
      this.scrollToBottom();
      return null;
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
    this.scrollToBottom();
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
    // Durable store: parent-turn end must not leave status=running subagents
    // or a stuck activeSubagentRunId (seen after IPC TIMEOUT mid deep-dive).
    void this.failStrandedSubagentRuns("Subagent run did not finish before the parent turn ended.");
  }

  /**
   * Mark every still-running subagent on the owner conversation as failed and
   * clear `activeSubagentRunId`. Safe to call multiple times (no-op when idle).
   */
  failStrandedSubagentRuns(reason = "Subagent run did not finish before the parent turn ended.") {
    const ownerId = this.turnOwnerConversationId || this.conversation?.id;
    const api = this.shell?.agentConversations;
    if (!ownerId || !api?.updateSubagentRunStatus) return Promise.resolve();
    const running = (this.conversation?.subagentRuns ?? []).filter((run) => run.status === "running");
    const ops = running.map((run) =>
      api.updateSubagentRunStatus(ownerId, run.runId, "fail", { error: reason }),
    );
    if (ops.length === 0 && !this.conversation?.activeSubagentRunId) {
      return Promise.resolve();
    }
    return Promise.all(ops)
      .then(async (results) => {
        let conv = results.at(-1);
        if (api.setActiveSubagentRun && (this.conversation?.activeSubagentRunId || running.length)) {
          conv = await api.setActiveSubagentRun(ownerId, null);
        }
        if (conv && this.conversation?.id === ownerId) this.conversation = conv;
      })
      .catch((err) => {
        this.log?.("error", `failStrandedSubagentRuns failed: ${err?.message || String(err)}`);
      });
  }

  /**
   * B2: Called when the WebSocket connection is lost. Seal any in-flight
   * tool cards as incomplete so the UI does not leave them "running" forever.
   */
  handleConnectionLost() {
    if (!this.turnPending) return;
    this.sealStreamingToolCardsIncomplete(this.liveStreamState);
    const status = $("#agent-provider-status");
    if (status) status.textContent = "Connection lost · reconnecting…";
    this.log?.("warn", "WebSocket connection lost during turn");
  }

  /**
   * B2: Called when the WebSocket reconnects. Reconcile the active turn from
   * the backend projection rather than guessing what events were missed.
   */
  handleConnectionRestored() {
    if (!this.turnPending) return;
    const status = $("#agent-provider-status");
    if (status) status.textContent = "Reconnected · reconciling…";
    // Rehydrate from projection — the backend is the SoT for mid-turn state.
    if (this.conversation?.id && this.getActiveTurn) {
      void this.restoreActiveTurnUi();
    }
    this.log?.("info", "WebSocket reconnected during turn");
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
    this.updateAcpAttentionState();
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

  mountAcpAttentionCard(card) {
    const list = $("#agent-attention-list");
    const stack = $("#agent-attention-stack");
    if (!list || !stack || !card) return;
    const requestId = card.dataset.acpRequestId;
    if (requestId && list.querySelector(`[data-acp-request-id="${CSS.escape(requestId)}"]`)) return;
    list.appendChild(card);
    this.updateAcpAttentionState();
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
      const selectedLabel = [...card.querySelectorAll(".agent-ask-option")]
        .find((node) => node.dataset.optionId === optionId)
        ?.querySelector(".agent-ask-option-label")?.textContent || optionId;
      card.querySelectorAll(".agent-ask-option").forEach((node) => {
        node.classList.toggle("is-selected", chosen.has(node.dataset.optionId));
      });
      const answerEl = element("div", "agent-ask-answer", `Decision: ${selectedLabel}`);
      card.querySelector(".agent-ask-body")?.appendChild(answerEl);
      card.remove();
      this.updateAcpAttentionState();
      const nextAction = document.querySelector("#agent-attention-list .agent-ask-card.is-pending button, #agent-input");
      nextAction?.focus({ preventScroll: true });
    } catch (error) {
      card.classList.remove("is-submitting");
      this.notify(error instanceof Error ? error.message : "Could not answer permission", "error");
    }
  }

  updateAcpAttentionState() {
    const stack = $("#agent-attention-stack");
    const list = $("#agent-attention-list");
    const count = $("#agent-attention-count");
    const title = stack?.querySelector(".agent-attention-title");
    const copy = stack?.querySelector(".agent-attention-copy");
    if (!stack || !list) return;
    const pending = list.querySelectorAll(".agent-ask-card.is-pending").length;
    if (count) count.textContent = String(pending);
    if (count) count.hidden = pending === 0;
    if (title) title.textContent = pending > 0 ? "Action required" : "Decisions recorded";
    if (copy) copy.textContent = pending > 0
      ? "The subagent is waiting for your permission or answer."
      : "Your subagent permissions are recorded for this run.";
    stack.hidden = list.children.length === 0;
    stack.classList.toggle("has-pending", pending > 0);
  }

  clearAcpAttentionStack() {
    const stack = $("#agent-attention-stack");
    const list = $("#agent-attention-list");
    if (!stack || !list) return;
    list.textContent = "";
    stack.hidden = true;
    stack.classList.remove("has-pending");
  }

  createAcpAskCard(payload) {
    if (!payload?.requestId || !this.answerAcpAsk) return null;
    const traceId = payload.traceId || this.activeTraceId;
    // Prefer the ACP session conversationId from the event (subagent runs use
    // `subagent:<runId>`, not the parent chat id).
    const conversationId = payload.conversationId || this.conversation?.id;
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
      this.enhanceCodeFences(message, this.currentMessageIndex());
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
    // Kill any running async tool jobs for this conversation before deleting.
    if (this.toolJobStrip && this.toolJobStrip.conversationId === deletedId) {
      for (const job of this.toolJobStrip.jobs.values()) {
        if (job.status === "running") {
          try { await this.toolJobStrip.onKill(job.handleId); } catch { /* best-effort */ }
        }
      }
    }
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

  /**
   * B3: Tear down all listeners, observers, and subscriptions so the
   * controller can be garbage-collected when the agent view is closed.
   */
  destroy() {
    this.composerResizeObserver?.disconnect();
    this.composerResizeObserver = null;
    this.subagentLifecycle.dispose();
    this.disposeSubagentCardStream();
    this.liveStreamState = null;
    this.toolJobEventDisposer?.();
    this.toolJobEventDisposer = null;
    this.completionSteerer?.dispose();
    this.completionSteerer = null;
    this.log?.("info", "AgentConversationController destroyed");
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

/** Keep newlines so headings/lists still parse as markdown after a length cap. */
function truncateCardStreamMarkdown(text, max = 320) {
  const raw = String(text ?? "");
  if (raw.length <= max) return raw;
  return `${raw.slice(0, max)}…`;
}

/**
 * Collapse markdown markup into a single plain line for compact tool rows.
 * Headings/bold/code still showed raw `##` / `**` when applied via textContent.
 */
function stripMarkdownOneLine(text, max = 120) {
  const flat = String(text ?? "")
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/!\[[^\]]*]\([^)]*\)/g, " ")
    .replace(/\[([^\]]+)]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/(\*\*|__)(.*?)\1/g, "$2")
    .replace(/(\*|_)(.*?)\1/g, "$2")
    .replace(/~~(.*?)~~/g, "$1")
    .replace(/\s+/g, " ")
    .trim();
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

function downloadIcon() {
  return '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" aria-hidden="true"><path d="M12 3v11m0 0 4-4m-4 4-4-4M5 20h14" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>';
}

function formatTime(timestamp) {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
