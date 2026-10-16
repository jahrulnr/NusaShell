import { on as defaultOn, rpc as defaultRPC } from '../../js/rpc.js';
import { confirmDialog, debounce, dialog, el, toast } from '../../js/ui.js';
import {
  canCancelRun,
  canEnableWorkflow,
  canRunWorkflow,
  canSteerRun,
  countAutomationStatus,
  formatDateTime,
  relativeTime,
  statusLabel,
  triggerLabel,
  workflowStatus,
  workflowTriggerSummary,
} from './model.js';

const AUTOMATION_EVENTS = [
  'automation.run.created',
  'automation.run.started',
  'automation.run.completed',
  'automation.run.failed',
  'automation.run.cancelled',
  'automation.run.waiting',
  'automation.run.blocked',
  'automation.job.queued',
  'automation.job.started',
  'automation.job.completed',
  'automation.job.failed',
  'automation.job.cancelled',
  'automation.job.skipped',
  'automation.step.started',
  'automation.step.output',
  'automation.step.completed',
  'automation.step.failed',
  'automation.event',
];

const WORKFLOW_EXAMPLE = `name: morning-brief\ntriggers:\n  manual: true\njobs:\n  prepare:\n    steps:\n      - run: echo "Hello from NusaShell"\n`;

function resultArray(result, key) {
  return Array.isArray(result?.[key]) ? result[key] : [];
}

function shortValue(value, limit = 12000) {
  const text = String(value || '');
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}\n\n… output shortened …`;
}

function detailText(value) {
  if (value?.run) return value.run;
  return value;
}

export function createAutomationPage(root = document, { rpcClient = defaultRPC, onEvent = defaultOn } = {}) {
  const state = {
    workflows: [],
    runs: [],
    schedules: [],
    events: [],
    mode: 'workflows',
    query: '',
    selectedKind: '',
    selectedID: '',
    runDetail: null,
    runDetailID: '',
    runDetailLoading: false,
    editorOpen: false,
    validation: null,
    loading: false,
    loaded: false,
    error: null,
    busyAction: '',
  };
  let initialized = false;
  let destroyed = false;
  let active = false;
  let refreshVersion = 0;
  let detailVersion = 0;
  let refreshTimer = 0;
  let mobileListScroll = 0;
  let paneVersion = 0;
  const unsubscribers = [];
  const removeListeners = [];

  const find = (id) => root.querySelector(`#${id}`);
  const all = (selector) => [...root.querySelectorAll(selector)];
  const selectedWorkflow = () => state.workflows.find((workflow) => workflow.id === state.selectedID) || null;
  const selectedRun = () => state.runs.find((run) => run.id === state.selectedID) || null;
  const selectedEvent = () => state.events.find((entry) => entry.id === state.selectedID) || null;
  const pageNode = () => root.nodeType === 1 ? root : find('automation-view');
  const isMobileHistory = () => window.innerWidth <= 720 && state.mode !== 'workflows';

  function setMobilePane(pane, { restore = true } = {}) {
    const page = pageNode();
    if (!page) return;
    if (pane === 'detail' && page.dataset.mobilePane !== 'detail') mobileListScroll = page.scrollTop;
    page.dataset.mobilePane = pane;
    const token = ++paneVersion;
    if (!isMobileHistory()) return;
    page.scrollTop = pane === 'list' && restore ? mobileListScroll : 0;
    requestAnimationFrame(() => {
      if (destroyed || token !== paneVersion || !isMobileHistory()) return;
      page.scrollTop = pane === 'list' && restore ? mobileListScroll : 0;
      const target = pane === 'detail'
        ? find('automation-mobile-back')
        : root.querySelector('.automation-record[aria-current="true"]');
      target?.focus({ preventScroll: true });
    });
  }

  function listen(node, type, handler, options) {
    if (!node) return;
    node.addEventListener(type, handler, options);
    removeListeners.push(() => node.removeEventListener(type, handler, options));
  }

  function syncState(message, tone = 'idle') {
    const node = find('automation-sync-state');
    if (!node) return;
    node.textContent = message;
    node.dataset.state = tone;
  }

  function showError(error) {
    state.error = error || null;
    const panel = find('automation-error');
    const message = find('automation-error-message');
    if (panel) panel.hidden = !state.error;
    if (message) message.textContent = state.error?.message || '';
  }

  function visibleRows() {
    const records = state.mode === 'events' ? state.events : state.mode === 'workflows' ? state.workflows : state.runs;
    const query = state.query.trim().toLocaleLowerCase();
    if (!query) return records;
    return records.filter((record) => [
      record.name,
      record.id,
      record.workflow_id,
      record.status,
      record.availability,
      record.blocked_reason,
      record.type,
      record.subject,
      record.source,
      state.mode === 'workflows' ? workflowTriggerSummary(record) : record.requested_by,
    ].filter(Boolean).join(' ').toLocaleLowerCase().includes(query));
  }

  function updateOverview() {
    const counts = countAutomationStatus(state.workflows, state.runs);
    const values = {
      'automation-stat-runnable': counts.runnable,
      'automation-stat-blocked': counts.needsAttention,
      'automation-stat-active': counts.active,
      'automation-stat-waiting': counts.waiting,
    };
    for (const [id, value] of Object.entries(values)) {
      const node = find(id);
      if (node) node.textContent = String(value);
    }
  }

  function chooseRecord(kind, record, { loadDetail = true, openMobile = true } = {}) {
    state.editorOpen = false;
    state.selectedKind = kind;
    state.selectedID = record?.id || '';
    state.runDetail = null;
    state.runDetailID = '';
    state.runDetailLoading = false;
    if (openMobile && isMobileHistory()) setMobilePane('detail');
    render();
    if (kind === 'run' && loadDetail && record?.id) void loadRunDetail(record.id);
  }

  function reconcileSelection() {
    if (state.editorOpen) return;
    if (state.selectedKind === 'workflow' && selectedWorkflow()) return;
    if (state.selectedKind === 'run' && selectedRun()) return;
    if (state.selectedKind === 'event' && selectedEvent()) return;
    const firstWorkflow = state.workflows[0];
    const firstRun = state.runs[0];
    const firstEvent = state.events[0];
    if (state.mode === 'events') {
      if (firstEvent) {
        state.selectedKind = 'event';
        state.selectedID = firstEvent.id;
      } else {
        state.selectedKind = '';
        state.selectedID = '';
      }
      return;
    }
    if (state.mode === 'runs' && firstRun) {
      state.selectedKind = 'run';
      state.selectedID = firstRun.id;
      return;
    }
    if (firstWorkflow) {
      state.selectedKind = 'workflow';
      state.selectedID = firstWorkflow.id;
    } else if (firstRun) {
      state.selectedKind = 'run';
      state.selectedID = firstRun.id;
    } else {
      state.selectedKind = '';
      state.selectedID = '';
    }
  }

  function renderMode() {
    const page = pageNode();
    if (page) page.dataset.mode = state.mode;
    const heading = root.querySelector('.automation-rail-heading h2');
    if (heading) heading.textContent = state.mode === 'workflows' ? 'Browse' : state.mode === 'runs' ? 'Runs' : 'Events';
    const back = find('automation-mobile-back');
    if (back) back.textContent = `← Back to ${state.mode}`;
    for (const button of all('[data-automation-mode]')) {
      const pressed = button.dataset.automationMode === state.mode;
      button.setAttribute('aria-pressed', String(pressed));
    }
  }

  function renderList() {
    const list = find('automation-list');
    if (!list) return;
    const rows = visibleRows();
    list.replaceChildren();
    list.setAttribute('aria-busy', String(state.loading));
    const count = find('automation-list-count');
    if (count) count.textContent = String(rows.length);

    if (state.loading && !state.loaded) {
      const copy = state.mode === 'events' ? 'Loading events…' : state.mode === 'workflows' ? 'Loading workflows…' : 'Loading runs…';
      list.append(el('p', { class: 'automation-list-empty', role: 'status', text: copy }));
      return;
    }
    if (!rows.length) {
      const message = state.query
        ? 'No records match this filter.'
        : state.mode === 'workflows'
          ? 'No workflows are available yet.'
          : state.mode === 'events'
            ? 'No automation events have been recorded yet.'
            : 'Runs will appear here after a workflow starts.';
      list.append(el('p', { class: 'automation-list-empty', role: 'status', text: message }));
      return;
    }

    for (const record of rows) {
      const kind = state.mode === 'events' ? 'event' : state.mode === 'workflows' ? 'workflow' : 'run';
      const status = kind === 'event' ? 'event' : kind === 'workflow' ? workflowStatus(record) : String(record.status || 'unknown').toLowerCase();
      const title = kind === 'event' ? record.type || record.id : record.name || record.workflow_id || record.id;
      const meta = kind === 'event'
        ? [record.subject || record.source || '', record.time ? formatDateTime(record.time) : ''].filter(Boolean).join(' · ')
        : kind === 'workflow'
          ? workflowTriggerSummary(record)
          : [formatDateTime(record.created_at), record.workspace].filter(Boolean).join(' · ');
      const row = el('button', {
        class: `automation-record${record.id === state.selectedID && kind === state.selectedKind ? ' is-selected' : ''}`,
        type: 'button',
        role: 'listitem',
        dataset: { automationRecordId: record.id },
        'aria-current': record.id === state.selectedID && kind === state.selectedKind ? 'true' : 'false',
      }, [
        el('span', { class: `automation-record-mark is-${status}`, 'aria-hidden': 'true' }),
        el('span', { class: 'automation-record-copy' }, [
          el('strong', { class: 'automation-record-title', text: title }),
          el('span', { class: 'automation-record-meta', text: meta || statusLabel(status) }),
        ]),
        el('span', { class: `automation-status automation-status-${status}`, text: statusLabel(status) }),
      ]);
      row.addEventListener('click', () => chooseRecord(kind, record));
      list.append(row);
    }
  }

  function renderSchedules() {
    const list = find('automation-schedule-list');
    if (!list) return;
    list.replaceChildren();
    const schedules = [...state.schedules]
      .filter((schedule) => schedule.next_run_at)
      .sort((a, b) => new Date(a.next_run_at).getTime() - new Date(b.next_run_at).getTime())
      .slice(0, 3);
    if (!schedules.length) {
      list.append(el('p', { class: 'automation-list-empty', text: 'Schedules appear here when workflows are enabled.' }));
      return;
    }
    for (const schedule of schedules) {
      const workflow = state.workflows.find((candidate) => candidate.id === schedule.workflow_id);
      const button = el('button', {
        class: 'automation-schedule-row',
        type: 'button',
        title: formatDateTime(schedule.next_run_at),
        role: 'listitem',
      }, [
        el('span', { class: 'automation-schedule-time', text: relativeTime(schedule.next_run_at) }),
        el('span', { class: 'automation-schedule-name', text: workflow?.name || schedule.workflow_id }),
        el('span', { class: 'automation-schedule-kind', text: schedule.kind || 'scheduled' }),
      ]);
      button.disabled = !workflow;
      button.addEventListener('click', () => {
        if (!workflow) return;
        state.mode = 'workflows';
        state.query = '';
        const input = find('automation-search');
        if (input) input.value = '';
        chooseRecord('workflow', workflow);
      });
      list.append(button);
    }
  }

  function statusBadge(status) {
    const normalized = String(status || 'unknown').toLowerCase();
    return el('span', { class: `automation-status automation-status-${normalized}`, text: statusLabel(normalized) });
  }

  function renderInspector() {
    const empty = find('automation-inspector-empty');
    const workflowDetail = find('automation-workflow-detail');
    const runDetail = find('automation-run-detail');
    const editor = find('automation-editor');
    const showEditor = state.editorOpen;
    const workflow = selectedWorkflow();
    const run = selectedRun();
    const event = selectedEvent();
    const showWorkflow = !showEditor && state.selectedKind === 'workflow' && Boolean(workflow);
    const showRun = !showEditor && state.selectedKind === 'run' && Boolean(run);
    const showEvent = !showEditor && state.selectedKind === 'event' && Boolean(event);
    if (empty) empty.hidden = showEditor || showWorkflow || showRun || showEvent;
    if (workflowDetail) workflowDetail.hidden = !showWorkflow;
    if (runDetail) runDetail.hidden = !(showRun || showEvent);
    if (editor) editor.hidden = !showEditor;
    if (showEditor) return;
    if (showWorkflow) renderWorkflowDetail(workflow, workflowDetail);
    else if (showRun) renderRunDetail(run, runDetail);
    else if (showEvent) renderEventDetail(event, runDetail);
  }

  function renderEventDetail(event, target) {
    target.replaceChildren();
    target.append(el('header', { class: 'automation-detail-header' }, [
      el('div', { class: 'automation-detail-title' }, [
        el('span', { class: 'automation-section-kicker', text: 'TRIGGER SIGNAL' }),
        el('h2', { text: event.type || event.id }),
        el('p', { class: 'automation-detail-id', text: event.id }),
      ]),
      el('div', { class: 'automation-detail-heading-actions' }, [statusBadge('event')]),
    ]));
    target.append(el('div', { class: 'automation-meta-strip' }, [
      el('div', {}, [el('span', { text: 'Subject' }), el('strong', { text: event.subject || 'Not set' })]),
      el('div', {}, [el('span', { text: 'Source' }), el('strong', { text: event.source || 'Not set' })]),
      el('div', {}, [el('span', { text: 'Time' }), el('strong', { text: event.time ? formatDateTime(event.time) : 'Not recorded' })]),
    ]));
    const attributes = event.attributes && typeof event.attributes === 'object' ? event.attributes : {};
    if (Object.keys(attributes).length) {
      target.append(el('section', { class: 'automation-detail-section' }, [
        el('header', { class: 'automation-section-heading' }, [
          el('div', {}, [el('span', { class: 'automation-section-kicker', text: 'PAYLOAD' }), el('h3', { text: 'Attributes' })]),
        ]),
        el('pre', { class: 'automation-yaml', text: JSON.stringify(attributes, null, 2) }),
      ]));
    }
  }

  function renderWorkflowDetail(workflow, target) {
    target.replaceChildren();
    const status = workflowStatus(workflow);
    const disabledReason = workflow.blocked_reason || (status === 'invalid' ? 'This definition is invalid. Review its source before enabling or running it.' : '');
    const header = el('header', { class: 'automation-detail-header' }, [
      el('div', { class: 'automation-detail-title' }, [
        el('span', { class: 'automation-section-kicker', text: 'WORKFLOW' }),
        el('h2', { text: workflow.name || workflow.id }),
        el('p', { class: 'automation-detail-id', text: workflow.id }),
      ]),
      el('div', { class: 'automation-detail-heading-actions' }, [
        statusBadge(status),
        el('button', {
          class: 'automation-icon-button',
          type: 'button',
          title: workflow.enabled ? 'Pause workflow' : 'Enable workflow',
          'aria-label': workflow.enabled ? 'Pause workflow' : 'Enable workflow',
          disabled: Boolean(state.busyAction) || !canEnableWorkflow(workflow) && !workflow.enabled,
        }, el('i', { 'data-lucide': workflow.enabled ? 'pause' : 'play', 'aria-hidden': 'true' })),
      ]),
    ]);
    const toggle = header.querySelector('button');
    toggle?.addEventListener('click', () => void setWorkflowEnabled(workflow));
    target.append(header);

    if (disabledReason) {
      target.append(el('div', { class: `automation-callout is-${status}`, role: 'status' }, [
        el('i', { 'data-lucide': status === 'invalid' ? 'triangle-alert' : 'circle-help', 'aria-hidden': 'true' }),
        el('span', { text: disabledReason }),
      ]));
    }

    const meta = el('div', { class: 'automation-meta-strip' }, [
      el('div', {}, [el('span', { text: 'Trigger' }), el('strong', { text: workflowTriggerSummary(workflow) })]),
      el('div', {}, [el('span', { text: 'Updated' }), el('strong', { text: formatDateTime(workflow.updated_at) })]),
      el('div', {}, [el('span', { text: 'Jobs' }), el('strong', { text: String(workflow.jobs?.length || 0) })]),
    ]);
    target.append(meta);

    const actions = el('div', { class: 'automation-primary-actions' });
    const runButton = el('button', {
      class: 'automation-primary-button',
      type: 'button',
      disabled: Boolean(state.busyAction) || !canRunWorkflow(workflow),
    }, [el('i', { 'data-lucide': 'play', 'aria-hidden': 'true' }), state.busyAction === `run:${workflow.id}` ? 'Starting…' : 'Run now']);
    runButton.addEventListener('click', () => void startWorkflow(workflow));
    actions.append(runButton);
    if (status === 'blocked') {
      const enableProvider = el('button', {
        class: 'automation-quiet-button',
        type: 'button',
        disabled: Boolean(state.busyAction),
      }, state.busyAction === `provider:${workflow.id}` ? 'Checking…' : 'Enable provider');
      enableProvider.addEventListener('click', () => void enableBlockedProvider(workflow));
      actions.append(enableProvider);
    }
    const deleteButton = el('button', {
      class: 'automation-danger-button',
      type: 'button',
      disabled: Boolean(state.busyAction),
    }, [el('i', { 'data-lucide': 'trash-2', 'aria-hidden': 'true' }), state.busyAction === `delete:${workflow.id}` ? 'Deleting…' : 'Delete']);
    deleteButton.addEventListener('click', () => void deleteWorkflow(workflow));
    actions.append(deleteButton);
    if (workflow.capabilities?.length) {
      const count = workflow.capabilities.length;
      actions.append(el('span', { class: 'automation-capability-summary', text: `${count} required ${count === 1 ? 'capability' : 'capabilities'}` }));
    }
    target.append(actions);

    const triggers = Array.isArray(workflow.triggers) && workflow.triggers.length
      ? workflow.triggers
      : [{ family: 'manual' }];
    const triggerSection = el('section', { class: 'automation-detail-section', 'aria-labelledby': 'automation-trigger-heading' }, [
      el('header', { class: 'automation-section-heading' }, [
        el('div', {}, [el('span', { class: 'automation-section-kicker', text: 'WHEN IT STARTS' }), el('h3', { id: 'automation-trigger-heading', text: 'Triggers' })]),
        el('span', { class: 'automation-section-count', text: String(triggers.length) }),
      ]),
      el('div', { class: 'automation-trigger-list' }, triggers.map((trigger) => el('div', { class: 'automation-trigger' }, [
        el('i', { 'data-lucide': trigger.family === 'when' || trigger.family === 'event' ? 'radio' : trigger.family === 'manual' ? 'mouse-pointer-2' : 'clock-3', 'aria-hidden': 'true' }),
        el('span', { text: triggerLabel(trigger) }),
      ]))),
    ]);
    target.append(triggerSection);

    const jobs = Array.isArray(workflow.jobs) ? workflow.jobs : [];
    const nameByID = new Map(jobs.map((job) => [job.id, job.name || job.id]));
    const jobsSection = el('section', { class: 'automation-detail-section', 'aria-labelledby': 'automation-jobs-heading' }, [
      el('header', { class: 'automation-section-heading' }, [
        el('div', {}, [el('span', { class: 'automation-section-kicker', text: 'DEPENDENCY MAP' }), el('h3', { id: 'automation-jobs-heading', text: 'Execution plan' })]),
        el('span', { class: 'automation-section-count', text: `${jobs.length} job${jobs.length === 1 ? '' : 's'}` }),
      ]),
    ]);
    if (!jobs.length) {
      jobsSection.append(el('p', { class: 'automation-list-empty', text: 'This workflow has no jobs.' }));
    } else {
      const jobList = el('ol', { class: 'automation-job-list' });
      jobs.forEach((job, index) => {
        const steps = Array.isArray(job.steps) ? job.steps : [];
        const dependencies = (job.needs || []).map((id) => nameByID.get(id) || id);
        const article = el('li', { class: 'automation-job' }, [
          el('span', { class: 'automation-job-index', text: String(index + 1).padStart(2, '0'), 'aria-hidden': 'true' }),
          el('div', { class: 'automation-job-content' }, [
            el('header', { class: 'automation-job-heading' }, [
              el('div', {}, [el('h4', { text: job.name || job.id }), el('code', { text: job.id })]),
              el('span', { class: 'automation-step-count', text: `${steps.length} step${steps.length === 1 ? '' : 's'}` }),
            ]),
            dependencies.length ? el('p', { class: 'automation-job-needs', text: `Needs ${dependencies.join(' · ')}` }) : el('p', { class: 'automation-job-needs is-root', text: 'Can start immediately' }),
            el('ul', { class: 'automation-step-list' }, steps.map((step) => {
              const label = step.run || (step.uses ? `uses ${step.uses}` : step.name || step.id || 'Step');
              return el('li', {}, [el('span', { class: 'automation-step-dot', 'aria-hidden': 'true' }), el('code', { text: label })]);
            })),
          ]),
        ]);
        jobList.append(article);
      });
      jobsSection.append(jobList);
    }
    target.append(jobsSection);
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  }

  function renderRunDetail(summaryRun, target) {
    target.replaceChildren();
    const run = state.runDetailID === summaryRun.id && state.runDetail ? state.runDetail : summaryRun;
    const status = String(run.status || 'unknown').toLowerCase();
    const workflow = state.workflows.find((item) => item.id === run.workflow_id);
    const header = el('header', { class: 'automation-detail-header' }, [
      el('div', { class: 'automation-detail-title' }, [
        el('span', { class: 'automation-section-kicker', text: 'EXECUTION' }),
        el('h2', { text: run.name || workflow?.name || run.workflow_id || run.id }),
        el('p', { class: 'automation-detail-id', text: run.id }),
      ]),
      el('div', { class: 'automation-detail-heading-actions' }, [statusBadge(status)]),
    ]);
    target.append(header);

    const callouts = [];
    if (run.blocked_reason) callouts.push(el('div', { class: 'automation-callout is-blocked', role: 'status' }, [el('i', { 'data-lucide': 'circle-help', 'aria-hidden': 'true' }), el('span', { text: run.blocked_reason })]));
    if (status === 'waiting') callouts.push(el('div', { class: 'automation-callout is-waiting', role: 'status' }, [
      el('i', { 'data-lucide': 'clock-3', 'aria-hidden': 'true' }),
      el('span', { text: run.wake_at ? `Parked until ${formatDateTime(run.wake_at)} (${relativeTime(run.wake_at)}).` : 'Parked until a matching event arrives.' }),
    ]));
    if (callouts.length) target.append(...callouts);

    const meta = el('div', { class: 'automation-meta-strip' }, [
      el('div', {}, [el('span', { text: 'Started' }), el('strong', { text: formatDateTime(run.created_at) })]),
      el('div', {}, [el('span', { text: 'Requested by' }), el('strong', { text: run.requested_by || 'system' })]),
      el('div', {}, [el('span', { text: 'Workspace' }), el('strong', { text: run.workspace || 'Not set' })]),
    ]);
    target.append(meta);

    const summary = run.summary || {};
    const counts = el('div', { class: 'automation-run-summary', 'aria-label': 'Job summary' }, [
      ['success', summary.success || 0], ['running', summary.running || 0], ['failed', summary.failed || 0], ['waiting', summary.waiting || 0],
    ].map(([key, value]) => el('div', { class: `automation-run-count is-${key}` }, [el('strong', { text: String(value) }), el('span', { text: statusLabel(key) })])));
    target.append(counts);

    const actions = el('div', { class: 'automation-primary-actions' });
    if (canSteerRun(run)) {
      const steer = el('button', { class: 'automation-quiet-button', type: 'button', disabled: Boolean(state.busyAction) }, [
        el('i', { 'data-lucide': 'navigation', 'aria-hidden': 'true' }),
        state.busyAction === `steer:${run.id}` ? 'Sending…' : 'Steer',
      ]);
      steer.addEventListener('click', () => void steerRun(run, steer));
      actions.append(steer);
    }
    if (canCancelRun(run)) {
      const cancel = el('button', { class: 'automation-danger-button', type: 'button', disabled: Boolean(state.busyAction) }, [
        el('i', { 'data-lucide': 'square', 'aria-hidden': 'true' }),
        state.busyAction === `cancel:${run.id}` ? 'Cancelling…' : 'Cancel run',
      ]);
      cancel.addEventListener('click', () => void cancelRun(run));
      actions.append(cancel);
    }
    if (status === 'failed') {
      const retry = el('button', { class: 'automation-primary-button', type: 'button', disabled: Boolean(state.busyAction) }, [
        el('i', { 'data-lucide': 'rotate-ccw', 'aria-hidden': 'true' }),
        state.busyAction === `retry:${run.id}` ? 'Retrying…' : 'Retry run',
      ]);
      retry.addEventListener('click', () => void retryRun(run));
      actions.append(retry);
    }
    if (actions.childNodes.length) target.append(actions);

    const jobs = Array.isArray(run.jobs) ? run.jobs : [];
    const section = el('section', { class: 'automation-detail-section', 'aria-labelledby': 'automation-run-jobs-heading' }, [
      el('header', { class: 'automation-section-heading' }, [
        el('div', {}, [el('span', { class: 'automation-section-kicker', text: 'STEP OUTPUT' }), el('h3', { id: 'automation-run-jobs-heading', text: 'Jobs and results' })]),
        el('span', { class: 'automation-section-count', text: `${jobs.length} job${jobs.length === 1 ? '' : 's'}` }),
      ]),
    ]);
    if (state.runDetailLoading) section.append(el('p', { class: 'automation-list-empty', role: 'status', text: 'Refreshing the latest run snapshot…' }));
    else if (!jobs.length) section.append(el('p', { class: 'automation-list-empty', text: 'No job results are available for this run.' }));
    for (const job of jobs) {
      section.append(renderRunJob(job));
    }
    target.append(section);
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  }

  function renderRunJob(job) {
    const steps = Array.isArray(job.steps) ? job.steps : [];
    const details = el('details', { class: 'automation-run-job' });
    const summary = el('summary', { class: 'automation-run-job-summary' }, [
      el('span', { class: `automation-record-mark is-${String(job.status || 'unknown').toLowerCase()}`, 'aria-hidden': 'true' }),
      el('span', { class: 'automation-run-job-name' }, [el('strong', { text: job.name || job.id }), el('code', { text: job.id })]),
      statusBadge(job.status),
    ]);
    details.append(summary);
    if (job.error) details.append(el('p', { class: 'automation-run-error', text: job.error }));
    if (!steps.length) details.append(el('p', { class: 'automation-run-output-empty', text: 'No step output recorded.' }));
    for (const step of steps) {
      const stepNode = el('article', { class: 'automation-run-step' }, [
        el('header', {}, [
          el('strong', { text: step.name || step.id || 'Step' }),
          statusBadge(step.status),
        ]),
      ]);
      if (step.error) stepNode.append(el('p', { class: 'automation-run-error', text: step.error }));
      if (step.output) stepNode.append(el('pre', { class: 'automation-run-output', text: shortValue(step.output) }));
      if (step.started_at) stepNode.append(el('time', { class: 'automation-run-time', dateTime: step.started_at, text: formatDateTime(step.started_at) }));
      details.append(stepNode);
    }
    return details;
  }

  function renderValidation(validation, error = '') {
    const node = find('automation-validation');
    if (!node) return;
    node.replaceChildren();
    node.hidden = !validation && !error;
    node.dataset.state = error || validation?.verdict === 'INVALID' ? 'error' : 'ready';
    if (error) {
      node.append(el('strong', { text: 'Could not check this definition' }), el('p', { text: error }));
      return;
    }
    const issues = Array.isArray(validation?.issues) ? validation.issues : [];
    node.append(el('strong', { text: validation?.verdict === 'INVALID' ? 'Definition needs changes' : 'Definition checked' }));
    if (issues.length) {
      const list = el('ul', { class: 'automation-issue-list' });
      for (const issue of issues) {
        const location = [issue.path, issue.code].filter(Boolean).join(' · ');
        list.append(el('li', { class: `is-${String(issue.level || 'info').toLowerCase()}` }, [
          location ? el('code', { text: location }) : null,
          el('span', { text: issue.message || 'Review this item.' }),
        ]));
      }
      node.append(list);
    } else {
      node.append(el('p', { text: `${validation?.syntax || 'Syntax checked'} · ${validation?.capabilities || 'Capabilities checked'} · ${validation?.providers || 'Providers checked'}` }));
    }
  }

  async function validateDefinition() {
    const yaml = find('automation-editor-yaml')?.value.trim() || '';
    if (!yaml) {
      renderValidation(null, 'Add a workflow definition before checking it.');
      return null;
    }
    const button = find('automation-editor-check');
    if (button) {
      button.disabled = true;
      button.textContent = 'Checking…';
    }
    try {
      state.validation = await rpcClient('automation.validate', { yaml });
      renderValidation(state.validation);
      return state.validation;
    } catch (error) {
      renderValidation(null, error.message || 'Validation failed.');
      return null;
    } finally {
      if (button) {
        button.disabled = false;
        button.textContent = 'Check YAML';
      }
    }
  }

  function openEditor() {
    state.editorOpen = true;
    state.selectedKind = '';
    state.selectedID = '';
    state.validation = null;
    render();
    const name = find('automation-editor-name');
    const yaml = find('automation-editor-yaml');
    if (name && !name.value) name.value = 'morning-brief';
    if (yaml && !yaml.value) yaml.value = WORKFLOW_EXAMPLE;
    renderValidation(null);
    name?.focus();
  }

  function closeEditor() {
    state.editorOpen = false;
    state.validation = null;
    const name = find('automation-editor-name');
    const yaml = find('automation-editor-yaml');
    if (name) name.value = '';
    if (yaml) yaml.value = '';
    reconcileSelection();
    render();
  }

  async function saveWorkflow(event) {
    event.preventDefault();
    if (state.busyAction) return;
    const name = find('automation-editor-name')?.value.trim() || '';
    const yaml = find('automation-editor-yaml')?.value.trim() || '';
    if (!name || !yaml) {
      renderValidation(null, 'Add a name and workflow definition before saving.');
      return;
    }
    state.busyAction = 'save';
    const button = find('automation-editor-save');
    if (button) {
      button.disabled = true;
      button.textContent = 'Checking…';
    }
    try {
      const validation = await rpcClient('automation.validate', { yaml });
      state.validation = validation;
      renderValidation(validation);
      if (validation?.verdict === 'INVALID') return;
      if (button) button.textContent = 'Saving…';
      const result = await rpcClient('automation.save', { name, yaml, enabled: false });
      const workflow = result?.workflow || result;
      state.mode = 'workflows';
      state.query = '';
      const input = find('automation-search');
      if (input) input.value = '';
      state.editorOpen = false;
      state.validation = null;
      const editorName = find('automation-editor-name');
      const editorYaml = find('automation-editor-yaml');
      if (editorName) editorName.value = '';
      if (editorYaml) editorYaml.value = '';
      state.selectedKind = '';
      state.selectedID = '';
      toast('Workflow saved paused. Review its triggers, then enable it when ready.', 'success');
      await refresh();
      const saved = state.workflows.find((candidate) => candidate.id === workflow?.id);
      if (saved) chooseRecord('workflow', saved);
    } catch (error) {
      renderValidation(null, error.message || 'Could not save this workflow.');
      toast(error.message || 'Could not save workflow', 'error');
    } finally {
      state.busyAction = '';
      if (button) {
        button.disabled = false;
        button.textContent = 'Create workflow';
      }
    }
  }

  async function loadRunDetail(id) {
    const token = ++detailVersion;
    state.runDetailLoading = true;
    state.runDetailID = id;
    renderInspector();
    try {
      const result = detailText(await rpcClient('automation.runs.get', { id }));
      if (destroyed || token !== detailVersion || state.selectedID !== id) return;
      state.runDetail = result || null;
      state.runDetailID = id;
    } catch (error) {
      if (destroyed || token !== detailVersion || state.selectedID !== id) return;
      toast(error.message || 'Could not load run details', 'error');
    } finally {
      if (token === detailVersion) {
        state.runDetailLoading = false;
        renderInspector();
      }
    }
  }

  async function runAction(key, method, payload, successMessage, after = null) {
    if (state.busyAction) return null;
    state.busyAction = key;
    renderInspector();
    try {
      const result = await rpcClient(method, payload);
      if (successMessage) toast(successMessage, 'success');
      if (after) await after(result);
      else await refresh();
      return result;
    } catch (error) {
      toast(error.message || 'Automation action failed', 'error');
      return null;
    } finally {
      state.busyAction = '';
      render();
    }
  }

  async function startWorkflow(workflow) {
    if (!canRunWorkflow(workflow)) return;
    await runAction(`run:${workflow.id}`, 'automation.run', { id: workflow.id }, `Started ${workflow.name || workflow.id}`, async (result) => {
      const run = detailText(result);
      await refresh();
      const latest = run?.id ? state.runs.find((candidate) => candidate.id === run.id) || run : null;
      if (latest) {
        state.mode = 'runs';
        chooseRecord('run', latest);
      }
    });
  }

  async function setWorkflowEnabled(workflow) {
    if (!canEnableWorkflow(workflow) && !workflow.enabled) return;
    const method = workflow.enabled ? 'automation.disable' : 'automation.enable';
    const message = workflow.enabled ? 'Workflow paused' : 'Workflow enabled';
    await runAction(`toggle:${workflow.id}`, method, { id: workflow.id }, message, async () => {
      await refresh();
    });
  }

  async function cancelRun(run) {
    if (!canCancelRun(run)) return;
    await runAction(`cancel:${run.id}`, 'automation.runs.cancel', { id: run.id }, 'Run cancelled', async () => {
      await refresh();
      const refreshed = state.runs.find((candidate) => candidate.id === run.id);
      if (refreshed) chooseRecord('run', refreshed);
    });
  }

  async function retryRun(run) {
    await runAction(`retry:${run.id}`, 'automation.runs.retry', { id: run.id }, 'Retry started', async () => {
      await refresh();
      const latest = state.runs[0];
      if (latest) {
        state.mode = 'runs';
        chooseRecord('run', latest);
      }
    });
  }

  async function deleteWorkflow(workflow) {
    if (state.busyAction) return;
    const ok = await confirmDialog(
      'Delete automation',
      `"${workflow.name || workflow.id}" and its schedules will be removed.`,
      'Delete',
    );
    // The shared dialog traps focus without restoring it; park focus on a
    // stable control when the overlay is gone.
    if (!ok) {
      find('automation-search')?.focus();
      return;
    }
    await runAction(`delete:${workflow.id}`, 'automation.delete', { id: workflow.id }, 'Workflow deleted', async () => {
      state.selectedKind = '';
      state.selectedID = '';
      await refresh();
      find('automation-search')?.focus();
    });
  }

  async function steerRun(run, trigger = null) {
    if (state.busyAction || !canSteerRun(run)) return;
    const returnFocus = trigger && trigger.isConnected ? trigger : null;
    const result = await dialog({
      title: 'Steer run',
      message: 'Send additional instructions to the agent step that is running right now.',
      fields: [
        {
          name: 'text', label: 'Instructions', tag: 'textarea', rows: 3,
          placeholder: 'Additional instructions for the running agent step…',
        },
      ],
      actions: [
        { label: 'Cancel', value: null },
        { label: 'Send', value: 'send', primary: true },
      ],
    });
    returnFocus?.focus?.();
    if (result.value !== 'send' || !String(result.fields?.text || '').trim()) return;
    await runAction(`steer:${run.id}`, 'automation.runs.steer', { id: run.id, text: result.fields.text.trim() }, 'Instructions sent to the running step', async () => {
      await refresh();
    });
  }

  // A blocked workflow usually means its required capability is disabled at
  // the provider level. automation.capabilities tells which provider gates the
  // run so a single click can re-enable it instead of sending the operator to
  // the Providers view — same flow as the production blocked banner.
  async function enableBlockedProvider(workflow) {
    if (state.busyAction) return;
    state.busyAction = `provider:${workflow.id}`;
    renderInspector();
    try {
      const caps = await rpcClient('automation.capabilities');
      const disabled = (resultArray(caps, 'capabilities')).find((capability) => capability.status === 'disabled');
      if (!disabled) {
        toast('No disabled provider found', 'info');
        return;
      }
      await rpcClient('automation.provider.disable', { id: disabled.provider, enabled: true });
      toast(`Enabled ${disabled.provider}`, 'success');
      await refresh();
    } catch (error) {
      toast(error.message || 'Could not enable the provider', 'error');
    } finally {
      state.busyAction = '';
      render();
    }
  }

  function render() {
    updateOverview();
    renderMode();
    renderList();
    renderSchedules();
    renderInspector();
    showError(state.error);
    const refresh = find('automation-refresh');
    if (refresh) refresh.disabled = state.loading;
    window.lucide?.createIcons?.({ attrs: { 'stroke-width': 1.7 } });
  }

  async function refresh() {
    if (destroyed) return;
    const token = ++refreshVersion;
    state.loading = true;
    syncState(state.loaded ? 'Refreshing' : 'Loading', 'loading');
    render();
    try {
      const [workflowResult, runResult, scheduleResult, eventResult] = await Promise.all([
        rpcClient('automation.list'),
        rpcClient('automation.runs.list'),
        rpcClient('automation.schedules'),
        rpcClient('automation.events'),
      ]);
      if (destroyed || token !== refreshVersion) return;
      state.workflows = resultArray(workflowResult, 'workflows');
      state.runs = resultArray(runResult, 'runs');
      state.schedules = resultArray(scheduleResult, 'schedules');
      state.events = resultArray(eventResult, 'events');
      state.loaded = true;
      state.error = null;
      reconcileSelection();
      syncState(`Updated ${new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(new Date())}`, 'ready');
    } catch (error) {
      if (destroyed || token !== refreshVersion) return;
      state.error = error;
      syncState(state.loaded ? 'Using last snapshot' : 'Unavailable', 'error');
    } finally {
      if (!destroyed && token === refreshVersion) {
        state.loading = false;
        render();
        if (state.selectedKind === 'run' && state.selectedID && state.loaded) void loadRunDetail(state.selectedID);
      }
    }
  }

  function scheduleRefresh() {
    if (!active || destroyed) return;
    clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => { void refresh(); }, 280);
  }

  function activate() {
    active = true;
    void refresh();
  }

  function deactivate() {
    active = false;
    clearTimeout(refreshTimer);
  }

  function init() {
    if (initialized) return controller;
    initialized = true;
    const page = pageNode();
    if (page) page.dataset.mobilePane = 'list';
    listen(find('automation-mobile-back'), 'click', () => setMobilePane('list'));
    // The events index shares the rail with workflows and runs; the mode
    // switch is created here so the view markup stays a stable two-lane
    // contract for other slices.
    const modeSwitch = root.querySelector('.automation-mode-switch');
    if (modeSwitch && !modeSwitch.querySelector('[data-automation-mode="events"]')) {
      modeSwitch.style.gridTemplateColumns = 'repeat(3, minmax(0, 1fr))';
      modeSwitch.append(el('button', {
        type: 'button',
        dataset: { automationMode: 'events' },
        'aria-pressed': 'false',
        text: 'Events',
      }));
    }
    for (const button of all('[data-automation-mode]')) {
      listen(button, 'click', () => {
        const next = button.dataset.automationMode;
        if (!['workflows', 'runs', 'events'].includes(next) || state.mode === next) return;
        state.mode = next;
        setMobilePane('list', { restore: false });
        state.query = '';
        const input = find('automation-search');
        if (input) input.value = '';
        if (!state.editorOpen) {
          const nextRecord = next === 'events' ? state.events[0] : next === 'workflows' ? state.workflows[0] : state.runs[0];
          if (nextRecord) chooseRecord(next === 'events' ? 'event' : next === 'workflows' ? 'workflow' : 'run', nextRecord, { openMobile: false });
          else {
            state.selectedKind = '';
            state.selectedID = '';
            render();
          }
        } else render();
      });
    }
    listen(find('automation-search'), 'input', debounce((event) => {
      state.query = event.target.value;
      renderList();
    }, 120));
    listen(find('automation-refresh'), 'click', () => void refresh());
    listen(find('automation-retry'), 'click', () => void refresh());
    listen(find('automation-create'), 'click', openEditor);
    listen(find('automation-empty-create'), 'click', openEditor);
    listen(find('automation-editor-close'), 'click', () => closeEditor());
    listen(find('automation-editor-cancel'), 'click', () => closeEditor());
    listen(find('automation-editor-form'), 'submit', saveWorkflow);
    listen(find('automation-editor-check'), 'click', () => void validateDefinition());
    listen(find('automation-editor-yaml'), 'input', () => {
      state.validation = null;
      renderValidation(null);
    });
    for (const eventName of AUTOMATION_EVENTS) {
      const unsubscribe = onEvent(eventName, scheduleRefresh);
      if (typeof unsubscribe === 'function') unsubscribers.push(unsubscribe);
    }
    render();
    return controller;
  }

  function destroy() {
    destroyed = true;
    active = false;
    refreshVersion += 1;
    detailVersion += 1;
    clearTimeout(refreshTimer);
    for (const unsubscribe of unsubscribers.splice(0)) unsubscribe();
    for (const remove of removeListeners.splice(0)) remove();
  }

  const controller = { init, activate, deactivate, refresh, destroy, state };
  return controller;
}
