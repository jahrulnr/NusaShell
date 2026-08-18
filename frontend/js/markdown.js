// Minimal, dependency-free Markdown -> HTML renderer.
// Escapes all raw HTML, then renders the small inline/block subset the agent emits.
//
// parseBlocks returns top-level blocks annotated with data-start/data-end
// (byte offsets into the source). This enables incremental rendering: only
// blocks whose byte range changed are re-rendered; unchanged blocks —
// including rendered Mermaid SVGs — are preserved across streaming deltas.

function escapeHtml(s) {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function inline(text) {
  let out = escapeHtml(text);
  // code spans (before other inline rules so backticks stay literal)
  out = out.replace(/`([^`]+)`/g, (_, code) => `<code>${code}</code>`);
  // bold / italic
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>');
  // links
  out = out.replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  return out;
}

// parseBlocks parses markdown source into a list of top-level blocks, each
// annotated with its byte range in the source. The byte range is emitted as
// data-start/data-end attributes on the block's root element, enabling
// incremental rendering (see js/incremental-render.js).
//
// Block kinds: p, pre, mermaid, h1-h6, ul, ol, blockquote, hr, table.
// Each block: { start, end, html } where start/end are byte offsets.
export function parseBlocks(src) {
  if (!src) return [];
  const normalized = src.replace(/\r\n/g, '\n');
  const lines = normalized.split('\n');

  // Precompute byte offset of each line start.
  const lineOffset = new Array(lines.length);
  let off = 0;
  for (let i = 0; i < lines.length; i++) {
    lineOffset[i] = off;
    off += lines[i].length + 1; // +1 for \n
  }
  const lineEnd = (i) => lineOffset[i] + lines[i].length;

  const blocks = [];
  let i = 0;
  let listType = null;
  let listStartOff = 0;
  let listItems = [];
  let inTable = false;
  let tableStartOff = 0;
  let tableRows = [];

  const closeList = () => {
    if (listType) {
      const end = lineEnd(i - 1);
      blocks.push({
        start: listStartOff,
        end,
        html: `<${listType} data-start="${listStartOff}" data-end="${end}">${listItems.join('')}</${listType}>`,
      });
      listType = null;
      listItems = [];
    }
  };

  const closeTable = () => {
    if (inTable) {
      const end = lineEnd(i - 1);
      let html = `<table data-start="${tableStartOff}" data-end="${end}"><thead><tr>` +
        tableRows[0].map((c) => `<th>${inline(c)}</th>`).join('') + '</tr></thead><tbody>';
      for (const row of tableRows.slice(2)) {
        html += '<tr>' + row.map((c) => `<td>${inline(c)}</td>`).join('') + '</tr>';
      }
      html += '</tbody></table>';
      blocks.push({ start: tableStartOff, end, html });
      inTable = false;
      tableRows = [];
    }
  };

  for (; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();
    const ls = lineOffset[i];
    const le = lineEnd(i);

    if (!trimmed) { closeList(); closeTable(); continue; }

    // table row
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      const cells = trimmed.slice(1, -1).split('|').map((c) => c.trim());
      if (tableRows.length === 1 && cells.every((c) => /^:?-{2,}:?$/.test(c))) {
        tableRows.push(cells);
        continue;
      }
      if (tableRows.length > 0 || (inTable && tableRows.length === 0)) {
        if (!inTable) { inTable = true; tableRows = [cells]; tableStartOff = ls; continue; }
        tableRows.push(cells);
        continue;
      }
      inTable = true;
      tableRows = [cells];
      tableStartOff = ls;
      continue;
    }
    closeTable();

    // headings
    const h = /^(#{1,6})\s+(.*)$/.exec(trimmed);
    if (h) {
      closeList();
      blocks.push({ start: ls, end: le, html: `<h${h[1].length} data-start="${ls}" data-end="${le}">${inline(h[2])}</h${h[1].length}>` });
      continue;
    }

    // fenced code
    if (/^```/.test(trimmed)) {
      closeList();
      const lang = (/^```\s*([A-Za-z0-9_+-]+)/.exec(trimmed)?.[1] || '').toLowerCase();
      const buf = [];
      const fenceStart = ls;
      i++;
      while (i < lines.length && !/^```/.test(lines[i].trim())) { buf.push(escapeHtml(lines[i])); i++; }
      let fenceEnd;
      let complete;
      if (i < lines.length) {
        // closing fence found
        fenceEnd = lineEnd(i);
        complete = true;
      } else {
        // no closing fence — incomplete block, end at last line
        fenceEnd = lineEnd(i - 1);
        complete = false;
        i--; // compensate for loop increment
      }
      const completeAttr = complete ? ' data-complete="true"' : ' data-complete="false"';
      if (lang === 'mermaid') {
        // Emit a placeholder holding the raw diagram source. It is NOT rendered
        // to SVG here — the renderer (js/mermaid-render.js) turns it into a
        // diagram when the fence is complete (data-complete="true"), so live
        // streaming deltas keep re-emitting a cheap placeholder instead of
        // re-rendering the diagram.
        blocks.push({
          start: fenceStart,
          end: fenceEnd,
          html: `<div class="mermaid-block" data-start="${fenceStart}" data-end="${fenceEnd}"${completeAttr}><pre class="mermaid-src">${buf.join('\n')}</pre></div>`,
        });
      } else {
        blocks.push({ start: fenceStart, end: fenceEnd, html: `<pre data-start="${fenceStart}" data-end="${fenceEnd}"><code>${buf.join('\n')}</code></pre>` });
      }
      continue;
    }

    // indented code block (4+ spaces)
    if (/^ {4}/.test(line)) {
      closeList();
      const buf = [];
      const blockStart = ls;
      while (i < lines.length && (/^ {4}/.test(lines[i]) || lines[i].trim() === '')) {
        if (lines[i].trim() !== '') buf.push(escapeHtml(lines[i].slice(4)));
        i++;
      }
      i--;
      const blockEnd = lineEnd(i);
      blocks.push({ start: blockStart, end: blockEnd, html: `<pre data-start="${blockStart}" data-end="${blockEnd}"><code>${buf.join('\n')}</code></pre>` });
      continue;
    }

    // blockquote
    if (trimmed.startsWith('>')) {
      closeList();
      const buf = [];
      const blockStart = ls;
      while (i < lines.length && lines[i].trim().startsWith('>')) {
        buf.push(lines[i].trim().replace(/^>\s?/, ''));
        i++;
      }
      i--;
      const blockEnd = lineEnd(i);
      // Recursive call — inner blocks get their own byte ranges relative to
      // the blockquote content. The anchors are harmless in non-incremental
      // context (blockquote content is settled, not streamed).
      const inner = parseBlocks(buf.join('\n')).map((b) => b.html).join('\n');
      blocks.push({ start: blockStart, end: blockEnd, html: `<blockquote data-start="${blockStart}" data-end="${blockEnd}">${inner}</blockquote>` });
      continue;
    }

    // unordered list
    const ul = /^[-*+]\s+(.*)$/.exec(trimmed);
    if (ul) {
      if (listType !== 'ul') { closeList(); listType = 'ul'; listStartOff = ls; }
      listItems.push(`<li>${inline(ul[1])}</li>`);
      continue;
    }

    // ordered list
    const ol = /^\d+\.\s+(.*)$/.exec(trimmed);
    if (ol) {
      if (listType !== 'ol') { closeList(); listType = 'ol'; listStartOff = ls; }
      listItems.push(`<li>${inline(ol[1])}</li>`);
      continue;
    }

    // horizontal rule
    if (/^(-{3,}|\*{3,})$/.test(trimmed)) {
      closeList();
      blocks.push({ start: ls, end: le, html: `<hr data-start="${ls}" data-end="${le}">` });
      continue;
    }

    closeList();
    // paragraph — collect consecutive non-empty, non-special lines
    const buf = [line];
    const blockStart = ls;
    while (i + 1 < lines.length) {
      const next = lines[i + 1].trim();
      if (!next || /^(#{1,6})\s/.test(next) || /^```/.test(next) || /^[-*+]\s/.test(next) || /^\d+\.\s/.test(next) || next.startsWith('>') || /^ {4}/.test(lines[i + 1])) break;
      i++;
      buf.push(lines[i]);
    }
    const blockEnd = lineEnd(i);
    blocks.push({ start: blockStart, end: blockEnd, html: `<p data-start="${blockStart}" data-end="${blockEnd}">${buf.map(inline).join('<br>')}</p>` });
  }
  closeList();
  closeTable();
  return blocks;
}

// renderMarkdown is the backward-compatible full-render entry point. It
// delegates to parseBlocks and joins the HTML. The data-start/data-end
// attributes are harmless in non-incremental context.
export function renderMarkdown(src) {
  return parseBlocks(src).map((b) => b.html).join('\n');
}
