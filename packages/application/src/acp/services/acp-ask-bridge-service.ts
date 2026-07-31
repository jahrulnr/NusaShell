import { ApplicationError } from "../../errors/application-error.js";
import type { AcpAskAnswer, AcpAskOption, AcpAskRequest } from "../ports/acp-client.port.js";

interface PendingAsk {
  readonly traceId: string;
  readonly conversationId: string;
  readonly requestId: string;
  readonly options: readonly AcpAskOption[];
  readonly multiSelect?: boolean | undefined;
  readonly allowFreeText?: boolean | undefined;
  readonly resolve: (answer: AcpAskAnswer) => void;
  readonly reject: (error: Error) => void;
}

function key(conversationId: string, requestId: string): string {
  return `${conversationId}:${requestId}`;
}

export class AcpAskBridgeService {
  private readonly pending = new Map<string, PendingAsk>();

  request(traceId: string, conversationId: string, requestId: string, req: AcpAskRequest): Promise<AcpAskAnswer> {
    const k = key(conversationId, requestId);
    if (this.pending.has(k)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `ACP ask question already pending: ${requestId}`, { requestId, conversationId });
    }
    return new Promise<AcpAskAnswer>((resolve, reject) => {
      this.pending.set(k, {
        traceId,
        conversationId,
        requestId,
        options: req.options ?? [],
        multiSelect: req.multiSelect,
        allowFreeText: req.allowFreeText,
        resolve,
        reject,
      });
    });
  }

  answer(conversationId: string, requestId: string, answer: AcpAskAnswer): AcpAskAnswer {
    const k = key(conversationId, requestId);
    const pending = this.pending.get(k);
    if (!pending) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `No pending ACP ask question: ${requestId}`, { requestId, conversationId });
    }

    const text = (answer.text ?? "").trim();
    if (text) {
      if (!pending.allowFreeText) {
        throw new ApplicationError("AGENT_INVALID_INPUT", "Free-text answers are not allowed for this ACP question", { requestId, conversationId });
      }
      const result: AcpAskAnswer = { text };
      this.pending.delete(k);
      pending.resolve(result);
      return result;
    }

    const optionIds = [...new Set(answer.optionIds ?? [])];
    if (optionIds.length === 0) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "At least one option id or text is required", { requestId, conversationId });
    }
    if (!pending.multiSelect && optionIds.length > 1) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "Only one option may be selected for this ACP question", { requestId, conversationId });
    }

    const byId = new Map(pending.options.map((option) => [option.optionId, option]));
    for (const id of optionIds) {
      if (!byId.has(id)) {
        throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown ACP ask option id: ${id}`, { optionId: id, requestId, conversationId });
      }
    }

    const result: AcpAskAnswer = { optionIds };
    this.pending.delete(k);
    pending.resolve(result);
    return result;
  }

  rejectTurn(conversationId: string, reason = "ACP turn interrupted"): void {
    for (const [k, pending] of [...this.pending.entries()]) {
      if (pending.conversationId !== conversationId) continue;
      this.pending.delete(k);
      pending.reject(new ApplicationError("AGENT_TURN_CANCELLED", reason, { conversationId, requestId: pending.requestId }));
    }
  }

  clearTurn(conversationId: string): void {
    this.rejectTurn(conversationId, "ACP turn ended before the question was answered");
  }

  hasPending(conversationId: string, requestId: string): boolean {
    return this.pending.has(key(conversationId, requestId));
  }
}
