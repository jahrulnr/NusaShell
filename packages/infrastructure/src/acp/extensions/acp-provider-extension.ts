import type { AcpClientSink, AcpConfigOption, AcpProviderDescriptor } from "@nusashell/application";

export interface AcpExtensionContext {
  readonly provider: AcpProviderDescriptor;
  readonly sink: AcpClientSink | undefined;
  readonly traceId: string | null;
  readonly sessionId: string;
}

export interface AcpExtensionHandled {
  readonly result?: unknown;
  readonly error?: { code: number; message: string; data?: unknown };
}

/**
 * Descriptor for a provider-specific config-option apply.
 *
 * The extension does NOT hold the transport — it returns the JSON-RPC method +
 * params to send, and an optional `toConfigOptions` to derive the updated
 * `configOptions` snapshot from the response. Returning `undefined` from
 * `applyConfigOption` makes the core client fall back to the baseline
 * `session/set_config_option`.
 */
export interface AcpConfigOptionApplyDescriptor {
  readonly method: string;
  readonly params: Record<string, unknown>;
  /** Derive the updated configOptions snapshot from the response result.
   *  Return `undefined` (or an empty array) to signal "no snapshot — update
   *  the changed option locally" so the core client can mirror the value. */
  readonly toConfigOptions?: (result: unknown) => readonly AcpConfigOption[] | undefined;
}

/**
 * Provider-specific ACP behavior that lives outside the core JSON-RPC client.
 *
 * The core client handles the ACP baseline (`initialize`, `authenticate`,
 * `session/*`, `session/request_permission`, `session/update` mapping). Vendor
 * methods (e.g. `cursor/ask_question`, `cursor/create_plan`) and provider-specific
 * spawn-env enrichment belong here so the core client stays protocol-baseline.
 */
export interface AcpProviderExtension {
  matches(providerId: string): boolean;
  handleServerRequest?(
    ctx: AcpExtensionContext,
    method: string,
    params: Record<string, unknown>,
  ): Promise<AcpExtensionHandled | undefined>;
  enrichSpawnEnv?(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv;
  /**
   * Normalize a `session/new` result into synthetic `configOptions`.
   *
   * Return `undefined` to let the core client use the default
   * `parseConfigOptions(sessionResult.configOptions)` path. Providers whose
   * `session/new` returns a different shape (e.g. Gemini `modes`+`models`)
   * implement this to surface a uniform `AcpConfigOption[]` to the UI.
   */
  normalizeSessionConfig?(sessionResult: unknown): readonly AcpConfigOption[] | undefined;
  /**
   * Build a provider-specific apply descriptor for `setConfigOption`.
   *
   * Return `undefined` to fall back to the baseline `session/set_config_option`.
   * `sessionId` is the live session id so the descriptor can include it in
   * params (e.g. Gemini `session/set_mode` needs `{ sessionId, modeId }`).
   */
  applyConfigOption?(
    sessionId: string,
    configId: string,
    value: string | boolean,
  ): AcpConfigOptionApplyDescriptor | undefined;
}
