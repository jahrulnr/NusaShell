import { ApplicationError } from "../../errors/application-error.js";
import type { AcpPermissionAnswer, AcpPermissionOption, AcpPermissionRequest } from "../ports/acp-client.port.js";

interface PendingPermission {
  readonly traceId: string;
  readonly conversationId: string;
  readonly requestId: string;
  readonly options: readonly AcpPermissionOption[];
  readonly resolve: (answer: AcpPermissionAnswer) => void;
  readonly reject: (error: Error) => void;
}

function key(conversationId: string, requestId: string): string {
  return `${conversationId}:${requestId}`;
}

export class AcpPermissionService {
  private readonly pending = new Map<string, PendingPermission>();

  request(
    traceId: string,
    conversationId: string,
    requestId: string,
    req: AcpPermissionRequest,
  ): Promise<AcpPermissionAnswer> {
    const k = key(conversationId, requestId);
    if (this.pending.has(k)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `Permission request already pending: ${requestId}`, { requestId, conversationId });
    }
    return new Promise<AcpPermissionAnswer>((resolve, reject) => {
      this.pending.set(k, { traceId, conversationId, requestId, options: req.options, resolve, reject });
    });
  }

  answer(conversationId: string, requestId: string, optionId: string): AcpPermissionAnswer {
    const k = key(conversationId, requestId);
    const pending = this.pending.get(k);
    if (!pending) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `No pending permission request: ${requestId}`, { requestId, conversationId });
    }
    const valid = pending.options.some((option) => option.optionId === optionId);
    if (!valid) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown permission option: ${optionId}`, { optionId, requestId, conversationId });
    }
    const answer: AcpPermissionAnswer = { optionId };
    this.pending.delete(k);
    pending.resolve(answer);
    return answer;
  }

  rejectTurn(conversationId: string, reason = "ACP turn interrupted"): void {
    for (const [k, pending] of [...this.pending.entries()]) {
      if (pending.conversationId !== conversationId) continue;
      this.pending.delete(k);
      pending.reject(new ApplicationError("AGENT_TURN_CANCELLED", reason, { conversationId, requestId: pending.requestId }));
    }
  }

  clearTurn(conversationId: string): void {
    this.rejectTurn(conversationId, "ACP turn ended before permission was answered");
  }

  hasPending(conversationId: string, requestId: string): boolean {
    return this.pending.has(key(conversationId, requestId));
  }
}
