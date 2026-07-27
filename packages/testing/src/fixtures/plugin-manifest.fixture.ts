import type { PluginManifestInput } from "@nusashell/domain";
import type { PluginManifest } from "@nusashell/domain";
import { makeManifest } from "../fakes/fake-plugin-repository.js";

export function manifestFixture(): PluginManifest {
  return makeManifest();
}

export function manifestFixtureWith(
  overrides: Partial<PluginManifestInput>,
): PluginManifest {
  return makeManifest(overrides);
}
