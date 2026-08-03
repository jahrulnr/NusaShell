import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { SubagentPort } from "../ports/subagent-port.js";
import { optionalString, requireString } from "./gateway-utils.js";
import { resolveAgentWorkspace } from "./resolve-agent-workspace.js";

const RETRYABLE_PATTERNS = [
  /rate.?limit/i,
  /usage.?limit/i,
  /quota/i,
  /429/,
  /503/,
  /502/,
  /temporarily unavailable/i,
  /connection refused/i,
  /econnrefused/i,
  /spawn/i,
  /enoent/i,
  /timeout/i,
  /timed out/i,
  /reset/i,
  /socket hang up/i,
];

function isRetryable(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return RETRYABLE_PATTERNS.some((pattern) => pattern.test(message));
}

export async function execSubagent(
  port: SubagentPort | undefined,
  args: Readonly<Record<string, unknown>>,
  _turnId: string,
  workspace: string | undefined,
  logger?: LoggerPort,
): Promise<unknown> {
  if (!port) {
    const message = "No ACP providers are connected";
    logger?.warn("Subagent rejected: subagent_not_configured");
    return {
      ok: false,
      code: "subagent_not_configured",
      error: message,
    };
  }
  const prompt = requireString(args.prompt, "prompt").trim();
  if (!prompt) throw new ApplicationError("AGENT_INVALID_INPUT", "prompt must not be empty");
  const providerIdOverride = optionalString(args.provider_id) || undefined;
  const title = optionalString(args.title) || undefined;
  const effectiveWorkspace = resolveAgentWorkspace(optionalString(args.workspace) || undefined, workspace);

  const resolved = await port.resolve({ providerIdOverride, workspace: effectiveWorkspace });
  if (resolved.tryOrder.length === 0) {
    const message = providerIdOverride
      ? `ACP provider "${providerIdOverride}" is not connected. Connect it in Settings → ACP Agents.`
      : "No ACP providers are connected. Connect one in Settings → ACP Agents.";
    logger?.warn("Subagent rejected: no_acp_provider override=%s", providerIdOverride ?? "(none)");
    return {
      ok: false,
      code: "no_acp_provider",
      error: message,
      workspace: effectiveWorkspace,
    };
  }

  const runId = randomUUID();
  const conversationId = `subagent:${runId}`;
  const attempted: string[] = [];
  // Host-prefix the absolute cwd so the ACP agent cannot invent a different path.
  const promptBlocks = [{
    type: "text" as const,
    text: `Working directory (cwd): ${effectiveWorkspace}\n\n${prompt}`,
  }];

  logger?.info("Subagent workspace runId=%s cwd=%s", runId, effectiveWorkspace);

  for (const providerId of resolved.tryOrder) {
    const candidate = resolved.candidates.get(providerId);
    if (!candidate) continue;
    attempted.push(providerId);
    try {
      const result = await port.run({
        runId,
        conversationId,
        providerId,
        workspace: effectiveWorkspace,
        prompt: promptBlocks,
        ...(title ? { title } : {}),
        ...(candidate.preferredConfig ? { preferredConfig: candidate.preferredConfig } : {}),
      });
      if (result.ok) {
        return {
          ok: true,
          runId,
          providerId: result.providerId,
          workspace: effectiveWorkspace,
          ...(attempted.length > 1 ? { attempted: attempted.slice(0, -1) } : {}),
          ...(title ? { title } : {}),
          summary: result.summary,
        };
      }
      if (!isRetryable(result.error)) {
        return {
          ok: false,
          runId,
          providerId: result.providerId,
          workspace: effectiveWorkspace,
          ...(attempted.length > 1 ? { attempted: attempted.slice(0, -1) } : {}),
          ...(title ? { title } : {}),
          error: result.error ?? "Subagent turn failed",
        };
      }
    } catch (error) {
      if (!isRetryable(error)) {
        throw error;
      }
    }
  }

  return {
    ok: false,
    runId,
    workspace: effectiveWorkspace,
    ...(attempted.length > 0 ? { providerId: attempted[attempted.length - 1] } : {}),
    attempted,
    ...(title ? { title } : {}),
    error: `All ACP providers failed: ${attempted.join(", ")}`,
  };
}
