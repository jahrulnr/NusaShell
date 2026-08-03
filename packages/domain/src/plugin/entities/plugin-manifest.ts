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
export type PluginSource = "native-mcp" | "package";

export interface PluginManifestInput {
  readonly source?: PluginSource;
  readonly id: string;
  readonly name: string;
  readonly version: string;
  readonly icon: string;
  readonly category?: string;
  readonly ui?: {
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
    readonly headers?: Readonly<Record<string, string>>;
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
    readonly source: PluginSource,
    readonly category: string | undefined,
    readonly ui: PluginManifestInput["ui"],
    readonly mcp: {
      readonly transport: TransportType;
      readonly command?: string;
      readonly args: readonly string[];
      readonly url?: string;
      readonly env: Readonly<Record<string, string>>;
      readonly headers: Readonly<Record<string, string>>;
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

    if (raw.ui && raw.ui.entry.trim().length === 0) {
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

    // `node` with no args drops into eval-stdin mode: the MCP SDK writes the
    // JSON-RPC `initialize` frame to the child's stdin, Node parses it as JS,
    // and the server crashes with `SyntaxError: Unexpected token ':'`. The
    // shell rewrites `node` → Electron-as-node (resolveStdioLaunch), so a
    // script path in args is mandatory for this command.
    if (
      raw.mcp.transport === "stdio" &&
      raw.mcp.command?.trim() === "node" &&
      !(raw.mcp.args?.length && raw.mcp.args.some((arg) => arg.trim().length > 0))
    ) {
      return err(
        new ManifestValidationError(
          "stdio transport with command 'node' requires a script path in mcp.args",
          { transport: raw.mcp.transport, command: raw.mcp.command },
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

    const windowMode = raw.ui?.window?.mode;
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

    if (raw.mcp.transport === "stdio" && raw.mcp.headers && Object.keys(raw.mcp.headers).length > 0) {
      return err(new ManifestValidationError("MCP headers are only valid for http or sse transport"));
    }

    const mcp: PluginManifest["mcp"] = {
      transport: raw.mcp.transport,
      args: raw.mcp.args ?? [],
      env: raw.mcp.env ?? {},
      headers: raw.mcp.headers ?? {},
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
        raw.source ?? "package",
        raw.category,
        raw.ui,
        mcp,
        dependencies,
      ),
    );
  }

  withMcpAutostart(autostart: boolean): PluginManifest {
    const result = PluginManifest.create({ ...this.toInput(), mcp: { ...this.toInput().mcp, autostart } });
    if (!result.ok) throw new Error(result.error.message);
    return result.value;
  }

  toInput(): PluginManifestInput {
    return {
      source: this.source,
      id: PluginIdFactory.toString(this.id), name: this.name, version: this.version.toString(), icon: this.icon,
      ...(this.category !== undefined ? { category: this.category } : {}),
      ...(this.ui !== undefined ? { ui: this.ui } : {}),
      mcp: { transport: this.mcp.transport, ...(this.mcp.command !== undefined ? { command: this.mcp.command } : {}), args: this.mcp.args, ...(this.mcp.url !== undefined ? { url: this.mcp.url } : {}), env: this.mcp.env, headers: this.mcp.headers, autostart: this.mcp.autostart, keepAliveOnClose: this.mcp.keepAliveOnClose },
      ...(this.dependencies.shell !== undefined ? { dependencies: this.dependencies } : {}),
    };
  }
}
