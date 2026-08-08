// Completion steering — when a background async tool job ends and the
// conversation has no active turn, auto-start a follow-up turn with a
// synthetic system message containing the job completion summary.
//
// Coalesces multiple job completions in quick succession (debounce 500ms)
// so the agent gets one wake with all completed jobs, not N separate turns.

const STEER_DEBOUNCE_MS = 500;
const MAX_JOBS_PER_WAKE = 10;

export class CompletionSteerer {
  constructor({ conversationId, isIdle, startTurn, log }) {
    this.conversationId = conversationId;
    this.isIdle = isIdle ?? (() => true);
    this.startTurn = startTurn;
    this.log = log ?? (() => {});
    this.pending = [];
    this.timer = null;
    this.enabled = true;
  }

  /** Called when a tool_job_ended event arrives. */
  onJobEnded(payload) {
    if (!this.enabled) return;
    if (payload?.conversationId !== this.conversationId) return;
    this.pending.push({
      handleId: payload.handleId,
      toolName: payload.toolName ?? "(unknown)",
      ok: payload.ok,
      reason: payload.reason,
      ...(payload.error ? { error: payload.error } : {}),
      ...(payload.output !== undefined ? { output: payload.output } : {}),
    });
    this.scheduleWake();
  }

  scheduleWake() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = null;
      this.fireWake();
    }, STEER_DEBOUNCE_MS);
  }

  fireWake() {
    if (this.pending.length === 0) return;
    if (!this.isIdle()) {
      // Active turn, unsent composer draft, or IME composition — do not steal
      // the textarea. Drop the wake; jobs remain visible on the job strip (#69).
      this.log("completion steer skipped — conversation not idle (active turn or composer busy)");
      this.pending = [];
      return;
    }
    const jobs = this.pending.slice(0, MAX_JOBS_PER_WAKE);
    this.pending = [];
    const summary = formatJobSummary(jobs);
    this.log(`completion steer — auto-starting follow-up turn with ${jobs.length} job(s)`);
    this.startTurn(summary).catch((err) => {
      this.log(`completion steer failed: ${err?.message ?? err}`);
    });
  }

  dispose() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    this.pending = [];
  }
}

function formatJobSummary(jobs) {
  const lines = ["[Background job completed]"];
  for (const job of jobs) {
    const status = job.ok ? "ok" : job.reason ?? "failed";
    const parts = [`- ${job.toolName} (${job.handleId.slice(0, 8)}): ${status}`];
    if (job.error) parts.push(`  Error: ${String(job.error).slice(0, 500)}`);
    if (job.output !== undefined && job.output !== null) {
      const out = typeof job.output === "string" ? job.output : JSON.stringify(job.output);
      parts.push(`  Output: ${out.slice(0, 1000)}`);
    }
    lines.push(parts.join("\n"));
  }
  lines.push("");
  lines.push("The background job(s) above finished. Check the result and continue the task if needed.");
  return lines.join("\n");
}
