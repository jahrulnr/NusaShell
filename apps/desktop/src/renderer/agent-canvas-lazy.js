/**
 * Lazy reveal for Agent Canvas inline artifacts.
 *
 * Renders an in-message canvas artifact (svg / mermaid / html) only when its
 * element approaches the viewport, instead of eagerly rendering every artifact
 * in a long thread on load. A lightweight placeholder is shown until a shared
 * IntersectionObserver fires, then `onReveal()` runs once and the observer is
 * disconnected.
 *
 * The renderer degrades gracefully: when `IntersectionObserver` is unavailable
 * the artifact is revealed immediately (previous eager behavior).
 */

/**
 * Create and mount a lazy placehholder + IntersectionObserver-driven reveal.
 *
 * @param {object} opts
 * @param {HTMLElement} opts.host - Element to contain the placeholder (the
 *   fence `pre` parent, or the html card container).
 * @param {() => HTMLElement|null} opts.getContainer - Returns the rendered
 *   container once it exists (used to decide "already rendered").
 * @param {() => (void|Promise<void>)} opts.onReveal - Runs the render. Called
 *   at most once.
 * @param {Element|Document|null} [opts.root] - IntersectionObserver root,
 *   typically the thread element.
 * @param {string} [opts.rootMargin] - IO rootMargin (default "80px 0px").
 * @param {number} [opts.threshold] - IO threshold (default 0).
 * @returns {{ placeholder: HTMLElement|null, dispose: () => void, revealed: boolean }}
 */
export function bindLazyCanvasReveal({ host, getContainer, onReveal, root = null, rootMargin = "80px 0px", threshold = 0 }) {
  let done = false;
  let revealed = false;
  let io = null;
  let placeholder = null;

  const finish = () => {
    if (done) return;
    done = true;
    if (io) {
      io.disconnect();
      io = null;
    }
    placeholder?.remove();
    placeholder = null;
  };

  const reveal = () => {
    if (done || revealed) return;
    revealed = true;
    try {
      const result = onReveal();
      if (result && typeof result.then === "function") {
        result.catch(() => undefined);
      }
    } finally {
      finish();
    }
  };

  // If the container already exists (e.g. streaming re-decoration) there is
  // nothing to do.
  if (getContainer?.()) {
    done = true;
    return { placeholder: null, dispose: () => {}, revealed: false };
  }

  const ioCtor = typeof IntersectionObserver !== "undefined" ? IntersectionObserver : null;
  if (ioCtor) {
    placeholder = document.createElement("div");
    placeholder.className = "agent-canvas-lazy-placeholder";
    const label = document.createElement("span");
    label.className = "agent-canvas-lazy-label";
    label.textContent = "Artifact preview deferred until visible";
    const loadBtn = document.createElement("button");
    loadBtn.type = "button";
    loadBtn.className = "agent-canvas-fence-btn is-primary";
    loadBtn.textContent = "Load preview";
    loadBtn.addEventListener("click", () => reveal());
    placeholder.append(label, loadBtn);
    host.appendChild(placeholder);

    io = new ioCtor(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            reveal();
            break;
          }
        }
      },
      { root, rootMargin, threshold },
    );
    io.observe(placeholder);
  } else {
    // Fallback: no observer -> render immediately.
    reveal();
  }

  return {
    placeholder,
    dispose: finish,
    revealed,
  };
}
