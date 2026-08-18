// Incremental markdown renderer using byte-range anchored blocks.
//
// Each top-level block produced by parseBlocks() carries data-start and
// data-end attributes — the byte range of the block in the raw markdown
// source. During streaming, deltas only append text, so:
//
//   - A block whose (start, end) is unchanged across deltas is skipped.
//     This is what keeps rendered Mermaid SVGs from being re-rendered
//     while subsequent text deltas continue to arrive (the ChatGPT pattern:
//     render at fence-close, then "lock" the block).
//   - A block whose end grew (e.g., an incomplete fence receiving more
//     lines, or a paragraph absorbing more text) is re-rendered in place.
//   - A new block (a start that didn't exist before) is inserted.
//   - An orphaned block (start no longer present) is removed.
//
// The net effect is that only changed blocks touch the DOM. Unchanged
// blocks — including rendered Mermaid diagrams — are preserved across
// deltas without any explicit caching or lock mechanism.

import { parseBlocks } from './markdown.js';

function htmlToElement(html) {
  const template = document.createElement('template');
  template.innerHTML = html.trim();
  return template.content.firstElementChild;
}

// incrementalRender diffs the new parse against the existing DOM children
// and updates only the blocks that changed. Non-block children (those
// without data-start) are left untouched — the caller is responsible for
// removing thinking dots, retry banners, etc. before calling this.
export function incrementalRender(container, raw) {
  if (!container) return;
  const newBlocks = parseBlocks(raw);

  // Snapshot of existing block children (those with data-start).
  const oldBlockChildren = [...container.children].filter(
    (el) => el.dataset.start != null,
  );
  const oldMap = new Map();
  for (const el of oldBlockChildren) {
    oldMap.set(el.dataset.start, el);
  }

  // Track which old elements are still in use (either preserved or replaced).
  const used = new Set();
  let prevEl = null;

  for (const block of newBlocks) {
    const startKey = String(block.start);
    const oldEl = oldMap.get(startKey);

    if (oldEl && oldEl.dataset.end === String(block.end)) {
      // Unchanged — preserve DOM node. This is the "lock": rendered
      // Mermaid SVGs and any other settled content stay in place.
      used.add(oldEl);
      prevEl = oldEl;
      continue;
    }

    // Changed or new — create fresh element.
    const newEl = htmlToElement(block.html);
    if (!newEl) continue;

    if (oldEl) {
      // Replace in place — byte range start matches but end differs
      // (block grew, e.g. fence received more lines before closing).
      oldEl.replaceWith(newEl);
      used.add(newEl);
    } else if (prevEl) {
      // New block — insert after the previous block to maintain order.
      prevEl.after(newEl);
      used.add(newEl);
    } else {
      // First block — prepend (non-block children, if any, come after).
      container.prepend(newEl);
      used.add(newEl);
    }
    prevEl = newEl;
  }

  // Remove orphaned block children (blocks that no longer exist in the
  // new parse). In streaming this is rare, but can happen if a block
  // type changes (e.g., a paragraph becomes a fence).
  for (const el of oldBlockChildren) {
    if (!used.has(el)) el.remove();
  }
}
