// Ask question card — ported 1:1 from Electron's agent-conversation-controller.js
// createAskCard / syncAskSendState / submitAskCard / sealAskCard.
//
// The card is rendered when the agent.ask.pending event fires, sealed when
// agent.ask.answered or agent.ask.cancelled fires, and submits the user's
// answer via the agent.ask.answer RPC.

import { el } from '../ui.js';
import { rpc } from '../rpc.js';

// parseAskAnswer extracts the structured answer from a sealed tool output.
function parseAskAnswer(output) {
  if (!output) return null;
  try {
    const parsed = JSON.parse(output);
    if (parsed && typeof parsed === 'object') {
      return {
        via: parsed.via || '',
        answer: parsed.answer || '',
        optionIds: Array.isArray(parsed.option_ids) ? parsed.option_ids : (Array.isArray(parsed.optionIds) ? parsed.optionIds : []),
        text: parsed.text || '',
      };
    }
  } catch {}
  return { via: '', answer: String(output), optionIds: [], text: '' };
}

// createAskCard builds the ask_question card DOM. When sealed=true, the card
// is read-only and shows the answer. Otherwise it's interactive.
export function createAskCard(callId, args, { sealed = false, output = '', ok = true, error = '', runId = '', onSubmit = null, onStop = null } = {}) {
  const question = typeof args?.question === 'string' ? args.question : 'Choose a response';
  const options = Array.isArray(args?.options) ? args.options : [];
  const multiSelect = Boolean(args?.multi_select);
  const allowFreeText = args?.allow_free_text !== false;
  const parsedAnswer = sealed ? parseAskAnswer(output) : null;

  const card = el('div', { class: `agent-ask-card${sealed ? ' is-sealed' : ' is-pending'}${ok === false ? ' is-error' : ''}` });
  card.dataset.callId = callId || '';
  card.dataset.runId = runId || '';
  card._toolArgs = args && typeof args === 'object' ? args : {};

  const header = el('div', { class: 'agent-ask-header' });
  header.append(
    el('span', { class: 'agent-ask-header-icon', text: '⚒' }),
    el('span', { class: 'agent-ask-header-title', text: 'Ask Question' }),
  );
  card.append(header);

  const body = el('div', { class: 'agent-ask-body' });
  const hintText = multiSelect
    ? 'Choose one or more responses so I can continue the task.'
    : 'Choose one response so I can continue the task.';
  body.append(
    el('div', { class: 'agent-ask-question', text: question }),
    el('div', { class: 'agent-ask-hint', text: hintText }),
  );

  const optionsWrap = el('div', { class: 'agent-ask-options' });
  const selected = new Set(
    sealed && parsedAnswer?.optionIds?.length
      ? parsedAnswer.optionIds
      : options.filter((o) => o?.default).map((o) => String(o.id)),
  );
  if (!multiSelect && selected.size > 1) {
    const first = [...selected][0];
    selected.clear();
    if (first) selected.add(first);
  }

  for (const option of options) {
    if (!option || typeof option !== 'object') continue;
    const id = String(option.id ?? '');
    const label = String(option.label ?? id);
    const row = el('button', { class: `agent-ask-option${selected.has(id) ? ' is-selected' : ''}`, type: 'button' });
    row.dataset.optionId = id;
    row.setAttribute('aria-pressed', selected.has(id) ? 'true' : 'false');
    if (sealed) row.disabled = true;

    const marker = el('span', { class: `agent-ask-option-marker${multiSelect ? ' is-check' : ' is-radio'}` });
    const media = el('div', { class: 'agent-ask-option-media' });
    if (typeof option.image === 'string' && option.image.trim()) {
      const img = document.createElement('img');
      img.className = 'agent-ask-option-image';
      img.src = option.image.trim();
      img.alt = '';
      media.append(img);
    } else if (typeof option.icon === 'string' && option.icon.trim()) {
      media.append(el('span', { class: 'agent-ask-option-icon', text: option.icon.trim() }));
    } else {
      media.append(el('span', { class: 'agent-ask-option-icon is-empty', text: '•' }));
    }

    const copy = el('div', { class: 'agent-ask-option-copy' });
    const titleRow = el('div', { class: 'agent-ask-option-title-row' });
    titleRow.append(el('span', { class: 'agent-ask-option-label', text: label }));
    if (option.default) titleRow.append(el('span', { class: 'agent-ask-option-badge', text: 'Recommended' }));
    copy.append(titleRow);
    if (typeof option.description === 'string' && option.description.trim()) {
      copy.append(el('div', { class: 'agent-ask-option-desc', text: option.description.trim() }));
    }

    row.append(marker, media, copy);
    if (!sealed) {
      row.addEventListener('click', () => {
        if (card.classList.contains('is-submitting') || card.classList.contains('is-sealed')) return;
        if (multiSelect) {
          if (selected.has(id)) selected.delete(id);
          else selected.add(id);
        } else {
          selected.clear();
          selected.add(id);
          card.querySelectorAll('.agent-ask-option').forEach((node) => {
            node.classList.toggle('is-selected', node.dataset.optionId === id);
            node.setAttribute('aria-pressed', node.dataset.optionId === id ? 'true' : 'false');
          });
        }
        row.classList.toggle('is-selected', selected.has(id));
        row.setAttribute('aria-pressed', selected.has(id) ? 'true' : 'false');
        syncAskSendState(card, selected);
      });
    }
    optionsWrap.append(row);
  }
  body.append(optionsWrap);

  // Free text is a *supplement* to the selected options (a note, a
  // suggestion, a different direction), never a replacement — picking an
  // option no longer clears the note and vice versa.
  const customActive = sealed && (parsedAnswer?.via === 'text' || Boolean(parsedAnswer?.text));
  if (allowFreeText || customActive) {
    const custom = el('div', { class: `agent-ask-custom${customActive ? ' is-active' : ''}` });
    const customToggle = el('button', { class: 'agent-ask-custom-toggle', type: 'button', text: customActive ? 'Custom answer' : 'Add a note…' });
    customToggle.disabled = sealed;
    const textarea = document.createElement('textarea');
    textarea.className = 'agent-ask-textarea';
    textarea.rows = 3;
    textarea.placeholder = 'Type a note, suggestion, or different direction…';
    textarea.maxLength = 8000;
    if (sealed && customActive) {
      textarea.value = parsedAnswer?.via === 'text'
        ? (parsedAnswer.answer || parsedAnswer.text || '')
        : (parsedAnswer.text || '');
      textarea.disabled = true;
    }
    if (!sealed) {
      customToggle.addEventListener('click', () => {
        custom.classList.add('is-active');
        textarea.focus();
        syncAskSendState(card, selected);
      });
      textarea.addEventListener('input', () => syncAskSendState(card, selected));
    }
    custom.append(customToggle, textarea);
    body.append(custom);
  }

  if (sealed) {
    const answerLine = el('div', { class: 'agent-ask-answer', text: ok === false ? (error || 'Ask question failed.') : `Answer: ${parsedAnswer?.answer || output || '—'}` });
    body.append(answerLine);
  } else {
    const actions = el('div', { class: 'agent-ask-actions' });
    const send = el('button', { class: 'agent-ask-send', type: 'button', html: `<span class="agent-ask-send-icon">✈</span><span>Send answer</span>` });
    send.addEventListener('click', () => void submitAskCard(card, selected, onSubmit));
    actions.append(
      send,
      el('span', { class: 'agent-ask-dismiss-hint', text: 'Esc or Stop to cancel the turn' }),
    );
    body.append(actions);
    card.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && !card.classList.contains('is-sealed')) {
        event.preventDefault();
        onStop?.();
      }
    });
    card.tabIndex = 0;
    syncAskSendState(card, selected);
  }

  card.append(body);
  return card;
}

function syncAskSendState(card, selected) {
  const send = card.querySelector('.agent-ask-send');
  if (!send) return;
  const textarea = card.querySelector('.agent-ask-textarea');
  const customActive = card.querySelector('.agent-ask-custom')?.classList.contains('is-active');
  const hasText = Boolean(textarea?.value?.trim());
  const hasOptions = selected.size > 0;
  send.disabled = card.classList.contains('is-submitting') || (!hasOptions && !(customActive && hasText));
}

async function submitAskCard(card, selected, onSubmit) {
  const callId = card.dataset.callId;
  const runId = card.dataset.runId;
  if (!callId || !runId) return;
  if (card.classList.contains('is-submitting')) return;
  const textarea = card.querySelector('.agent-ask-textarea');
  const customActive = card.querySelector('.agent-ask-custom')?.classList.contains('is-active');
  const text = textarea?.value?.trim() || '';
  const hasOptions = selected.size > 0;
  const hasText = customActive && text.length > 0;
  if (!hasOptions && !hasText) return;

  const via = hasOptions ? 'option' : 'text';
  card.classList.add('is-submitting');
  syncAskSendState(card, selected);
  try {
    await rpc('agent.ask.answer', {
      run_id: runId,
      tool_call_id: callId,
      via,
      ...(hasOptions ? { option_ids: [...selected] } : {}),
      ...(hasText ? { text } : {}),
    });
    card.querySelectorAll('button, textarea').forEach((node) => { node.disabled = true; });
  } catch (err) {
    card.classList.remove('is-submitting');
    syncAskSendState(card, selected);
    onSubmit?.(err);
  }
}

// sealAskCard transitions a pending card to sealed state with the answer.
export function sealAskCard(card, { ok = true, answer = '', via = '', optionIds = [], text = '', error = '' } = {}) {
  card.classList.remove('is-pending', 'is-submitting');
  card.classList.add('is-sealed');
  card.classList.toggle('is-error', ok === false);
  card.querySelectorAll('button, textarea').forEach((node) => { node.disabled = true; });
  let answerEl = card.querySelector('.agent-ask-answer');
  if (!answerEl) {
    answerEl = el('div', { class: 'agent-ask-answer' });
    card.querySelector('.agent-ask-body')?.append(answerEl);
  }
  answerEl.textContent = ok === false ? (error || 'Ask question failed.') : `Answer: ${answer || '—'}`;
  if (via === 'option' && optionIds?.length) {
    const chosen = new Set(optionIds);
    card.querySelectorAll('.agent-ask-option').forEach((node) => {
      node.classList.toggle('is-selected', chosen.has(node.dataset.optionId));
    });
  }
  // A supplementary note shows alongside the selected options (via
  // "option" + text); a pure custom answer fills the textarea alone.
  const note = via === 'text' ? (answer || text) : text;
  if (note) {
    const custom = card.querySelector('.agent-ask-custom');
    const textarea = card.querySelector('.agent-ask-textarea');
    custom?.classList.add('is-active');
    if (textarea) textarea.value = note;
  }
}

// cancelAskCard seals the card as cancelled (error state).
export function cancelAskCard(card, reason = '') {
  sealAskCard(card, { ok: false, error: reason || 'Ask question cancelled.' });
}
