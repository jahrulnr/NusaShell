// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { setSidebarNavCurrent } from "../src/renderer/launcher-ui.js";

describe("sidebar nav aria-current (switchView / setSidebarNavCurrent, #64)", () => {
  function installSidebarNav() {
    document.body.innerHTML = `
      <nav class="nav-main" aria-label="Main">
        <button type="button" class="nav-item active" data-view="home" data-nav aria-current="page">Home</button>
        <button type="button" class="nav-item" data-view="agent" data-nav>Agent</button>
        <button type="button" class="nav-item" data-view="jobs" data-nav>Jobs</button>
        <button type="button" class="nav-item" data-view="pipelines" data-nav>Pipelines</button>
        <button type="button" class="nav-item" data-view="plugins" data-nav>Plugins</button>
      </nav>
    `;
    return {
      nav: document.querySelector('nav.nav-main[aria-label="Main"]') as HTMLElement,
      current: () =>
        [...document.querySelectorAll("[data-nav][aria-current]")].map(
          (el) => (el as HTMLElement).dataset.view,
        ),
    };
  }

  /** Mirrors switchView's nav half: setSidebarNavCurrent(querySelectorAll('[data-nav]'), view). */
  function switchView(viewName: string) {
    setSidebarNavCurrent(document.querySelectorAll("[data-nav]"), viewName);
  }

  it("keeps a stable Main landmark label", () => {
    const { nav } = installSidebarNav();
    expect(nav.getAttribute("aria-label")).toBe("Main");
    expect(nav.tagName).toBe("NAV");
  });

  it("after switchView('agent'), only Agent has aria-current=page", () => {
    installSidebarNav();
    switchView("agent");

    const agent = document.querySelector('[data-nav][data-view="agent"]') as HTMLElement;
    expect(agent.getAttribute("aria-current")).toBe("page");
    expect(agent.classList.contains("active")).toBe(true);

    const withCurrent = [...document.querySelectorAll("[data-nav][aria-current]")];
    expect(withCurrent).toHaveLength(1);
    expect(withCurrent[0]).toBe(agent);

    for (const item of document.querySelectorAll("[data-nav]")) {
      if (item === agent) continue;
      expect(item.hasAttribute("aria-current")).toBe(false);
      expect(item.classList.contains("active")).toBe(false);
    }
  });

  it("moves aria-current when switching views", () => {
    const { current } = installSidebarNav();
    expect(current()).toEqual(["home"]);

    switchView("agent");
    expect(current()).toEqual(["agent"]);

    switchView("pipelines");
    expect(current()).toEqual(["pipelines"]);

    switchView("jobs");
    expect(current()).toEqual(["jobs"]);
  });

  it("clears aria-current when the view is outside main nav (settings)", () => {
    installSidebarNav();
    switchView("agent");
    switchView("settings");

    expect(document.querySelectorAll("[data-nav][aria-current]")).toHaveLength(0);
    for (const item of document.querySelectorAll("[data-nav]")) {
      expect(item.classList.contains("active")).toBe(false);
    }
  });
});
