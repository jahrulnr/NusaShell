/**
 * Agent Canvas fence extraction.
 *
 * Scans assistant Markdown for fenced code blocks tagged as canvas media
 * (`html`/`htm`, `svg`, `mermaid`) and returns one candidate per fence in
 * document order. Candidates power both inline auto-render (svg/mermaid) and
 * the HTML Preview action.
 */

export const CANVAS_FENCE_LANGS = new Set(["html", "htm", "svg", "mermaid"]);

export const CANVAS_ARTIFACT_MAX_SOURCE_BYTES = 512 * 1024;

/**
 * Resolve the canvas kind for a fence language, or `null` when the language is
 * not a canvas medium. `htm` is treated as `html`.
 *
 * @param {string} lang
 * @returns {"html" | "svg" | "mermaid" | null}
 */
export function canvasKindForLang(lang) {
  const normalized = String(lang ?? "").trim().toLocaleLowerCase();
  if (normalized === "html" || normalized === "htm") return "html";
  if (normalized === "svg") return "svg";
  if (normalized === "mermaid") return "mermaid";
  return null;
}

/**
 * Extract canvas candidates from assistant Markdown.
 *
 * `fenceIndex` counts only canvas-tagged fences, in document order (0,1,2…).
 * Each candidate carries a `tooLarge` flag when its body exceeds the per-
 * artifact byte cap so the UI can toast instead of silently truncating.
 *
 * @param {string} markdown
 * @returns {Array<{ lang: string, kind: "html"|"svg"|"mermaid", source: string, fenceIndex: number, title: string, tooLarge: boolean }>}
 */
export function extractCanvasCandidates(markdown) {
  const text = String(markdown ?? "");
  const candidates = [];
  let fenceIndex = 0;
  let position = 0;

  while (position < text.length) {
    const fenceStart = findFenceOpen(text, position);
    if (fenceStart === -1) break;

    const langMatch = text.slice(fenceStart).match(/^`{3,}([^\n\r]*)/);
    const lang = langMatch ? langMatch[1].trim() : "";
    const kind = canvasKindForLang(lang);
    const fenceMarkerMatch = text.slice(fenceStart).match(/^`{3,}/);
    const fenceMarker = fenceMarkerMatch ? fenceMarkerMatch[0] : "```";
    const bodyStart = fenceStart + fenceMarker.length + (langMatch ? langMatch[1].length : 0);
    const lineEnd = text.indexOf("\n", bodyStart);
    const contentStart = lineEnd === -1 ? text.length : lineEnd + 1;
    const fenceEnd = findFenceClose(text, contentStart, fenceMarker);

    if (!kind) {
      // Not a canvas fence — skip past it so nested non-canvas fences don't
      // confuse the scanner, but still advance past the close if present.
      position = fenceEnd === -1 ? text.length : fenceEnd + fenceMarker.length;
      continue;
    }

    const source = fenceEnd === -1
      ? text.slice(contentStart)
      : text.slice(contentStart, fenceEnd);
    candidates.push({
      lang,
      kind,
      source,
      fenceIndex,
      title: titleForKind(kind, fenceIndex),
      tooLarge: source.length > CANVAS_ARTIFACT_MAX_SOURCE_BYTES,
    });
    fenceIndex += 1;
    position = fenceEnd === -1 ? text.length : fenceEnd + fenceMarker.length;
  }

  return candidates;
}

/**
 * Build the stable artifact id for a fence candidate.
 *
 * @param {string} conversationId
 * @param {string} sourceMessageId
 * @param {number} fenceIndex
 * @returns {string}
 */
export function canvasArtifactId(conversationId, sourceMessageId, fenceIndex) {
  return `${conversationId}:${sourceMessageId}:${fenceIndex}`;
}

/**
 * @param {string} text
 * @param {number} from
 * @returns {number}
 */
function findFenceOpen(text, from) {
  let searchFrom = from;
  while (searchFrom < text.length) {
    const candidate = text.indexOf("```", searchFrom);
    if (candidate === -1) return -1;
    // A fence must start a line: at the document start or right after a newline
    // (CommonMark allows up to three leading spaces, which we tolerate).
    if (candidate === 0 || text[candidate - 1] === "\n") return candidate;
    const lineStart = text.lastIndexOf("\n", candidate - 1) + 1;
    const indent = candidate - lineStart;
    if (indent > 0 && indent <= 3 && text.slice(lineStart, candidate).trim() === "") return candidate;
    searchFrom = candidate + 3;
  }
  return -1;
}

/**
 * @param {string} text
 * @param {number} from
 * @param {string} marker
 * @returns {number}
 */
function findFenceClose(text, from, marker) {
  let searchFrom = from;
  while (searchFrom < text.length) {
    const candidate = text.indexOf(marker, searchFrom);
    if (candidate === -1) return -1;
    const after = candidate + marker.length;
    const nextChar = text[after];
    // A closing fence is the marker at line start followed by newline or EOF.
    if (nextChar === "\n" || nextChar === "\r" || nextChar === undefined) {
      return candidate;
    }
    searchFrom = after;
  }
  return -1;
}

function titleForKind(kind, fenceIndex) {
  return `${kind} ${fenceIndex + 1}`;
}
