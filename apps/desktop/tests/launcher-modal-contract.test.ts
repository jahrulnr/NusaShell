// Source-invariant tests for the global modal keyboard/focus contract in
// launcher.js (tickets #52 + #58). launcher.js is a side-effect-heavy module,
// so we assert the wiring invariants on its source rather than importing it.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const launcherSrc = readFileSync(new URL("../src/renderer/launcher.js", import.meta.url), "utf8");

describe("global modal keyboard/focus contract (tickets #52, #58)", () => {
  it("Escape handler closes the pipeline editor and details modals", () => {
    expect(launcherSrc).toMatch(
      /\$\(\"#pipeline-modal\"\)\?\.classList\.contains\("active"\)[\s\S]*?pipelinesController\?\.closeModal\(\)/,
    );
    expect(launcherSrc).toMatch(
      /\$\(\"#pipeline-details-modal\"\)\?\.classList\.contains\("active"\)[\s\S]*?pipelinesController\?\.closeDetails\(\)/,
    );
  });

  it("Escape handler closes the Add Plugin modal and the plugin drawer", () => {
    expect(launcherSrc).toMatch(
      /\$\(\"#add-plugin-modal\"\)\?\.[\s\S]*?closeAddPluginModal\(\)/,
    );
    expect(launcherSrc).toMatch(
      /\$\(\"#plugin-drawer\"\)\?\.classList\.contains\("active"\)[\s\S]*?closeDrawer\(\)/,
    );
  });

  it("Add Plugin modal captures and restores focus on close", () => {
    expect(launcherSrc).toMatch(/let addPluginReturnFocus = null;/);
    expect(launcherSrc).toMatch(
      /function openAddPluginModal[\s\S]*?addPluginReturnFocus = document\.activeElement/,
    );
    expect(launcherSrc).toMatch(
      /function closeAddPluginModal[\s\S]*?addPluginReturnFocus\?\.isConnected[\s\S]*?addPluginReturnFocus\.focus\(\)/,
    );
  });

  it("plugin lifecycle actions run through runPluginLifecycle and surface failures (#60)", () => {
    // Drawer buttons must go through the error-toasting helper, not raw sendRequest.
    expect(launcherSrc).toMatch(/\$\("#btn-start"\)\.addEventListener\("click"[\s\S]*?runPluginLifecycle\("start"/);
    expect(launcherSrc).toMatch(/\$\("#btn-stop"\)\.addEventListener\("click"[\s\S]*?runPluginLifecycle\("stop"/);
    expect(launcherSrc).toMatch(/\$\("#btn-restart"\)\.addEventListener\("click"[\s\S]*?runPluginLifecycle\("restart"/);
    // Context-menu lifecycle actions also use the toast helper.
    expect(launcherSrc).toMatch(/runPluginLifecycle\("start", id\)/);
    expect(launcherSrc).toMatch(/runPluginLifecycle\("restart", id\)/);
    // openPluginWindow aborts opening a dead window when the lifecycle action fails.
    expect(launcherSrc).toMatch(
      /if \(!\(await runPluginLifecycle\("start", plugin\.pluginId\)\)\) return;/,
    );
    expect(launcherSrc).toMatch(
      /if \(!\(await runPluginLifecycle\("restart", plugin\.pluginId\)\)\) return;/,
    );
  });

  it("plugin load failure renders an error state, not empty state (#61)", () => {
    // fetchPlugins must not swallow to [] (plugin-api.js owns that, asserted in plugin-api tests).
    // launcher distinguishes error from empty in grid + table and tracks a pluginLoadError flag.
    expect(launcherSrc).toMatch(/let pluginLoadError = false;/);
    expect(launcherSrc).toMatch(/function setPluginLoadError/);
    expect(launcherSrc).toMatch(
      /if \(plugins\.length === 0\) \{[\s\S]*?if \(pluginLoadError\)[\s\S]*?Could not load plugins\. Use Retry above/,
    );
    expect(launcherSrc).toMatch(/\$\("#plugin-load-retry"\)\?\.addEventListener\("click"[\s\S]*?refreshAll\(\)/);
    // On a failed load with no known plugins, render the error state directly.
    expect(launcherSrc).toMatch(
      /setPluginLoadError\(true[\s\S]*?renderAppGrid\(\);\n      renderInstalledTable\(\);/,
    );
  });

  it("restart path recovers a crashed plugin before opening its window (#57)", () => {
    expect(launcherSrc).toMatch(
      /else if \(plugin\.state === "crashed"\)[\s\S]*?runPluginLifecycle\("restart", plugin\.pluginId\)/,
    );
    expect(launcherSrc).toMatch(
      /eventType === "plugin\.crashed"[\s\S]*?showToast\(`\$\{name\} crashed\. Use Restart to bring it back\.`, "error"\)/,
    );
  });
});
