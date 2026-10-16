import { renderMarkdownBody } from '../../shared/markdown-body.js?v=7';

function element(document, tag, className, value = '') {
  const node = document.createElement(tag);
  if (className) node.className = className;
  node.textContent = value;
  return node;
}

export function createAskPopover(root, { rpc, onClose = null } = {}) {
  let current = null;

  function close() {
    current = null;
    root.hidden = true;
    root.replaceChildren();
    onClose?.();
  }

  function show(ask) {
    if (!ask) return close();
    current = ask;
    const document = root.ownerDocument;
    const selected = new Set((ask.options || []).filter((option) => option.default).map((option) => String(option.id)));
    if (!ask.multi_select && selected.size > 1) selected.delete([...selected][1]);
    root.replaceChildren();
    root.hidden = false;
    root.setAttribute('role', 'dialog');
    root.setAttribute('aria-label', 'Question from agent');

    const header = element(document, 'header', 'ask-popover-head');
    header.append(element(document, 'span', 'ask-popover-kicker', 'Input needed'), element(document, 'span', 'ask-popover-state', 'Turn paused'));
    const question = element(document, 'div', 'ask-popover-question');
    void renderMarkdownBody(question, ask.question || 'Choose a response');
    root.append(header, question);

    const options = element(document, 'div', 'ask-popover-options');
    function syncOptions() {
      options.querySelectorAll('[data-option-id]').forEach((button) => {
        const active = selected.has(button.dataset.optionId);
        button.classList.toggle('is-selected', active);
        button.setAttribute('aria-pressed', String(active));
      });
    }
    for (const option of ask.options || []) {
      const button = element(document, 'button', 'ask-popover-option');
      button.type = 'button'; button.dataset.optionId = String(option.id);
      const marker = element(document, 'span', 'ask-option-marker', ask.multi_select ? '□' : '○');
      const copy = element(document, 'span', 'ask-option-copy');
      copy.append(element(document, 'strong', '', option.label || option.id));
      if (option.description) copy.append(element(document, 'small', '', option.description));
      button.append(marker, copy);
      button.addEventListener('click', () => {
        const id = button.dataset.optionId;
        if (ask.multi_select) selected.has(id) ? selected.delete(id) : selected.add(id);
        else { selected.clear(); selected.add(id); }
        if (!ask.multi_select && textarea) textarea.value = '';
        syncOptions(); syncSubmit();
      });
      options.append(button);
    }
    root.append(options);

    let textarea = null;
    if (ask.allow_free_text !== false) {
      textarea = document.createElement('textarea');
      textarea.className = 'ask-popover-note'; textarea.rows = 2; textarea.maxLength = 8000;
      textarea.placeholder = ask.multi_select ? 'Add context…' : 'Or type another answer…';
      textarea.addEventListener('input', () => {
        if (!ask.multi_select && textarea.value.trim()) selected.clear();
        syncOptions(); syncSubmit();
      });
      root.append(textarea);
    }

    const error = element(document, 'span', 'ask-popover-error'); error.hidden = true;
    const actions = element(document, 'footer', 'ask-popover-actions');
    const cancel = element(document, 'button', 'ask-popover-cancel', 'Cancel turn'); cancel.type = 'button';
    const submit = element(document, 'button', 'ask-popover-submit', 'Continue'); submit.type = 'button'; submit.dataset.askSubmit = '';
    function syncSubmit() { submit.disabled = selected.size === 0 && !textarea?.value.trim(); }
    cancel.addEventListener('click', async () => {
      cancel.disabled = true;
      try { await rpc('agent.ask.cancel', { run_id: ask.run_id, tool_call_id: ask.tool_call_id, reason: 'Cancelled by user' }); close(); }
      catch (failure) { cancel.disabled = false; error.hidden = false; error.textContent = failure.message || 'Could not cancel'; }
    });
    submit.addEventListener('click', async () => {
      const note = textarea?.value.trim() || '';
      if (!selected.size && !note) return;
      submit.disabled = true; error.hidden = true;
      const via = note && !ask.multi_select ? 'text' : selected.size ? 'option' : 'text';
      const payload = { run_id: ask.run_id, tool_call_id: ask.tool_call_id, via };
      if (via === 'option') payload.option_ids = [...selected];
      if (note) payload.text = note;
      try { await rpc('agent.ask.answer', payload); close(); }
      catch (failure) { submit.disabled = false; error.hidden = false; error.textContent = failure.message || 'Could not send answer'; }
    });
    actions.append(error, cancel, submit); root.append(actions);
    syncOptions(); syncSubmit();
    queueMicrotask(() => root.querySelector('.ask-popover-option, .ask-popover-note')?.focus());
  }

  return { show, close, current: () => current };
}
