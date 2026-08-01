import type { AcpClientSink, AcpProviderDescriptor } from "@nusashell/application";

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
}
