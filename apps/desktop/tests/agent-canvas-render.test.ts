// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import {
  buildHtmlSandboxDoc,
  healMermaidFlowchartEdgeLabels,
  mermaidRenderCandidates,
  renderArtifact,
  sanitizeSvgString,
  softenMermaidSequenceRects,
  softenMermaidSvgRects,
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

  it("softens opaque mermaid sequence rect colors to rgba", () => {
    const source = [
      "sequenceDiagram",
      "    rect rgb(30, 60, 90)",
      "        A->>B: hi",
      "    end",
      "    rect rgba(10, 20, 30, 0.5)",
      "        A->>B: keep",
      "    end",
    ].join("\n");
    const out = softenMermaidSequenceRects(source);
    expect(out).toContain("rect rgba(30, 60, 90, 0.18)");
    expect(out).toContain("rect rgba(10, 20, 30, 0.5)");
    expect(out).not.toMatch(/rect rgb\(30, 60, 90\)\s*$/m);
  });

  it("adds fill-opacity to opaque SVG rects without one", () => {
    const svg = '<svg><rect fill="#1e3c5a" width="10" height="10"/><rect fill="none" width="1" height="1"/><rect fill="#abc" fill-opacity="0.4" width="2" height="2"/></svg>';
    const out = softenMermaidSvgRects(svg);
    expect(out).toMatch(/fill="#1e3c5a"[^>]*fill-opacity="0\.18"/);
    expect(out).toContain('fill="none"');
    expect(out).toContain('fill-opacity="0.4"');
    expect(out.match(/fill-opacity="0\.18"/g)?.length).toBe(1);
  });

  it("does not mistake Mermaid error CSS definitions for an error diagram", async () => {
    const validMermaidSvg = [
      '<svg id="valid-diagram">',
      "<style>.error-icon{fill:#552222}.error-text{fill:#552222}</style>",
      '<g class="node"><text>Plugin UI</text></g>',
      "</svg>",
    ].join("");

    const { isMermaidErrorDiagramSvg } = await import("../src/renderer/agent-canvas-render.js");
    expect(isMermaidErrorDiagramSvg(validMermaidSvg)).toBe(false);
    expect(isMermaidErrorDiagramSvg('<svg><text class="error-text">Syntax error in text</text></svg>')).toBe(true);
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

  it("mermaid syntax errors return type error and do not leak SVG into document.body", async () => {
    // Mermaid's default error diagram injects a huge "Syntax error in text" SVG
    // onto document.body when suppressErrorRendering is off and the temp node
    // is not removed — that breaks Electron shell layout.
    const before = document.body.innerHTML;
    const result = await renderArtifact({
      kind: "mermaid",
      source: "flowchart\n  A[\n  B ---",
    });
    expect(result.type).toBe("error");
    if (result.type === "error") {
      expect(result.message.length).toBeGreaterThan(0);
      expect(result.message).not.toMatch(/Could not load mermaid/i);
    }
    expect(document.body.textContent ?? "").not.toContain("Syntax error in text");
    expect(document.body.querySelectorAll("svg.error-icon, .error-text, svg").length).toBe(0);
    // No leftover mermaid temp hosts (`d{id}` / `i{id}`).
    expect(document.body.querySelector("[id^='d'], [id^='i']")).toBeNull();
    // Body should not have grown with mermaid leftovers.
    expect(document.body.innerHTML.length).toBeLessThanOrEqual(before.length + 32);
  });

  it("quotes unquoted flowchart edge labels that contain [] () {} # or HTML", () => {
    expect(healMermaidFlowchartEdgeLabels("flowchart LR\nA -->|x: []| B\n")).toContain(
      'A -->|"x: []"| B',
    );
    expect(healMermaidFlowchartEdgeLabels("flowchart LR\nA -->|foo(bar)| B\n")).toContain(
      'A -->|"foo(bar)"| B',
    );
    expect(healMermaidFlowchartEdgeLabels("flowchart LR\nA -->|id {x}| B\n")).toContain(
      'A -->|"id {x}"| B',
    );
    expect(healMermaidFlowchartEdgeLabels("flowchart LR\nA -->|#tag| B\n")).toContain(
      'A -->|"#tag"| B',
    );
    expect(healMermaidFlowchartEdgeLabels("flowchart LR\nA -->|a<br/>b| B\n")).toContain(
      'A -->|"a<br/>b"| B',
    );
    // leave safe / already-quoted labels alone
    expect(healMermaidFlowchartEdgeLabels("flowchart LR\nA -->|ok path| B\n")).toBe(
      "flowchart LR\nA -->|ok path| B\n",
    );
    expect(healMermaidFlowchartEdgeLabels('flowchart LR\nA -->|"x: []"| B\n')).toContain(
      'A -->|"x: []"| B',
    );
    // non-flowchart diagrams are not touched
    const seq = "sequenceDiagram\nA->>B: call(x)\n";
    expect(healMermaidFlowchartEdgeLabels(seq)).toBe(seq);
  });

  it("offers a healed flowchart candidate after the original source", () => {
    const raw = "flowchart LR\nD -->|pluginIds: []| E\n";
    const candidates = mermaidRenderCandidates(raw);
    expect(candidates).toHaveLength(2);
    expect(candidates[0]).toContain("|pluginIds: []|");
    expect(candidates[1]).toContain('|"pluginIds: []"|');
  });

  it("auto-heals [] edge labels enough to exit Mermaid parse failure", async () => {
    // Turn 5e23d8bf pattern: unquoted [] in edge label → SQS parse error.
    // After heal, Mermaid accepts the diagram. jsdom may still fail later on
    // getBBox (layout), so assert we are past the parse stage.
    const source = [
      "flowchart LR",
      "    A[pipeline step<br/>action: type=agent] --> B[PipelineScheduler.runStep]",
      "    B --> C[JobAgentExecutor.runAgent]",
      "    C --> D[worker.run]",
      "    D -->|pluginIds: []<br/>traceId, signal...| E[AgentTurnRunner.run]",
      "    E -->|input.workspace TIDAK di-set| F[beginTurn context]",
      "    F -->|workspace: undefined| G[McpAgentToolGateway]",
      "    G -->|wrapToolArgs tanpa base path| H[files/terminal tools<br/>fallback: home dir]",
    ].join("\n");
    const result = await renderArtifact({ kind: "mermaid", source });
    if (result.type === "error") {
      expect(result.message).not.toMatch(/Parse error/i);
      expect(result.message).not.toMatch(/got 'SQS'/);
    } else {
      expect(result.type).toBe("svg");
      expect(result.svg.length).toBeGreaterThan(0);
    }
  }, 30000);
});
