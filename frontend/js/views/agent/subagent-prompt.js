/* Subagent prompt card: one bounded, expandable chrome for delegation and
 * steering prompts wherever they render (dock objective, dock transcript,
 * ACP drawer, peek popup).
 *
 * The card owns a header (identity label + expand toggle) and a markdown
 * body. Callers keep their own renderer — `renderPromptContent` for live
 * transcript segments, `renderMarkdownBody` for the dock objective — via the
 * `render` callback, so inline code and editor buttons keep working. */

let promptCardSeq = 0;

// A prompt earns the expand control when it clearly exceeds the collapsed
// preview bound. Character and line heuristics are layout-independent, so a
// card built while detached (popup mount, JSDOM) resolves the same way.
const COLLAPSE_MIN_CHARS = 240;
const COLLAPSE_MIN_LINES = 5;

export function subagentPromptCollapsible(text) {
  const raw = typeof text === 'string' ? text.trim() : '';
  if (!raw) return false;
  return raw.length > COLLAPSE_MIN_CHARS || raw.split('\n').length > COLLAPSE_MIN_LINES;
}

export function subagentPromptExpanded(card) {
  return !card.root.classList.contains('is-collapsed');
}

export function setSubagentPromptExpanded(card, expanded) {
  card.root.classList.toggle('is-collapsed', !expanded);
  card.toggle.setAttribute('aria-expanded', String(expanded));
  card.toggle.textContent = expanded ? 'Collapse' : 'Show full prompt';
}

export function createSubagentPromptCard({ label = 'Prompt', className = '', bodyClass = '', id = '', ownerDocument } = {}) {
  const doc = ownerDocument || document;
  const body = doc.createElement('div');
  body.className = `subagent-prompt-body${bodyClass ? ` ${bodyClass}` : ''}`;
  body.id = id || `subagent-prompt-${++promptCardSeq}`;
  const toggle = doc.createElement('button');
  toggle.type = 'button';
  toggle.className = 'subagent-prompt-toggle';
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute('aria-controls', body.id);
  toggle.textContent = 'Show full prompt';
  toggle.hidden = true;
  const head = doc.createElement('div');
  head.className = 'subagent-prompt-head';
  const labelEl = doc.createElement('span');
  labelEl.className = 'subagent-prompt-label';
  labelEl.textContent = label;
  head.append(labelEl, toggle);
  const root = doc.createElement('div');
  root.className = `subagent-prompt is-collapsed${className ? ` ${className}` : ''}`;
  root.append(head, body);
  const card = { root, head, label: labelEl, toggle, body };
  root._subagentPromptCard = card;
  body._subagentPromptCard = card;
  toggle.addEventListener('click', () => setSubagentPromptExpanded(card, !subagentPromptExpanded(card)));
  return card;
}

// syncSubagentPromptText renders new prompt text through the caller's
// renderer and refreshes the expand affordance. Unchanged text leaves the
// DOM — and the reader's expanded/collapsed choice — untouched, so live
// deltas never collapse a card or force a markdown re-render.
export function syncSubagentPromptText(card, text, render) {
  const raw = typeof text === 'string' ? text : '';
  card.root.hidden = !raw.trim();
  if (card.body._subagentPromptRaw !== raw) {
    card.body._subagentPromptRaw = raw;
    render(card.body, raw);
  }
  const collapsible = subagentPromptCollapsible(raw);
  card.toggle.hidden = !collapsible;
  if (!collapsible) setSubagentPromptExpanded(card, true);
}
