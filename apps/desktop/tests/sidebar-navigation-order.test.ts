import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const rendererMarkup = readFileSync(new URL("../src/renderer/index.html", import.meta.url), "utf8");
const launcherSrc = readFileSync(new URL("../src/renderer/launcher.js", import.meta.url), "utf8");

describe("sidebar navigation order", () => {
  it("exposes a Main navigation landmark with initial Home aria-current", () => {
    const navOpen = rendererMarkup.match(/<nav class="nav-main"[^>]*>/)?.[0] ?? "";
    expect(navOpen).toContain('aria-label="Main"');

    const homeBtn = rendererMarkup.match(/<button[^>]*data-view="home"[^>]*>/)?.[0] ?? "";
    expect(homeBtn).toContain('aria-current="page"');
    expect(homeBtn).toContain("active");

    // switchView must drive aria-current via the shared helper (not class-only).
    expect(launcherSrc).toMatch(/setSidebarNavCurrent\(\$\$\("\[data-nav\]"\),\s*viewName\)/);
  });

  it("places Autostart below Plugins, Providers before Logs, and Logs last", () => {
    const navigation = rendererMarkup.match(/<nav class="nav-main"[^>]*>([\s\S]*?)<\/nav>/)?.[1] ?? "";
    const viewOrder = [...navigation.matchAll(/data-view="([^"]+)" data-nav/g)].map((match) => match[1]);

    expect(viewOrder).toEqual([
      "home",
      "agent",
      "skills",
      "learning",
      "plugins",
      "autostart",
      "ai-providers",
      "jobs",
      "pipelines",
      "logs",
    ]);
  });

  it("uses a chat icon for Agent and a CPU icon for Providers", () => {
    const agentNavigation = rendererMarkup.match(/<button[^>]*data-view="agent"[\s\S]*?<\/button>/)?.[0] ?? "";
    const providersNavigation = rendererMarkup.match(/<button[^>]*data-view="ai-providers"[\s\S]*?<\/button>/)?.[0] ?? "";

    expect(agentNavigation).toContain('d="M6 4.5h12A2.5 2.5 0 0 1 20.5 7v7A2.5 2.5 0 0 1 18 16.5H10l-4.5 3v-12A2.5 2.5 0 0 1 8 5"');
    expect(providersNavigation).toContain('<rect x="7" y="7" width="10" height="10" rx="2"');
  });

  it("keeps overview views free of large page headers while retaining their toolbars", () => {
    for (const viewName of ["home", "agent", "skills", "learning", "plugins", "autostart", "ai-providers", "logs", "jobs", "pipelines", "settings"]) {
      const section = rendererMarkup.match(new RegExp(`<section[^>]*data-view="${viewName}"[\\s\\S]*?<\\/section>`))?.[0] ?? "";
      expect(section).not.toMatch(/<(?:h1|p|div)[^>]*class="[^"]*(?:page-title|subtitle|agent-kicker|home-kicker|greeting)[^"]*"/);
    }

    expect(rendererMarkup).toContain('id="skills-install-btn"');
    expect(rendererMarkup).toContain('id="learning-refresh-btn"');
    expect(rendererMarkup).toContain('id="jobs-new-btn"');
    expect(rendererMarkup).toContain('id="pipelines-new-btn"');
  });
});
