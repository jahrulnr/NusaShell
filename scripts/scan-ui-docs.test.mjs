import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtemp, writeFile, rm, readdir, readFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  collectViews,
  collectHtmlIds,
  collectJsIds,
  renderControl,
  renderView,
  generate,
} from "./scan-ui-docs.mjs";

describe("collectViews", () => {
  it("extracts data-view identifiers from section tags", () => {
    const html = `
      <section class="view active" data-view="home">Home</section>
      <section class="view panel" data-view="settings">Settings</section>
      <div data-view="ignored">Not a section</div>
    `;
    expect([...collectViews(html)].sort()).toEqual(["home", "settings"]);
  });
});

describe("collectHtmlIds", () => {
  it("extracts id attributes from HTML", () => {
    const html = `
      <button id="btn-start">Start</button>
      <input id="search-input" />
      <div id="app-grid"></div>
    `;
    expect([...collectHtmlIds(html)].sort()).toEqual([
      "app-grid",
      "btn-start",
      "search-input",
    ]);
  });
});

describe("collectJsIds", () => {
  it("extracts ids referenced via $ and getElementById", () => {
    const js = `
      $("#btn-start").addEventListener("click", start);
      document.getElementById("search-input").focus();
      const el = document.querySelector("#app-grid");
      const all = document.querySelectorAll(".tile");
    `;
    expect([...collectJsIds([js])].sort()).toEqual([
      "app-grid",
      "btn-start",
      "search-input",
    ]);
  });
});

describe("renderControl", () => {
  it("renders a control with label, type, action, and shortcut", () => {
    const controls = {
      search: {
        label: "Global app search",
        type: "search input",
        action: "Filters the app grid.",
        shortcut: "Escape clears the query.",
      },
    };
    const md = renderControl("search", controls.search, controls);
    expect(md).toContain("**Global app search** (`#search`)");
    expect(md).toContain("Type: search input");
    expect(md).toContain("Action: Filters the app grid.");
    expect(md).toContain("Shortcut: Escape clears the query.");
  });

  it("renders selector-based controls", () => {
    const controls = {
      tile: { label: "Plugin tile", selector: ".app-cell", type: "button", action: "Click to open." },
    };
    const md = renderControl("tile", controls.tile, controls);
    expect(md).toContain("**Plugin tile** (`.app-cell`)");
  });
});

describe("renderView", () => {
  it("renders a full view markdown file with H2 sections and controls", () => {
    const view = {
      title: "Home",
      purpose: "Launcher grid.",
      howToOpen: "Open NusaShell.",
      sections: [
        {
          heading: "Search",
          paragraphs: ["Filter the grid."],
          controls: ["search"],
        },
      ],
    };
    const controls = {
      search: { label: "Search", type: "input", action: "Filter." },
    };
    const md = renderView("home", view, controls);
    expect(md.startsWith("# Home")).toBe(true);
    expect(md).toContain("## Search");
    expect(md).toContain("**Search** (`#search`)");
  });
});

describe("generate", () => {
  let tempDir;
  let uiMapPath;
  let htmlPath;
  let jsPath;
  let outDir;

  beforeEach(async () => {
    tempDir = await mkdtemp(join(tmpdir(), "scan-ui-docs-"));
    uiMapPath = join(tempDir, "ui-map.json");
    htmlPath = join(tempDir, "index.html");
    jsPath = join(tempDir, "launcher.js");
    outDir = join(tempDir, "ui");
  });

  afterEach(async () => {
    await rm(tempDir, { recursive: true, force: true });
  });

  it("generates markdown files and passes validation", async () => {
    await writeFile(
      uiMapPath,
      JSON.stringify({
        views: {
          home: {
            title: "Home",
            purpose: "Launcher.",
            sections: [
              { heading: "Grid", paragraphs: ["Plugins."], controls: ["app-grid"] },
            ],
          },
        },
        controls: {
          "app-grid": { label: "App grid", type: "grid", action: "Shows plugins." },
        },
      }),
    );
    await writeFile(
      htmlPath,
      `<section class="view" data-view="home"><div id="app-grid"></div></section>`,
    );
    await writeFile(jsPath, ``);

    await generate({ uiMapPath, htmlPath, jsPaths: [jsPath], outDir, exitOnError: false });

    const files = await readdir(outDir);
    expect(files).toContain("home.md");
    const md = await readFile(join(outDir, "home.md"), "utf8");
    expect(md).toContain("# Home");
    expect(md).toContain("## Grid");
    expect(md).toContain("**App grid** (`#app-grid`)");
  });

  it("fails when a data-view is missing from the map", async () => {
    await writeFile(
      uiMapPath,
      JSON.stringify({ views: {}, controls: {} }),
    );
    await writeFile(
      htmlPath,
      `<section class="view" data-view="home"><div id="app-grid"></div></section>`,
    );
    await writeFile(jsPath, ``);

    await expect(
      generate({ uiMapPath, htmlPath, jsPaths: [jsPath], outDir, exitOnError: false }),
    ).rejects.toThrow(/HTML has data-view="home"/);
  });

  it("fails when a non-generated control ID is missing from source", async () => {
    await writeFile(
      uiMapPath,
      JSON.stringify({
        views: {
          home: {
            title: "Home",
            sections: [
              { heading: "Grid", controls: ["missing-id"] },
            ],
          },
        },
        controls: {
          "missing-id": { label: "Missing", type: "button", action: "Nope." },
        },
      }),
    );
    await writeFile(htmlPath, `<section class="view" data-view="home"></section>`);
    await writeFile(jsPath, ``);

    await expect(
      generate({ uiMapPath, htmlPath, jsPaths: [jsPath], outDir, exitOnError: false }),
    ).rejects.toThrow(/"missing-id" is not found in the renderer source/);
  });

  it("allows generated controls that do not exist as source IDs", async () => {
    await writeFile(
      uiMapPath,
      JSON.stringify({
        views: {
          home: {
            title: "Home",
            sections: [
              { heading: "Grid", controls: ["app-cell"] },
            ],
          },
        },
        controls: {
          "app-cell": {
            label: "App cell",
            type: "button",
            selector: ".app-cell",
            generated: true,
            action: "Click to open.",
          },
        },
      }),
    );
    await writeFile(
      htmlPath,
      `<section class="view" data-view="home"><div class="app-cell"></div></section>`,
    );
    await writeFile(jsPath, ``);

    await generate({ uiMapPath, htmlPath, jsPaths: [jsPath], outDir, exitOnError: false });

    const files = await readdir(outDir);
    expect(files).toContain("home.md");
  });
});
