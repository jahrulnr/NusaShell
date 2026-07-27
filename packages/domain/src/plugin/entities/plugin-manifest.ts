import { DomainError } from "../../shared/domain-error.js";
import type { Result } from "../../shared/result.js";
import { err, ok } from "../../shared/result.js";
import type { PluginId } from "../value-objects/plugin-id.js";
import { PluginId as PluginIdFactory } from "../value-objects/plugin-id.js";
import type { PluginVersion } from "../value-objects/plugin-version.js";
import { PluginVersion as PluginVersionFactory } from "../value-objects/plugin-version.js";
import type { TransportType } from "../value-objects/transport-type.js";
import { TRANSPORT_TYPES } from "../value-objects/transport-type.js";

export type WindowMode = "panel" | "fullscreen" | "widget";

export interface PluginManifestInput {
  readonly id: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly ui: {
    readonly entry: string;
    readonly window?: {
      readonly mode?: WindowMode;
      readonly defaultSize?: { readonly width: number; readonly height: number };
      readonly resizable?: boolean;
    };
  };
  readonly mcp: {
    readonly transport: TransportType;
    readonly command?: string;
    readonly args?: readonly string[];
    readonly url?: string;
    readonly env?: Readonly<Record<string, string>>;
    readonly autostart?: boolean;
    readonly keepAliveOnClose?: boolean;
  };
  readonly dependencies?: {
    readonly shell?: string;
  };
}

class ManifestValidationError extends DomainError {
  readonly code = "VALIDATION_ERROR" as const;

  constructor(message: string, details?: Readonly<Record<string, unknown>>) {
    super(message, details);
  }
}

export class PluginManifest {
  private constructor(
    readonly id: PluginId,
    readonly name: string,
    readonly version: PluginVersion,
    readonly icon: string,
    readonly ui: PluginManifestInput["ui"],
    readonly mcp: {
      readonly transport: TransportType;
      readonly command?: string;
      readonly args: readonly string[];
      readonly url?: string;
      readonly env: Readonly<Record<string, string>>;
      readonly autostart: boolean;
      readonly keepAliveOnClose: boolean;
    },
    readonly dependencies: {
      readonly shell?: string;
    },
  ) {}

  static create(raw: PluginManifestInput): Result<PluginManifest, DomainError> {
    const idResult = PluginIdFactory.create(raw.id);
    if (!idResult.ok) {
      return idResult;
    }

    const versionResult = PluginVersionFactory.create(raw.version);
    if (!versionResult.ok) {
      return versionResult;
    }

    if (raw.name.trim().length === 0) {
      return err(new ManifestValidationError("Plugin name must not be empty"));
    }

    if (raw.icon.trim().length === 0) {
      return err(new ManifestValidationError("Plugin icon must not be empty"));
    }

    if (raw.ui.entry.trim().length === 0) {
      return err(new ManifestValidationError("UI entry must not be empty"));
    }

    if (!TRANSPORT_TYPES.includes(raw.mcp.transport)) {
      return err(
        new ManifestValidationError("MCP transport must be stdio, sse, or http"),
      );
    }

    if (raw.mcp.transport === "stdio" && !raw.mcp.command?.trim()) {
      return err(
        new ManifestValidationError(
          "stdio transport requires mcp.command",
          { transport: raw.mcp.transport },
        ),
      );
    }

    if (
      (raw.mcp.transport === "sse" || raw.mcp.transport === "http") &&
      !raw.mcp.url?.trim()
    ) {
      return err(
        new ManifestValidationError(
          `${raw.mcp.transport} transport requires mcp.url`,
          { transport: raw.mcp.transport },
        ),
      );
    }

    const windowMode = raw.ui.window?.mode;
    if (
      windowMode !== undefined &&
      windowMode !== "panel" &&
      windowMode !== "fullscreen" &&
      windowMode !== "widget"
    ) {
      return err(
        new ManifestValidationError("UI window mode must be panel, fullscreen, or widget"),
      );
    }

    const mcp: PluginManifest["mcp"] = {
      transport: raw.mcp.transport,
      args: raw.mcp.args ?? [],
      env: raw.mcp.env ?? {},
      autostart: raw.mcp.autostart ?? false,
      keepAliveOnClose: raw.mcp.keepAliveOnClose ?? false,
      ...(raw.mcp.command !== undefined ? { command: raw.mcp.command } : {}),
      ...(raw.mcp.url !== undefined ? { url: raw.mcp.url } : {}),
    };

    const dependencies: PluginManifest["dependencies"] =
      raw.dependencies?.shell !== undefined
        ? { shell: raw.dependencies.shell }
        : {};

    return ok(
      new PluginManifest(
        idResult.value,
        raw.name.trim(),
        versionResult.value,
        raw.icon.trim(),
        raw.ui,
        mcp,
        dependencies,
      ),
    );
  }
}
