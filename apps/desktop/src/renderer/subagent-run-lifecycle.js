// SubagentRunLifecycle — owns the 6 subagent run state fields and their
// transitions. The controller delegates lifecycle changes here so the
// order-sensitive dispose/null logic lives in one place.
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
    this.activeRun = null;
    this.streamState = null;
    this.streamDisposer = null;
    this.cardStream = null;
    this.eventDisposer = null;
    this.ownerConversationId = null;
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
    this.streamDisposer?.();
    this.streamDisposer = null;
    this.cardStream = null;
    this.streamState = null;
    this.activeRun = null;
    this.ownerConversationId = null;
  }

  isViewingOwner(conversationId) {
    if (!this.ownerConversationId) return true;
    return Boolean(conversationId && conversationId === this.ownerConversationId);
  }
}
