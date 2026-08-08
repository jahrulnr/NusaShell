// Minimal accessible toast notifications for the desktop renderer.

export const TOAST_MAX_CHARS = 140;
export const TOAST_MAX_ACTIVE = 4;
export const TOAST_DURATION_MS = 4000;
export const TOAST_FADE_MS = 300;

/**
 * Ensure the toast host is a polite live region (safe to call repeatedly).
 * @param {HTMLElement | null | undefined} container
 */
export function ensureToastContainerA11y(container) {
  if (!container) return;
  container.setAttribute("aria-live", "polite");
  container.setAttribute("aria-atomic", "false");
}

/**
 * @param {HTMLElement} toast
 * @param {{ immediate?: boolean }} [opts]
 */
export function dismissToast(toast, opts = {}) {
  if (!toast || toast.dataset.dismissing === "1") return;
  toast.dataset.dismissing = "1";
  const hideTimer = toast._hideTimer;
  const removeTimer = toast._removeTimer;
  if (hideTimer) clearTimeout(hideTimer);
  if (removeTimer) clearTimeout(removeTimer);
  toast._hideTimer = null;
  toast._removeTimer = null;

  if (opts.immediate) {
    toast.remove();
    return;
  }
  toast.classList.remove("toast-show");
  toast._removeTimer = setTimeout(() => {
    toast.remove();
  }, TOAST_FADE_MS);
}

/**
 * @param {string} message
 * @param {"info" | "success" | "error"} [type]
 * @param {{ container?: HTMLElement | null, durationMs?: number }} [options]
 * @returns {HTMLElement | null}
 */
export function showToast(message, type = "info", options = {}) {
  const container = options.container ?? document.getElementById("toast-container");
  if (!container) return null;
  ensureToastContainerA11y(container);

  while (container.querySelectorAll(".toast:not([data-dismissing='1'])").length >= TOAST_MAX_ACTIVE) {
    const oldest = container.querySelector(".toast:not([data-dismissing='1'])");
    if (!oldest) break;
    dismissToast(/** @type {HTMLElement} */ (oldest), { immediate: true });
  }

  const toast = document.createElement("div");
  const kind = type === "error" || type === "success" ? type : "info";
  toast.className = `toast toast-${kind}`;
  toast.setAttribute("role", kind === "error" ? "alert" : "status");

  const full = String(message ?? "").replace(/\s+/g, " ").trim();
  const clipped = full.length > TOAST_MAX_CHARS ? `${full.slice(0, TOAST_MAX_CHARS - 1)}…` : full;

  const text = document.createElement("span");
  text.className = "toast-message";
  text.textContent = clipped;
  if (clipped !== full) text.title = full;

  const dismiss = document.createElement("button");
  dismiss.type = "button";
  dismiss.className = "toast-dismiss";
  dismiss.setAttribute("aria-label", "Dismiss notification");
  dismiss.textContent = "×";
  dismiss.addEventListener("click", () => dismissToast(toast));

  toast.append(text, dismiss);
  container.appendChild(toast);
  setTimeout(() => {
    if (toast.dataset.dismissing !== "1") toast.classList.add("toast-show");
  }, 10);

  const durationMs = options.durationMs ?? TOAST_DURATION_MS;
  toast._hideTimer = setTimeout(() => {
    dismissToast(toast);
  }, durationMs);

  return toast;
}
