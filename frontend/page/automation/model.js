const STATUS_LABELS = Object.freeze({
  runnable: 'Runnable',
  enabled: 'Enabled',
  disabled: 'Paused',
  blocked: 'Blocked',
  invalid: 'Invalid',
  running: 'Running',
  queued: 'Queued',
  waiting: 'Waiting',
  completed: 'Completed',
  success: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
  skipped: 'Skipped',
});

export function workflowStatus(workflow = {}) {
  const availability = String(workflow.availability || '').toLowerCase();
  if (availability === 'invalid' || availability === 'blocked') return availability;
  return workflow.enabled ? 'enabled' : 'disabled';
}

export function statusLabel(value) {
  const status = String(value || '').trim().toLowerCase();
  return STATUS_LABELS[status] || (status ? status[0].toUpperCase() + status.slice(1) : 'Unknown');
}

export function canRunWorkflow(workflow = {}) {
  return !['invalid', 'blocked'].includes(String(workflow.availability || '').toLowerCase());
}

export function canEnableWorkflow(workflow = {}) {
  return workflow.enabled || String(workflow.availability || '').toLowerCase() === 'runnable';
}

export function isActiveRun(run = {}) {
  return ['running', 'queued'].includes(String(run.status || '').toLowerCase());
}

export function canCancelRun(run = {}) {
  return ['running', 'queued', 'waiting'].includes(String(run.status || '').toLowerCase());
}

// Steer forwards operator instructions into the run's live agent step. The
// backend resolves the conversation from a running step that carries output
// (contracts.AutomationRunSteerRequest), so the control only makes sense while
// such a step is visible — same gate as the production view.
export function canSteerRun(run = {}) {
  const status = String(run.status || '').toLowerCase();
  if (!canCancelRun({ status })) return false;
  return (Array.isArray(run.jobs) ? run.jobs : []).some((job) =>
    (Array.isArray(job.steps) ? job.steps : []).some((step) =>
      String(step.status || '').toLowerCase() === 'running' && step.output !== undefined));
}

export function triggerLabel(trigger = {}) {
  const family = String(trigger.family || trigger.kind || '').trim().toLowerCase();
  if (family === 'manual') return 'Manual';
  if (family === 'once') return `Once · ${formatDateTime(trigger.at)}`;
  if (family === 'every') {
    const cadence = trigger.cron ? `Cron ${trigger.cron}` : trigger.interval ? `Every ${trigger.interval}` : 'Recurring';
    return trigger.timezone ? `${cadence} · ${trigger.timezone}` : cadence;
  }
  if (family === 'when' || family === 'event') return trigger.event ? `On ${trigger.event}` : 'On event';
  return family ? statusLabel(family) : 'Manual';
}

export function workflowTriggerSummary(workflow = {}) {
  const triggers = Array.isArray(workflow.triggers) ? workflow.triggers : [];
  return triggers.length ? triggers.map(triggerLabel).join(' · ') : 'Manual trigger';
}

export function formatDateTime(value) {
  if (!value) return 'Time not recorded';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  }).format(date);
}

export function relativeTime(value, now = Date.now()) {
  const date = new Date(value);
  if (!value || Number.isNaN(date.getTime())) return 'Time not set';
  const minutes = Math.round((date.getTime() - now) / 60_000);
  if (Math.abs(minutes) < 1) return 'now';
  const unit = Math.abs(minutes) < 60 ? 'minute' : Math.abs(minutes) < 1440 ? 'hour' : 'day';
  const amount = unit === 'minute' ? minutes : unit === 'hour' ? Math.round(minutes / 60) : Math.round(minutes / 1440);
  return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(amount, unit);
}

export function countAutomationStatus(workflows = [], runs = []) {
  const runnable = workflows.filter((workflow) => String(workflow.availability || 'runnable').toLowerCase() === 'runnable').length;
  const needsAttention = workflows.filter((workflow) => ['blocked', 'invalid'].includes(workflowStatus(workflow))).length;
  const active = runs.filter(isActiveRun).length;
  const waiting = runs.filter((run) => String(run.status || '').toLowerCase() === 'waiting').length;
  return { runnable, needsAttention, active, waiting };
}
