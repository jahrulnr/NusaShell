import { renderMarkdownBody } from '../../shared/markdown-body.js?v=7';
import { attachCodeEditorButtons, codeModeFor } from '../../shared/code-surface.js?v=2';
import { isNestedScrollerEvent } from '../../js/agent-ui.js';
import { syncTranscript } from '../../js/views/agent/subagents.js';
import {
  createSubagentPromptCard,
  setSubagentPromptExpanded,
  subagentPromptCollapsible,
  subagentPromptExpanded,
  syncSubagentPromptText,
} from '../../js/views/agent/subagent-prompt.js?v=1';
import { sortSubagentViews, subagentAgentLabel } from './subagent-state.js?v=4';

const statusLabels = Object.freeze({
  starting: 'Starting',
  running: 'Running',
  complete: 'Complete',
  waiting: 'Waiting',
  waiting_permission: 'Waiting for permission',
  failed: 'Failed',
});

// Follow and reader positions belong to a run, not to the shared dock DOM.
// The detail nodes themselves stay mounted while ACP snapshots arrive; these
// maps are only needed when the user switches between runs in the same dock.
const followByRunID = new Map();
const objectiveScrollByRunID = new Map();
const objectiveExpandedByRunID = new Map();
const transcriptScrollByRunID = new Map();
const panelStateByElement = new WeakMap();

function statusLabel(status) {
  return statusLabels[status] || status || 'Waiting';
}

function followState(runID) {
  if (!followByRunID.has(runID)) {
    followByRunID.set(runID, {
      runID,
      pinned: true,
      intent: '',
      pointerActive: false,
      lastScrollTop: 0,
    });
  }
  return followByRunID.get(runID);
}

function isAtTail(container) {
  return container.scrollTop + container.clientHeight >= container.scrollHeight - 16;
}

function scheduleTail(container, follow) {
  if (!follow.pinned) return;
  const stick = () => {
    if (container._subagentFollow !== follow || !follow.pinned) return;
    container.scrollTop = container.scrollHeight;
    transcriptScrollByRunID.set(follow.runID, container.scrollTop);
  };
  const view = container.ownerDocument?.defaultView;
  if (typeof view?.requestAnimationFrame === 'function') view.requestAnimationFrame(stick);
  else setTimeout(stick, 0);
}

function pinTranscriptToTail(container, follow) {
  follow.intent = '';
  follow.pinned = true;
  container.scrollTop = container.scrollHeight;
  transcriptScrollByRunID.set(follow.runID, container.scrollTop);
}

function bindObjectiveScroll(container) {
  if (container._subagentObjectiveScrollBound) return;
  container._subagentObjectiveScrollBound = true;
  container.addEventListener('scroll', () => {
    const runID = container._subagentObjectiveRunID;
    if (!runID) return;
    objectiveScrollByRunID.set(runID, container.scrollTop);
  }, { passive: true });
}

function restoreObjectiveScroll(container, runID) {
  if (!objectiveScrollByRunID.has(runID)) return;
  const top = objectiveScrollByRunID.get(runID);
  if (!Number.isFinite(top)) return;
  const restore = () => {
    if (container._subagentObjectiveRunID === runID) container.scrollTop = top;
  };
  restore();
  const view = container.ownerDocument?.defaultView;
  if (typeof view?.requestAnimationFrame === 'function') {
    view.requestAnimationFrame(() => view.requestAnimationFrame(restore));
  } else setTimeout(restore, 0);
}

function restoreTranscriptScroll(container, runID, follow) {
  if (follow.pinned || !transcriptScrollByRunID.has(runID)) return;
  const top = transcriptScrollByRunID.get(runID);
  if (!Number.isFinite(top)) return;
  const restore = () => {
    if (container._subagentFollow !== follow || follow.pinned) return;
    container.scrollTop = top;
    follow.lastScrollTop = container.scrollTop;
  };
  restore();
  const view = container.ownerDocument?.defaultView;
  if (typeof view?.requestAnimationFrame === 'function') {
    view.requestAnimationFrame(() => view.requestAnimationFrame(restore));
  } else setTimeout(restore, 0);
}

function bindTranscriptFollow(container) {
  if (container._subagentFollowBound) return;
  container._subagentFollowBound = true;
  let touchY = null;
  const markIntent = (direction) => {
    const current = container._subagentFollow;
    if (!current) return;
    current.intent = direction;
    if (direction === 'up') current.pinned = false;
    else if (direction === 'down' && isAtTail(container)) current.pinned = true;
  };
  container.addEventListener('wheel', (event) => {
    if (isNestedScrollerEvent(event.target, container)) return;
    if (event.deltaY < 0) markIntent('up');
    if (event.deltaY > 0) markIntent('down');
  }, { passive: true });
  container.addEventListener('touchstart', (event) => { touchY = event.touches[0]?.clientY ?? null; }, { passive: true });
  container.addEventListener('touchmove', (event) => {
    if (isNestedScrollerEvent(event.target, container)) return;
    const nextY = event.touches[0]?.clientY;
    if (touchY !== null && nextY > touchY) markIntent('up');
    else if (touchY !== null && nextY < touchY) markIntent('down');
    touchY = nextY ?? null;
  }, { passive: true });
  container.addEventListener('keydown', (event) => {
    if (['ArrowUp', 'PageUp', 'Home'].includes(event.key) || (event.key === ' ' && event.shiftKey)) markIntent('up');
    if (['ArrowDown', 'PageDown'].includes(event.key) || (event.key === ' ' && !event.shiftKey)) markIntent('down');
    if (event.key === 'End') {
      pinTranscriptToTail(container, container._subagentFollow);
      event.preventDefault();
    }
  });
  container.addEventListener('pointerdown', (event) => {
    if (event.button !== 0) return;
    const current = container._subagentFollow;
    if (!current) return;
    const bounds = container.getBoundingClientRect?.();
    // Only a primary-button press in the scrollbar gutter starts a drag
    // transaction. Clicking transcript content must not detach live follow.
    current.pointerActive = !bounds || event.clientX >= bounds.right - 16;
    current.lastScrollTop = container.scrollTop;
  }, { passive: true });
  const releasePointer = () => {
    const current = container._subagentFollow;
    if (current) current.pointerActive = false;
  };
  container.addEventListener('pointerup', releasePointer, { passive: true });
  container.addEventListener('pointercancel', releasePointer, { passive: true });
  container.addEventListener('scroll', () => {
    const current = container._subagentFollow;
    if (!current) return;
    transcriptScrollByRunID.set(current.runID, container.scrollTop);
    const intent = current.intent;
    const atTail = isAtTail(container);
    current.intent = intent === 'down' && !atTail ? 'down' : '';
    if (intent === 'up') current.pinned = false;
    else if (intent === 'down' && atTail) current.pinned = true;
    else if (current.pointerActive) {
      // While dragging the scrollbar, every intermediate position away from
      // the tail is reader-owned. Repin only after the actual tail is reached.
      if (atTail) {
        current.pinned = true;
        current.intent = '';
      } else if (container.scrollTop !== current.lastScrollTop) {
        current.pinned = false;
        current.intent = '';
      }
    }
    current.lastScrollTop = container.scrollTop;
  }, { passive: true });
}

function createPanelState(panel) {
  const document = panel.ownerDocument;
  const section = document.createElement('section');
  section.className = 'dock-section subagent-section';
  section.innerHTML = `
    <header class="dock-section-head"><strong>Parallel runs</strong><span>0 running</span></header>
    <div class="subagent-switcher">
      <button class="subagent-trigger" type="button" role="combobox" aria-haspopup="listbox" aria-expanded="false" aria-controls="subagent-options">
        <span class="subagent-option-dot" aria-hidden="true"></span>
        <span class="subagent-trigger-copy"><strong></strong><small></small></span>
        <span class="subagent-chevron" aria-hidden="true">⌄</span>
      </button>
      <div class="subagent-options" id="subagent-options" role="listbox" aria-label="Parallel subagents" hidden></div>
    </div>
    <article class="subagent-detail" aria-live="polite">
      <header class="subagent-meta acp-run-meta"><span class="subagent-agent"></span><span class="subagent-status"></span><span class="subagent-elapsed"></span></header>
      <div class="subagent-transcript acp-transcript" role="log" aria-label="Subagent transcript" tabindex="0"></div>
    </article>`;
  panel.replaceChildren(section);

  const objectiveCard = createSubagentPromptCard({
    label: 'Objective',
    className: 'subagent-objective-card',
    bodyClass: 'subagent-objective',
    // This module is loaded under a ?v= query while the transcript renderer
    // imports the helper unversioned — two counter instances — so the dock's
    // card takes a host-scoped id instead of a generated one.
    id: `subagent-objective-${panel.id || 'dock'}`,
    ownerDocument: document,
  });
  objectiveCard.body.setAttribute('role', 'region');
  objectiveCard.body.setAttribute('aria-label', 'Subagent objective');
  objectiveCard.toggle.addEventListener('click', () => {
    const runID = objectiveCard.body._subagentObjectiveRunID;
    if (runID) objectiveExpandedByRunID.set(runID, subagentPromptExpanded(objectiveCard));
  });
  section.querySelector('.subagent-meta').after(objectiveCard.root);

  const state = {
    panel,
    section,
    headCount: section.querySelector('.dock-section-head span'),
    trigger: section.querySelector('.subagent-trigger'),
    menu: section.querySelector('.subagent-options'),
    detail: section.querySelector('.subagent-detail'),
    status: section.querySelector('.subagent-status'),
    agent: section.querySelector('.subagent-agent'),
    elapsed: section.querySelector('.subagent-elapsed'),
    objective: objectiveCard.body,
    objectiveCard,
    transcript: section.querySelector('.subagent-transcript'),
    options: new Map(),
    orderedSubagents: [],
    activeID: '',
    objectiveSource: undefined,
    hasObjectiveSource: false,
  };
  bindObjectiveScroll(state.objective);
  bindTranscriptFollow(state.transcript);
  state.trigger.addEventListener('click', () => setOpen(state, state.trigger.getAttribute('aria-expanded') !== 'true'));
  state.menu.addEventListener('pointerdown', (event) => {
    const option = event.target.closest?.('[role="option"]');
    if (option && state.menu.contains(option)) chooseSubagent(state, option, event);
  });
  state.menu.addEventListener('click', (event) => {
    // Pointer selection already ran on pointerdown. detail=0 is the keyboard
    // and assistive-technology path, which still needs the click fallback.
    if (event.detail > 0) return;
    const option = event.target.closest?.('[role="option"]');
    if (option && state.menu.contains(option)) chooseSubagent(state, option, event);
  });
  state.trigger.addEventListener('keydown', (event) => {
    if (!['ArrowDown', 'ArrowUp'].includes(event.key)) return;
    event.preventDefault();
    setOpen(state, true);
  });
  panelStateByElement.set(panel, state);
  return state;
}

function setOpen(state, open) {
  state.trigger.setAttribute('aria-expanded', String(open));
  state.menu.hidden = !open;
  if (!open) return;
  const option = state.options.get(state.activeID) || state.options.get(state.orderedSubagents[0]?.id);
  option?.focus();
}

function createOption(state, subagent) {
  const document = state.panel.ownerDocument;
  const option = document.createElement('button');
  option.type = 'button';
  option.id = `subagent-option-${subagent.id}`;
  option.dataset.subagentId = subagent.id;
  option.setAttribute('role', 'option');
  option.innerHTML = '<span class="subagent-option-dot" aria-hidden="true"></span><span></span><small></small>';
  option.addEventListener('keydown', (event) => moveOptionFocus(event, state, option.dataset.subagentId));
  return option;
}

function updateOption(option, subagent) {
  option.title = `${subagentAgentLabel(subagent)} · ${subagent.name} · ${statusLabel(subagent.status)}`;
  option.querySelector('.subagent-option-dot').dataset.status = subagent.status;
  option.querySelector('span:nth-child(2)').textContent = subagent.name;
  option.querySelector('small').textContent = `${subagentAgentLabel(subagent)} · ${statusLabel(subagent.status)}`;
}

function syncOptions(state, orderedSubagents) {
  state.orderedSubagents = orderedSubagents;
  const seen = new Set();
  for (const subagent of orderedSubagents) {
    seen.add(subagent.id);
    let option = state.options.get(subagent.id);
    if (!option) {
      option = createOption(state, subagent);
      state.options.set(subagent.id, option);
    }
    updateOption(option, subagent);
    option.setAttribute('aria-selected', String(subagent.id === state.activeID));
    state.menu.append(option);
  }
  for (const [id, option] of state.options) {
    if (seen.has(id)) continue;
    option.remove();
    state.options.delete(id);
  }
}

function moveOptionFocus(event, state, currentID) {
  if (event.key === 'Escape') {
    event.preventDefault();
    setOpen(state, false);
    state.trigger.focus();
    return;
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  event.preventDefault();
  const ids = state.orderedSubagents.map((subagent) => subagent.id);
  const currentIndex = Math.max(0, ids.indexOf(currentID));
  let next = currentIndex;
  if (event.key === 'ArrowDown') next = (currentIndex + 1) % ids.length;
  if (event.key === 'ArrowUp') next = (currentIndex - 1 + ids.length) % ids.length;
  if (event.key === 'Home') next = 0;
  if (event.key === 'End') next = ids.length - 1;
  state.options.get(ids[next])?.focus();
}

function updateTrigger(state, active) {
  state.trigger.querySelector('.subagent-option-dot').dataset.status = active.status;
  state.trigger.querySelector('strong').textContent = active.name;
  state.trigger.querySelector('small').textContent = `${subagentAgentLabel(active)} · ${statusLabel(active.status)}`;
  state.trigger.setAttribute('aria-activedescendant', `subagent-option-${active.id}`);
  for (const option of state.options.values()) {
    option.setAttribute('aria-selected', String(option.dataset.subagentId === active.id));
  }
}

function updateDetailMeta(state, active) {
  state.agent.textContent = subagentAgentLabel(active);
  state.status.textContent = statusLabel(active.status);
  state.status.dataset.status = active.status;
  state.elapsed.textContent = active.elapsed || '';
}

function chooseSubagent(state, option, event) {
  if (event?.type === 'pointerdown' && event.button !== 0) return;
  const active = state.orderedSubagents.find((candidate) => candidate.id === option?.dataset?.subagentId);
  if (!active) return;
  if (event?.type === 'pointerdown') event.preventDefault();
  selectSubagent(state, active, { followTail: true });
}

function syncAgentTranscript(state, transcript) {
  // The established Agent renderer uses the shared `el()` helper, whose
  // creation context is the global document. Keep that renderer reusable for
  // detached JSDOM/test panels and embedded documents without changing the
  // production Agent contract.
  const ownerDocument = state.detail.ownerDocument;
  const previousDocument = globalThis.document;
  const needsDocument = previousDocument !== ownerDocument;
  if (needsDocument) globalThis.document = ownerDocument;
  try {
    syncTranscript(state.detail, transcript, '', '');
  } finally {
    if (needsDocument) {
      if (previousDocument === undefined) delete globalThis.document;
      else globalThis.document = previousDocument;
    }
  }
}

function decorateSubagentTranscript(container) {
  for (const paragraph of container.querySelectorAll('.agent-bubble-text p')) {
    const raw = paragraph.textContent || '';
    paragraph.classList.toggle('subagent-change-summary', /(?:^|\n)\s*(?:New|Modify|Delete):\s/.test(raw));
  }
  for (const code of container.querySelectorAll('pre > code')) {
    const label = code.parentElement?.querySelector('.code-language-label');
    if (!label) continue;
    if (!label.dataset.baseLabel) label.dataset.baseLabel = label.textContent || 'Code';
    const language = [...code.classList].find((name) => name.startsWith('language-'))?.slice('language-'.length)
      || code.dataset.language || '';
    const mime = codeModeFor(language);
    label.textContent = `${label.dataset.baseLabel} · ${mime}`;
    label.title = mime;
  }
}

function bindReasoningCodeSurfaces(container) {
  for (const details of container.querySelectorAll('details.agent-reasoning')) {
    if (details._subagentCodeEditorBound) continue;
    details._subagentCodeEditorBound = true;
    details.addEventListener('toggle', () => {
      if (details.open) attachCodeEditorButtons(details);
    });
    if (details.open) attachCodeEditorButtons(details);
  }
}

function selectSubagent(state, active, { followTail = false } = {}) {
  const previousID = state.activeID;
  if (previousID) {
    objectiveScrollByRunID.set(previousID, state.objective.scrollTop);
    transcriptScrollByRunID.set(previousID, state.transcript.scrollTop);
  }
  state.activeID = active.id;
  state.panel.dataset.activeSubagentId = active.id;
  state.objective._subagentObjectiveRunID = active.id;
  const follow = followState(active.id);
  if (followTail) {
    follow.pinned = true;
    follow.intent = '';
  }
  state.transcript._subagentFollow = follow;
  updateTrigger(state, active);
  updateDetailMeta(state, active);
  // Close and acknowledge the selection before touching the potentially
  // large transcript. Otherwise a live run with hundreds of events leaves
  // the menu visually frozen until reconciliation finishes.
  if (followTail) {
    setOpen(state, false);
    state.trigger.focus();
  }

  const objectiveSource = active.objective || '';
  const objectiveChanged = !state.hasObjectiveSource || state.objectiveSource !== objectiveSource;
  state.objectiveSource = objectiveSource;
  state.hasObjectiveSource = true;
  // The prompt card re-renders its body only when the text actually changed,
  // so live deltas keep the reader's scroll and expansion state.
  syncSubagentPromptText(state.objectiveCard, objectiveSource, (body, raw) => {
    void renderMarkdownBody(body, raw).then(() => restoreObjectiveScroll(body, active.id));
  });
  if (!objectiveChanged && previousID !== active.id) restoreObjectiveScroll(state.objective, active.id);
  // Expansion belongs to the run, not the shared card: a switch applies that
  // run's own choice instead of inheriting the previous run's open prompt.
  if (subagentPromptCollapsible(objectiveSource)) {
    setSubagentPromptExpanded(state.objectiveCard, objectiveExpandedByRunID.get(active.id) === true);
  }

  // Reuse the Agent view's proven transcript reconciler. It keeps each
  // message/round node stable, updates only changed text/tool data, and thus
  // preserves open reasoning disclosures while ACP deltas append.
  syncAgentTranscript(state, active.transcript || []);
  // Keep the inline code block light by default, but expose the existing
  // read-only CodeMirror surface when a reader needs line numbers, search, or
  // a wider source view. The helper is idempotent across live deltas.
  attachCodeEditorButtons(state.transcript);
  bindReasoningCodeSurfaces(state.transcript);
  decorateSubagentTranscript(state.transcript);
  if (previousID !== active.id) restoreTranscriptScroll(state.transcript, active.id, follow);
  scheduleTail(state.transcript, follow);
}

// External opener seam: a delegation card carries its own run id, which is
// rarely the newest run in the room. When the run is already listed it is
// selected immediately; otherwise the request is parked on the panel until a
// later mount hydrates it (stream snapshots lag behind the timeline card).
export function selectSubagentRun(panel, runID) {
  if (!panel || !runID) return false;
  const state = panelStateByElement.get(panel);
  const active = state?.orderedSubagents.find((subagent) => subagent.id === runID);
  if (!active) {
    panel.dataset.requestedSubagentId = runID;
    return false;
  }
  delete panel.dataset.requestedSubagentId;
  selectSubagent(state, active, { followTail: true });
  return true;
}

export function mountSubagentPanel(panel, subagents) {
  const orderedSubagents = sortSubagentViews(subagents);
  const running = orderedSubagents.filter((subagent) => subagent.status === 'running').length;
  let state = panelStateByElement.get(panel);
  if (!state || state.section.parentElement !== panel) state = createPanelState(panel);
  state.headCount.textContent = `${running} running`;
  syncOptions(state, orderedSubagents);
  const requested = panel.dataset.requestedSubagentId || '';
  const active = orderedSubagents.find((subagent) => subagent.id === (requested || state.activeID)) || orderedSubagents[0];
  if (!active) return;
  if (requested && active.id === requested) delete panel.dataset.requestedSubagentId;
  selectSubagent(state, active, { followTail: !state.activeID || state.activeID !== active.id });
}
