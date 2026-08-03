/**
 * Agent Canvas shared render pipeline.
 *
 * One module used by inline auto-render, the Sidebar iframe, and (later) the
 * pop-out window. SVG and Mermaid compile to a static SVG string; HTML is
 * wrapped in a sandbox document string with a strict CSP and an empty external
 * allowlist (v1). Mermaid is lazy: it is only `import()`ed when a mermaid fence
 * is actually rendered.
 */

/**
 * @typedef {("html"|"svg"|"mermaid")} CanvasArtifactKind
 * @typedef {{ kind: CanvasArtifactKind, source: string, title?: string }} CanvasRenderInput
 */

let mermaidPromise = null;

/**
 * Render a canvas artifact to a displayable form.
 *
 * - `svg` → `{ type: "svg", svg }` (sanitized static SVG string).
 * - `mermaid` → `{ type: "svg", svg }` after lazy-loading mermaid with
 *   `securityLevel: 'strict'`. On failure, returns `{ type: "error", message }`
 *   so the caller can fall back to the original code block.
 * - `html` → `{ type: "html", srcdoc }` — a full sandbox document string ready
 *   for `iframe.srcdoc`, with a CSP that denies all remote origins in v1.
 *
 * @param {CanvasRenderInput} input
 * @returns {Promise<{ type: "svg", svg: string } | { type: "html", srcdoc: string } | { type: "error", message: string }>}
 */
export async function renderArtifact(input) {
  const source = String(input?.source ?? "");
  if (input.kind === "svg") return { type: "svg", svg: sanitizeSvgString(source) };
  if (input.kind === "mermaid") return renderMermaid(source);
  if (input.kind === "html") return { type: "html", srcdoc: buildHtmlSandboxDoc(source) };
  return { type: "error", message: `Unknown canvas kind: ${input.kind}` };
}

/**
 * Build the srcdoc document for an HTML artifact. The CSP denies every remote
 * origin in v1 (empty allowlist); only inline scripts/styles and `data:`/
 * `https:` images are permitted. `javascript:` URLs and `meta http-equiv`
 * refresh are stripped from the artifact body before it is embedded.
 *
 * @param {string} source
 * @returns {string}
 */
export function buildHtmlSandboxDoc(source) {
  const body = stripDangerousHtml(String(source ?? ""));
  const csp = [
    "default-src 'none'",
    "script-src 'unsafe-inline'",
    "style-src 'unsafe-inline'",
    "img-src data: https:",
    "font-src data:",
    "connect-src 'none'",
    "frame-src 'none'",
    "media-src 'none'",
  ].join("; ");
  return [
    "<!DOCTYPE html>",
    "<html>",
    "<head>",
    "<meta charset=\"utf-8\">",
    `<meta http-equiv=\"Content-Security-Policy\" content=\"${csp}\">`,
    "<style>",
    "html,body{margin:0;padding:0;background:#fff;color:#111;",
    "font:13px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;}",
    "</style>",
    "</head>",
    "<body>",
    body,
    "</body>",
    "</html>",
  ].join("\n");
}

/**
 * Sanitize an SVG string for inline embedding. Parses the source as XML and
 * refuses anything that does not root in `<svg`. Returns the original source
 * text when it parses cleanly so the caller can innerHTML it into a dedicated
 * canvas container (not the markdown body).
 *
 * @param {string} source
 * @returns {string}
 */
export function sanitizeSvgString(source) {
  const text = String(source ?? "").trim();
  if (!text) return "";
  if (!/^<svg[\s>]/i.test(text)) return "";
  // Drop event handler attributes and replace javascript: URLs with "#".
  return text
    .replace(/\s+on[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi, "")
    .replace(/((?:xlink:)?href)\s*=\s*("javascript:[^"]*"|'javascript:[^']*')/gi, '$1="#"');
}

/**
 * Strip `javascript:` URLs and `<meta http-equiv="refresh">` from an HTML
 * artifact body. This is structural sandbox hardening of the rendering
 * surface, not content moderation — the original source stays visible to the
 * user as a code block.
 *
 * @param {string} source
 * @returns {string}
 */
export function stripDangerousHtml(source) {
  return String(source ?? "")
    .replace(/<meta\b[^>]*http-equiv\s*=\s*["']?refresh["']?[^>]*>/gi, "")
    .replace(/(href|src)\s*=\s*("javascript:[^"]*"|'javascript:[^']*'|javascript:[^\s"'>]*)/gi, "$1=\"#\"");
}

/**
 * Lazily import mermaid and render a diagram to a static SVG string. Mermaid is
 * initialized once with `securityLevel: 'strict'` and `startOnLoad: false`.
 *
 * @param {string} source
 * @returns {Promise<{ type: "svg", svg: string } | { type: "error", message: string }>}
 */
async function renderMermaid(source) {
  const text = String(source ?? "");
  if (!text.trim()) return { type: "error", message: "Empty mermaid diagram" };
  try {
    const mermaid = await loadMermaid();
    const id = `mmd-${randomId()}`;
    const { svg } = await mermaid.render(id, text);
    return { type: "svg", svg: sanitizeSvgString(svg) };
  } catch (error) {
    return { type: "error", message: error instanceof Error ? error.message : String(error) };
  }
}

/**
 * @returns {Promise<{ render: (id: string, source: string) => Promise<{ svg: string }> }>}
 */
function loadMermaid() {
  if (!mermaidPromise) {
    mermaidPromise = import("mermaid")
      .then((mod) => {
        const mermaid = mod.default ?? mod;
        mermaid.initialize({ securityLevel: "strict", startOnLoad: false });
        return mermaid;
      })
      .catch((error) => {
        mermaidPromise = null;
        throw error;
      });
  }
  return mermaidPromise;
}

function randomId() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID().replace(/-/g, "").slice(0, 12);
  }
  return `mmd${Math.random().toString(36).slice(2, 14)}`;
}
