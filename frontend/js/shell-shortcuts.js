// Global keyboard shortcuts for the NusaShell workbench.
// Bound once from the application shell. View-specific handlers stay in
// their modules; this file only routes focus and a few chrome actions.

const SEARCH_BY_VIEW = {
  home: 'search-input',
  agent: 'conversation-search',
  skills: 'skills-search',
  learning: 'learning-search-input',
};

export function searchFieldForView(view) {
  return SEARCH_BY_VIEW[view] || null;
}

export function isTypingTarget(node) {
  if (!node || node.nodeType === 9 || node === document.body || node === document.documentElement) return false;
  const el = node.nodeType === 1 ? node : node.parentElement;
  if (!el || typeof el.closest !== 'function') return false;
  if (el.closest('.ss-main, .ss-content, .ui-dialog, [contenteditable="true"]')) return true;
  const tag = el.tagName;
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true;
  if (tag === 'INPUT') {
    const type = (el.getAttribute('type') || 'text').toLowerCase();
    return !['button', 'checkbox', 'radio', 'file', 'reset', 'submit', 'range', 'color'].includes(type);
  }
  if (el.isContentEditable) return true;
  return false;
}

function activeView() {
  return document.querySelector('.view.active')?.dataset.view || '';
}

function focusSearch(view = activeView()) {
  const id = searchFieldForView(view);
  if (!id) return false;
  const field = document.getElementById(id);
  if (!field) return false;
  field.focus();
  if (typeof field.select === 'function') field.select();
  return true;
}

export function bindShellShortcuts({
  getView = activeView,
  onNewConversation,
} = {}) {
  document.addEventListener('keydown', (event) => {
    if (event.defaultPrevented) return;
    const meta = event.metaKey || event.ctrlKey;
    const view = getView();

    const typing = isTypingTarget(event.target) || isTypingTarget(document.activeElement);

    if (meta && !event.altKey && (event.key === 'k' || event.key === 'K')) {
      if (!searchFieldForView(view)) return;
      event.preventDefault();
      focusSearch(view);
      return;
    }

    if (meta && !event.altKey && !event.shiftKey && (event.key === 'n' || event.key === 'N')) {
      if (view !== 'agent') return;
      if (typing) return;
      event.preventDefault();
      onNewConversation?.();
      return;
    }

    if (!meta && event.key === '/' && !event.altKey) {
      if (typing) return;
      if (!searchFieldForView(view)) return;
      event.preventDefault();
      focusSearch(view);
    }
  });
}
