import type { AcpContentBlock } from "../../acp/ports/acp-client.port.js";
import type { AcpProviderResolverPort, AcpResolverResult } from "../../acp/ports/acp-provider-resolver.port.js";

export type { AcpResolverCandidate as SubagentProviderCandidate } from "../../acp/ports/acp-provider-resolver.port.js";

export interface SubagentResolveRequest {
  /** Workspace from the parent turn (used as default cwd). */
  readonly workspace?: string | undefined;
}

export type SubagentResolveResult = AcpResolverResult;

export interface SubagentRoutingInfo {
  /** Comma-separated list of connected+enabled ACP provider IDs. */
  readonly availableSubagents: string;
  /** The user-configured default ACP provider ID (first in try-order). */
  readonly defaultSubagent: string;
}

export interface SubagentRunRequest {
  readonly runId: string;
  readonly conversationId: string;
  readonly providerId: string;
  readonly workspace: string;
  readonly prompt: readonly AcpContentBlock[];
  /** Optional label for the side pane and inline run card. */
  readonly title?: string;
  /** Preferred config to apply via setConfigOption before the prompt. */
  readonly preferredConfig?: Readonly<Record<string, string | boolean>>;
}

export interface SubagentRunResult {
  readonly ok: boolean;
  readonly providerId: string;
  readonly summary: string;
  readonly error?: string;
  /** Non-fatal preferredConfig apply failures (surfaced to agent/user). */
  readonly configWarnings?: readonly string[];
}

/**
 * Port for resolving ACP provider candidates and running subagent turns.
 * Implemented by the composition root; bridges the application-layer gateway
 * to the desktop-side ACP provider store + session service.
 */
export interface SubagentPort {
  /** Resolve the effective try-order and candidate descriptors. */
  resolve(request: SubagentResolveRequest): Promise<SubagentResolveResult>;
  /** Resolve connected ACP providers + default for subagent prompt injection. */
  getRoutingInfo(): Promise<SubagentRoutingInfo | null>;
  /** Run a single ACP turn (blocking until turn_end). */
  run(request: SubagentRunRequest): Promise<SubagentRunResult>;
  /** Cancel an in-progress subagent turn. */
  cancel(runId: string, conversationId: string): Promise<void>;
}

/**
 * Convenience: check if a resolver port is available (non-null).
 * Used by the gateway to decide whether to expose the subagent tool.
 */
export function hasSubagentResolver(port: SubagentPort | undefined): port is SubagentPort {
  return port !== undefined;
}

export type { AcpProviderResolverPort };
