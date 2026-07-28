import { Entity } from "../../shared/entity.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import type { PluginVersion } from "../value-objects/plugin-version.js";
import type { PluginManifest } from "./plugin-manifest.js";

export interface CreatePluginInput {
  readonly id: PluginId;
  readonly version: PluginVersion;
  readonly manifest: PluginManifest;
  readonly enabled: boolean;
  readonly installPath: string;
  readonly installedAt: Date;
}

export class Plugin extends Entity<PluginId> {
  private constructor(
    id: PluginId,
    readonly version: PluginVersion,
    readonly manifest: PluginManifest,
    readonly enabled: boolean,
    readonly installPath: string,
    readonly installedAt: Date,
  ) {
    super(id);
  }

  static create(input: CreatePluginInput): Plugin {
    return new Plugin(
      input.id,
      input.version,
      input.manifest,
      input.enabled,
      input.installPath,
      input.installedAt,
    );
  }

  withEnabled(enabled: boolean): Plugin {
    return new Plugin(
      this.id,
      this.version,
      this.manifest,
      enabled,
      this.installPath,
      this.installedAt,
    );
  }

  withMcpAutostart(autostart: boolean): Plugin {
    return new Plugin(
      this.id,
      this.version,
      this.manifest.withMcpAutostart(autostart),
      this.enabled,
      this.installPath,
      this.installedAt,
    );
  }
}
