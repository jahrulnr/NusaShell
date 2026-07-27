import type { PluginManifestInput } from "@nusashell/domain";
import type { Plugin } from "@nusashell/domain";
import type { PluginRuntimeManager } from "@nusashell/application";
import { makePlugin } from "../fakes/fake-plugin-repository.js";
import type { FakePluginRepository } from "../fakes/fake-plugin-repository.js";

export function pluginFixture(
  id: string = "com.example.notes",
  overrides: Partial<PluginManifestInput> = {},
): Plugin {
  return makePlugin(id, overrides);
}

export async function runningPluginFixture(
  manager: PluginRuntimeManager,
  repository: FakePluginRepository,
  id: string = "com.example.notes",
  overrides: Partial<PluginManifestInput> = {},
): Promise<Plugin> {
  const plugin = makePlugin(id, overrides);
  repository.add(plugin);
  await manager.startPlugin(plugin.id);
  return plugin;
}
