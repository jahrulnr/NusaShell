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
  const text = softenMermaidSequenceRects(String(source ?? ""));
  if (!text.trim()) return { type: "error", message: "Empty mermaid diagram" };
  const id = `mmd-${randomId()}`;
  // Host off-layout so any Mermaid temp nodes never reflow the shell UI. Mermaid
  // defaults to appending `#d{id}` under document.body; a parse failure without
  // suppressErrorRendering leaves a huge error diagram (viewBox 2412×512) there.
  const host = typeof document !== "undefined" ? document.createElement("div") : null;
  if (host) {
    host.setAttribute("data-mermaid-render-host", id);
    host.setAttribute("aria-hidden", "true");
    host.style.cssText = "position:absolute;left:-10000px;top:0;width:0;height:0;overflow:hidden;pointer-events:none;";
    document.body.appendChild(host);
  }
  try {
    const mermaid = await loadMermaid();
    // Prefer the 3-arg form so temp SVG lives under `host`, not the page root.
    const { svg } = host
      ? await mermaid.render(id, text, host)
      : await mermaid.render(id, text);
    // Never surface Mermaid's built-in error diagram as a "successful" SVG.
    if (isMermaidErrorDiagramSvg(svg)) {
      return { type: "error", message: "Mermaid syntax error" };
    }
    return { type: "svg", svg: softenMermaidSvgRects(sanitizeSvgString(svg)) };
  } catch (error) {
    return { type: "error", message: error instanceof Error ? error.message : String(error) };
  } finally {
    removeMermaidRenderArtifacts(id, host);
  }
}

/**
 * Detect Mermaid's fallback error diagram (bomb + "Syntax error in text").
 * That diagram is enormous and must never be mounted into chat UI.
 *
 * @param {string} svg
 * @returns {boolean}
 */
export function isMermaidErrorDiagramSvg(svg) {
  // Mermaid includes `.error-icon` and `.error-text` CSS definitions in valid
  // SVGs too. Strip styles before inspecting rendered markup, otherwise every
  // successful diagram is falsely classified as an error.
  const text = String(svg ?? "").replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, "");
  if (!text) return false;
  if (text.includes("Syntax error in text")) return true;
  if (text.includes("error-icon") && text.includes("error-text")) return true;
  return false;
}

/**
 * Remove Mermaid temp nodes that can survive a thrown parse/render failure.
 * Mermaid IDs: svg=`id`, enclosing div=`d{id}`, sandbox iframe=`i{id}`.
 *
 * @param {string} id
 * @param {HTMLElement | null} host
 */
function removeMermaidRenderArtifacts(id, host) {
  if (typeof document === "undefined") return;
  for (const nodeId of [id, `d${id}`, `i${id}`]) {
    document.getElementById(nodeId)?.remove();
  }
  host?.remove();
  document.querySelectorAll(`[data-mermaid-render-host="${CSS.escape ? CSS.escape(id) : id}"]`).forEach((node) => node.remove());
}

/**
 * Mermaid sequence `rect rgb(r, g, b)` paints fully opaque backgrounds that
 * often cover arrows/messages (looks like a "leak"). Rewrite opaque rgb/hsl
 * rect fills to translucent rgba/hsla before render. Leave rects that already
 * specify alpha alone.
 *
 * @param {string} source
 * @returns {string}
 */
export function softenMermaidSequenceRects(source) {
  return String(source ?? "")
    .replace(
      /^([ \t]*rect[ \t]+)rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)\s*$/gim,
      "$1rgba($2, $3, $4, 0.18)",
    )
    .replace(
      /^([ \t]*rect[ \t]+)hsl\(\s*([\d.]+)\s*,\s*([\d.]+%)\s*,\s*([\d.]+%)\s*\)\s*$/gim,
      "$1hsla($2, $3, $4, 0.18)",
    );
}

/**
 * Defense in depth: if Mermaid still emits opaque activation/background rects,
 * force a translucent fill-opacity on filled <rect> nodes that lack one.
 *
 * @param {string} svg
 * @returns {string}
 */
export function softenMermaidSvgRects(svg) {
  const text = String(svg ?? "");
  if (!text) return text;
  return text.replace(/<rect\b([^>]*)>/gi, (full, attrs) => {
    const selfClosing = /\/\s*$/.test(attrs);
    const cleanAttrs = attrs.replace(/\/\s*$/, "");
    if (/\bfill-opacity\s*=/i.test(cleanAttrs)) return full;
    if (/\bopacity\s*=/i.test(cleanAttrs)) return full;
    // Skip hollow rects (no fill / fill=none).
    const fillMatch = cleanAttrs.match(/\bfill\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))/i);
    const fill = (fillMatch?.[2] ?? fillMatch?.[3] ?? fillMatch?.[4] ?? "").trim();
    if (!fill || /^none$/i.test(fill)) return full;
    return `<rect${cleanAttrs} fill-opacity="0.18"${selfClosing ? " /" : ""}>`;
  });
}

/**
 * @returns {Promise<{ render: (id: string, source: string) => Promise<{ svg: string }> }>}
 */
function loadMermaid() {
  if (!mermaidPromise) {
    mermaidPromise = import("mermaid")
      .then((mod) => {
        const mermaid = mod.default ?? mod;
        mermaid.initialize({
          securityLevel: "strict",
          startOnLoad: false,
          // On parse failure, throw + strip temp DOM instead of injecting Mermaid's
          // giant built-in error SVG into the page (breaks Electron layout).
          suppressErrorRendering: true,
          theme: "neutral",
          // Keep sequence activation/rects from painting solid walls over arrows.
          themeVariables: {
            actorBkg: "#e8eef7",
            actorBorder: "#7a8aa0",
            actorTextColor: "#1a1a1a",
            signalColor: "#1a1a1a",
            signalTextColor: "#1a1a1a",
            noteBkgColor: "#fff6c8",
            noteTextColor: "#1a1a1a",
            activationBkgColor: "rgba(90, 130, 180, 0.18)",
            activationBorderColor: "#7a8aa0",
          },
        });
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

const CANVAS_ZOOM_MIN = 0.4;
const CANVAS_ZOOM_MAX = 3;
const CANVAS_ZOOM_STEP = 0.1;

/**
 * Bind Ctrl/Cmd + wheel zoom on an SVG canvas host. Double-click resets to 100%.
 * Idempotent: safe to call twice on the same element.
 *
 * Zooms by resizing the SVG (viewBox stays put) so Chromium re-rasterizes as a
 * vector — CSS `transform: scale()` would promote a bitmap layer and look like
 * a stretched PNG when zooming in.
 *
 * @param {HTMLElement | null | undefined} host
 */
export function bindCanvasZoom(host) {
  if (!host || host.dataset.canvasZoomBound === "1") return;
  host.dataset.canvasZoomBound = "1";
  host.classList.add("agent-canvas-zoomable");
  host.title = host.title || "Ctrl + scroll to zoom · double-click to reset";

  let stage = host.querySelector(":scope > .agent-canvas-zoom-stage");
  if (!stage) {
    stage = document.createElement("div");
    stage.className = "agent-canvas-zoom-stage";
    while (host.firstChild) stage.appendChild(host.firstChild);
    host.appendChild(stage);
  }

  let label = host.querySelector(":scope > .agent-canvas-zoom-label");
  if (!label) {
    label = document.createElement("div");
    label.className = "agent-canvas-zoom-label";
    label.hidden = true;
    label.setAttribute("aria-live", "polite");
    host.appendChild(label);
  }

  let scale = 1;
  let hideTimer = 0;

  const applyScale = (next) => {
    scale = Math.min(CANVAS_ZOOM_MAX, Math.max(CANVAS_ZOOM_MIN, next));
    const svg = stage.querySelector("svg");
    if (svg) {
      applySvgZoom(svg, scale);
      stage.style.zoom = "";
      stage.style.transform = "";
    } else {
      // Non-SVG fallback (rare): Chromium `zoom` reflows sharper than transform.
      stage.style.transform = "";
      stage.style.zoom = String(scale);
    }
    label.textContent = `${Math.round(scale * 100)}%`;
    label.hidden = false;
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(() => {
      label.hidden = true;
    }, 900);
  };

  host.addEventListener(
    "wheel",
    (event) => {
      if (!event.ctrlKey && !event.metaKey) return;
      event.preventDefault();
      event.stopPropagation();
      const direction = event.deltaY < 0 ? 1 : -1;
      applyScale(scale + direction * CANVAS_ZOOM_STEP);
    },
    { passive: false },
  );

  host.addEventListener("dblclick", (event) => {
    if (event.target.closest(".agent-canvas-zoom-label")) return;
    applyScale(1);
  });
}

/**
 * @param {SVGSVGElement} svg
 * @param {number} scale
 */
function applySvgZoom(svg, scale) {
  const base = ensureSvgZoomBase(svg);
  const width = base.width * scale;
  const height = base.height * scale;
  svg.setAttribute("width", String(width));
  svg.setAttribute("height", String(height));
  svg.style.width = `${width}px`;
  svg.style.height = `${height}px`;
  svg.style.maxWidth = "none";
}

/**
 * @param {SVGSVGElement} svg
 * @returns {{ width: number, height: number }}
 */
function ensureSvgZoomBase(svg) {
  if (svg.dataset.zoomBaseW && svg.dataset.zoomBaseH) {
    return {
      width: Number(svg.dataset.zoomBaseW),
      height: Number(svg.dataset.zoomBaseH),
    };
  }

  const viewBox = svg.viewBox?.baseVal;
  let width = viewBox && viewBox.width > 0 ? viewBox.width : 0;
  let height = viewBox && viewBox.height > 0 ? viewBox.height : 0;

  if (!width || !height) {
    const attrW = Number.parseFloat(svg.getAttribute("width") || "");
    const attrH = Number.parseFloat(svg.getAttribute("height") || "");
    if (Number.isFinite(attrW) && attrW > 0) width = attrW;
    if (Number.isFinite(attrH) && attrH > 0) height = attrH;
  }

  if (!width || !height) {
    try {
      const box = svg.getBBox();
      if (box.width > 0) width = box.width;
      if (box.height > 0) height = box.height;
    } catch {
      // Not rendered yet.
    }
  }

  if (!width) width = svg.clientWidth || 400;
  if (!height) height = svg.clientHeight || Math.round(width * 0.6);

  svg.dataset.zoomBaseW = String(width);
  svg.dataset.zoomBaseH = String(height);
  return { width, height };
}
