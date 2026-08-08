// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  TOAST_MAX_ACTIVE,
  dismissToast,
  showToast,
} from "../src/renderer/toast.js";

function installDom() {
  document.body.innerHTML = `<div class="toast-container" id="toast-container" aria-live="polite" aria-atomic="false"></div>`;
}

describe("toast accessibility and lifecycle (#63)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    installDom();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps the toast container as a polite live region", () => {
    const container = document.getElementById("toast-container") as HTMLElement;
    showToast("hello", "info");
    expect(container.getAttribute("aria-live")).toBe("polite");
    expect(container.getAttribute("aria-atomic")).toBe("false");
  });

  it("uses role=alert for errors and role=status for info/success", () => {
    showToast("oops", "error");
    showToast("ok", "success");
    showToast("note", "info");
    const roles = [...document.querySelectorAll(".toast")].map((t) => t.getAttribute("role"));
    expect(roles).toEqual(["alert", "status", "status"]);
  });

  it("dismisses a toast immediately via the dismiss control", () => {
    const toast = showToast("stay", "error");
    expect(document.querySelectorAll(".toast")).toHaveLength(1);
    const btn = toast?.querySelector(".toast-dismiss") as HTMLButtonElement;
    expect(btn.getAttribute("aria-label")).toBe("Dismiss notification");
    btn.click();
    // Fade removal still scheduled; immediate click marks dismissing then removes after fade
    // unless we advanced timers — dismiss removes after fade unless immediate path.
    vi.advanceTimersByTime(300);
    expect(document.querySelectorAll(".toast")).toHaveLength(0);
  });

  it("caps active toasts and evicts the oldest on burst", () => {
    for (let i = 0; i < TOAST_MAX_ACTIVE + 3; i += 1) {
      showToast(`msg-${i}`, i % 2 === 0 ? "error" : "info");
    }
    const toasts = [...document.querySelectorAll(".toast")];
    expect(toasts.length).toBeLessThanOrEqual(TOAST_MAX_ACTIVE);
    expect(toasts.map((t) => t.querySelector(".toast-message")?.textContent)).toEqual(
      Array.from({ length: TOAST_MAX_ACTIVE }, (_, i) => `msg-${i + 3}`),
    );
  });

  it("auto-dismisses after the duration without manual action", () => {
    showToast("timed", "info", { durationMs: 1000 });
    expect(document.querySelectorAll(".toast")).toHaveLength(1);
    vi.advanceTimersByTime(1000);
    // starts fade-out
    vi.advanceTimersByTime(300);
    expect(document.querySelectorAll(".toast")).toHaveLength(0);
  });

  it("cancels auto-dismiss timers when dismissed manually", () => {
    const toast = showToast("early", "error", { durationMs: 5000 }) as HTMLElement;
    dismissToast(toast, { immediate: true });
    expect(document.querySelectorAll(".toast")).toHaveLength(0);
    vi.advanceTimersByTime(10_000);
    expect(document.querySelectorAll(".toast")).toHaveLength(0);
  });
});
