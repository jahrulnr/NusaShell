import type {
  AcpAskAnswer,
  AcpAskOption,
  AcpAskRequest,
} from "@nusashell/application";
import type { AcpExtensionContext, AcpExtensionHandled, AcpProviderExtension } from "./acp-provider-extension.js";
import { parsePlanSteps } from "./acp-shared.js";

/**
 * Cursor vendor methods moved out of the core ACP client.
 * Owns `cursor/ask_question` (user must answer) and `cursor/create_plan`
 * (provider proposes a plan; host acknowledges).
 */
export class CursorAcpExtension implements AcpProviderExtension {
  matches(providerId: string): boolean {
    return providerId === "cursor";
  }

  async handleServerRequest(
    ctx: AcpExtensionContext,
    method: string,
    params: Record<string, unknown>,
  ): Promise<AcpExtensionHandled | undefined> {
    switch (method) {
      case "cursor/ask_question": {
        const req = parseAskRequest(params);
        const answer = ctx.sink ? await ctx.sink.askQuestion(req) : { text: "" };
        return { result: toAskAnswerJson(answer) };
      }
      case "cursor/create_plan": {
        if (ctx.sink) {
          const steps = parsePlanSteps(params.steps ?? params.entries);
          if (steps.length > 0) {
            ctx.sink.publish({ type: "acp.plan", traceId: ctx.traceId ?? ctx.sessionId ?? "unknown", steps });
          }
        }
        return { result: { accepted: true } };
      }
      default:
        return undefined;
    }
  }
}

function parseAskRequest(params: Record<string, unknown>): AcpAskRequest {
  const options: AcpAskOption[] = [];
  const rawOptions = Array.isArray(params.options) ? params.options : [];
  for (const opt of rawOptions) {
    if (typeof opt !== "object" || opt === null) continue;
    const o = opt as Record<string, unknown>;
    const optionId = String(o.id ?? o.optionId ?? "");
    const name = String(o.name ?? o.label ?? optionId);
    if (!optionId) continue;
    options.push({ optionId, name });
  }
  return {
    requestId: String(params.requestId ?? ""),
    question: String(params.question ?? ""),
    options: options.length > 0 ? options : undefined,
    multiSelect: typeof params.multiSelect === "boolean" ? params.multiSelect : undefined,
    allowFreeText: typeof params.allowFreeText === "boolean" ? params.allowFreeText : undefined,
  };
}

function toAskAnswerJson(answer: AcpAskAnswer): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  if (answer.text) {
    result.text = answer.text;
  }
  if (answer.optionIds) {
    result.optionIds = answer.optionIds;
  }
  return result;
}
