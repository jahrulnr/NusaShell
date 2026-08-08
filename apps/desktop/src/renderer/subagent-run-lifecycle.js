// SubagentRunLifecycle — owns the per-run subagent state and transitions.
// The controller delegates lifecycle changes here so the order-sensitive
// dispose/null logic lives in one place without sharing state between runs.
//
// State machine (implicit phases):
//   idle → run_started → streaming → run_ended → sealed
//
// Transitions:
//   reset()           — clear everything (bindSubagentEvents / init)
//   rebindEvents(fn)  — dispose+resubscribe event disposer (renderThread)
//   startRun(fn)      — dispose old stream, bind new stream disposer (run_started)
//   endRun()          — dispose stream, clear stream state + owner (run_ended)
//   dispose()         — dispose everything (destroy)
//
// The controller still reads streamState / activeRun / cardStream / ownerConversationId
// for UI rendering via the getters — only the transition logic is centralized here.

export class SubagentRunLifecycle {
  constructor(log) {
    this.log = log;
    this.reset();
  }

  reset() {
    this.states = new Map();
    this.selectedRunId = null;
    this.legacyState = this.createState();
    this.eventDisposer = null;
  }

  createState() {
    return {
      activeRun: null,
      streamState: null,
      streamDisposer: null,
      cardStream: null,
      ownerConversationId: null,
    };
  }

  currentState() {
    if (!this.selectedRunId) return this.legacyState;
    let state = this.states.get(this.selectedRunId);
    if (!state) {
      state = this.createState();
      this.states.set(this.selectedRunId, state);
    }
    return state;
  }

  /** Select the run whose state is currently projected into the drawer. */
  selectRun(runId) {
    this.selectedRunId = runId || null;
    if (this.selectedRunId && !this.states.has(this.selectedRunId)) {
      const legacyRunId = this.legacyState.streamState?.runId || this.legacyState.activeRun?.runId;
      if (legacyRunId === this.selectedRunId) {
        this.states.set(this.selectedRunId, this.legacyState);
        this.legacyState = this.createState();
      }
    }
    return this.currentState();
  }

  get activeRun() { return this.currentState().activeRun; }
  set activeRun(value) {
    // Preserve the old direct-assignment API used by tests and callers that
    // create one run without explicitly selecting it first.
    if (!this.selectedRunId && value?.runId) this.selectRun(value.runId);
    this.currentState().activeRun = value;
  }
  get streamState() { return this.currentState().streamState; }
  set streamState(value) { this.currentState().streamState = value; }
  get streamDisposer() { return this.currentState().streamDisposer; }
  set streamDisposer(value) { this.currentState().streamDisposer = value; }
  get cardStream() { return this.currentState().cardStream; }
  set cardStream(value) { this.currentState().cardStream = value; }
  get ownerConversationId() { return this.currentState().ownerConversationId; }
  set ownerConversationId(value) { this.currentState().ownerConversationId = value; }

  forEachState(callback) {
    callback(this.legacyState);
    for (const state of this.states.values()) callback(state);
  }

  rebindEvents(subscribeFn) {
    this.eventDisposer?.();
    this.eventDisposer = subscribeFn();
    const convId = this._conversationId ?? "(no conversation)";
    if (this.eventDisposer) {
      this.log?.("info", `subagent events rebound for conversation=${convId}`);
    } else {
      this.log?.("error", `subagentEventDisposer is null after rebind for conversation=${convId} — subagent lifecycle events will be dropped`);
    }
  }

  startRun(bindStreamFn) {
    this.streamDisposer?.();
    this.streamDisposer = bindStreamFn();
  }

  /** Dispose the live stream disposer (call at top of run_ended, before sealing). */
  endRunDisposeStream() {
    this.streamDisposer?.();
    this.streamDisposer = null;
  }

  /** Clear stream state + ownership (call after sealing/snapshotting). */
  endRunClearState() {
    this.streamState = null;
    this.ownerConversationId = null;
  }

  dispose() {
    this.eventDisposer?.();
    this.eventDisposer = null;
    this.forEachState((state) => {
      state.streamDisposer?.();
      state.streamDisposer = null;
      state.cardStream = null;
      state.streamState = null;
      state.activeRun = null;
      state.ownerConversationId = null;
    });
    this.states.clear();
    this.selectedRunId = null;
    this.legacyState = this.createState();
  }

  isViewingOwner(conversationId) {
    if (!this.ownerConversationId) return true;
    return Boolean(conversationId && conversationId === this.ownerConversationId);
  }
}
