import { describe, expect, it } from "vitest";
import { filterLauncherPlugins, positionContextMenu } from "../src/renderer/launcher-ui.js";

describe("launcher UI helpers", () => {
  it("filters plugins by name, plugin id, and manifest description", () => {
    const plugins = [
      { pluginId: "com.example.notes", name: "Notes", description: "Quick markdown notes" },
      { pluginId: "com.example.browse", name: "Browser", description: "Web automation" },
    ];

    expect(filterLauncherPlugins(plugins, "markdown").map((plugin) => plugin.pluginId)).toEqual(["com.example.notes"]);
    expect(filterLauncherPlugins(plugins, "browse").map((plugin) => plugin.pluginId)).toEqual(["com.example.browse"]);
    expect(filterLauncherPlugins(plugins, "")).toHaveLength(2);
  });

  it("keeps a right-click menu inside the window", () => {
    expect(positionContextMenu({ x: 880, y: 670 }, { width: 220, height: 240 }, { width: 900, height: 700 })).toEqual({ x: 672, y: 452 });
  });
});
