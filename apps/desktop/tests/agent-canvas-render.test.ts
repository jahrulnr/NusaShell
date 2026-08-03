import { describe, expect, it } from "vitest";
import {
  buildHtmlSandboxDoc,
  renderArtifact,
  sanitizeSvgString,
  stripDangerousHtml,
} from "../src/renderer/agent-canvas-render.js";

describe("agent canvas render pipeline", () => {
  it("sanitizes an SVG string by stripping event handlers and javascript: URLs", () => {
    const svg = '<svg onload="alert(1)"><a xlink:href="javascript:alert(1)"><circle/></a></svg>';
    expect(sanitizeSvgString(svg)).toBe('<svg><a xlink:href="#"><circle/></a></svg>');
  });

  it("refuses a non-svg root", () => {
    expect(sanitizeSvgString("<div>nope</div>")).toBe("");
    expect(sanitizeSvgString("")).toBe("");
  });

  it("builds an HTML sandbox doc with a strict CSP and no remote script origins", () => {
    const doc = buildHtmlSandboxDoc("<p>hi</p>");
    expect(doc).toContain("Content-Security-Policy");
    expect(doc).toContain("default-src 'none'");
    expect(doc).toContain("script-src 'unsafe-inline'");
    expect(doc).toContain("<p>hi</p>");
    // No remote origin is allowlisted in v1.
    expect(doc).not.toMatch(/script-src[^;]*https:/);
  });

  it("strips meta refresh and javascript: URLs from the HTML body", () => {
    const body = stripDangerousHtml(
      '<meta http-equiv="refresh" content="0;url=evil"><a href="javascript:alert(1)">x</a>',
    );
    expect(body).not.toContain("refresh");
    expect(body).not.toMatch(/javascript:/i);
    expect(body).toContain('href="#"');
  });

  it("renderArtifact routes svg through sanitizeSvgString", async () => {
    const result = await renderArtifact({ kind: "svg", source: "<svg onload=\"alert(1)\"></svg>" });
    expect(result.type).toBe("svg");
    if (result.type === "svg") expect(result.svg).toBe("<svg></svg>");
  });

  it("renderArtifact routes html through the sandbox wrapper", async () => {
    const result = await renderArtifact({ kind: "html", source: "<b>hi</b>" });
    expect(result.type).toBe("html");
    if (result.type === "html") {
      expect(result.srcdoc).toContain("Content-Security-Policy");
      expect(result.srcdoc).toContain("<b>hi</b>");
    }
  });

  it("renderArtifact returns an error for an unknown kind", async () => {
    const result = await renderArtifact({ kind: "chartjs", source: "x" });
    expect(result.type).toBe("error");
  });
});
