// Minimal, dependency-free Markdown -> HTML renderer.
// Escapes all raw HTML, then renders the small inline/block subset the agent emits.

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

export function renderMarkdown(src) {
  if (!src) return '';
  const lines = src.replace(/\r\n/g, '\n').split('\n');
  const out = [];
  let i = 0;
  let listType = null;
  let inTable = false;
  let tableRows = [];

  const closeList = () => {
    if (listType) { out.push(`</${listType}>`); listType = null; }
  };
  const closeTable = () => {
    if (inTable) {
      out.push('<table><thead><tr>' + tableRows[0].map((c) => `<th>${inline(c)}</th>`).join('') + '</tr></thead><tbody>');
      for (const row of tableRows.slice(2)) {
        out.push('<tr>' + row.map((c) => `<td>${inline(c)}</td>`).join('') + '</tr>');
      }
      out.push('</tbody></table>');
      inTable = false;
      tableRows = [];
    }
  };

  const flush = (buf) => {
    if (buf.length) out.push(`<p>${buf.map(inline).join('<br>')}</p>`);
  };

  for (; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    if (!trimmed) { closeList(); closeTable(); continue; }

    // table row
    if (trimmed.startsWith('|') && trimmed.endsWith('|')) {
      const cells = trimmed.slice(1, -1).split('|').map((c) => c.trim());
      if (tableRows.length === 1 && cells.every((c) => /^:?-{2,}:?$/.test(c))) {
        tableRows.push(cells); // separator
        continue;
      }
      if (tableRows.length > 0 || (inTable && tableRows.length === 0)) {
        if (!inTable) { inTable = true; tableRows = [cells]; continue; }
        tableRows.push(cells);
        continue;
      }
      inTable = true;
      tableRows = [cells];
      continue;
    }
    closeTable();

    // headings
    const h = /^(#{1,6})\s+(.*)$/.exec(trimmed);
    if (h) { closeList(); out.push(`<h${h[1].length}>${inline(h[2])}</h${h[1].length}>`); continue; }

    // fenced code
    if (/^```/.test(trimmed)) {
      closeList();
      const buf = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i].trim())) { buf.push(escapeHtml(lines[i])); i++; }
      out.push(`<pre><code>${buf.join('\n')}</code></pre>`);
      continue;
    }

    // indented code block (4+ spaces)
    if (/^ {4}/.test(line)) {
      closeList();
      const buf = [];
      while (i < lines.length && (/^ {4}/.test(lines[i]) || lines[i].trim() === '')) {
        if (lines[i].trim() !== '') buf.push(escapeHtml(lines[i].slice(4)));
        i++;
      }
      i--;
      out.push(`<pre><code>${buf.join('\n')}</code></pre>`);
      continue;
    }

    // blockquote
    if (trimmed.startsWith('>')) {
      closeList();
      const buf = [];
      while (i < lines.length && lines[i].trim().startsWith('>')) {
        buf.push(lines[i].trim().replace(/^>\s?/, ''));
        i++;
      }
      i--;
      out.push(`<blockquote>${renderMarkdown(buf.join('\n'))}</blockquote>`);
      continue;
    }

    // unordered list
    const ul = /^[-*+]\s+(.*)$/.exec(trimmed);
    if (ul) {
      if (listType !== 'ul') { closeList(); out.push('<ul>'); listType = 'ul'; }
      out.push(`<li>${inline(ul[1])}</li>`);
      continue;
    }

    // ordered list
    const ol = /^\d+\.\s+(.*)$/.exec(trimmed);
    if (ol) {
      if (listType !== 'ol') { closeList(); out.push('<ol>'); listType = 'ol'; }
      out.push(`<li>${inline(ol[1])}</li>`);
      continue;
    }

    // horizontal rule
    if (/^(-{3,}|\*{3,})$/.test(trimmed)) { closeList(); out.push('<hr>'); continue; }

    closeList();
    const buf = [line];
    while (i + 1 < lines.length) {
      const next = lines[i + 1].trim();
      if (!next || /^(#{1,6})\s/.test(next) || /^```/.test(next) || /^[-*+]\s/.test(next) || /^\d+\.\s/.test(next) || next.startsWith('>') || /^ {4}/.test(lines[i + 1])) break;
      i++;
      buf.push(lines[i]);
    }
    flush(buf);
  }
  closeList();
  closeTable();
  return out.join('\n');
}
