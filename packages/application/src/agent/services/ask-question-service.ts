import { ApplicationError } from "../../errors/application-error.js";

export type AskAnswerVia = "option" | "text";

export interface AskQuestionOption {
  readonly id: string;
  readonly label: string;
  readonly description?: string;
  readonly default?: boolean;
  readonly icon?: string;
  readonly image?: string;
}

export interface AskQuestionRequest {
  readonly question: string;
  readonly options: readonly AskQuestionOption[];
  readonly allowFreeText: boolean;
  readonly multiSelect: boolean;
}

export interface AskQuestionAnswer {
  readonly via: AskAnswerVia;
  readonly optionIds?: readonly string[];
  readonly text?: string;
}

export interface AskQuestionResult {
  readonly ok: true;
  readonly data: {
    readonly via: AskAnswerVia;
    readonly answer: string;
    readonly optionIds?: readonly string[];
  };
  readonly meta: Record<string, never>;
}

interface PendingAsk {
  readonly turnId: string;
  readonly request: AskQuestionRequest;
  readonly resolve: (result: AskQuestionResult) => void;
  readonly reject: (error: Error) => void;
}

function pendingKey(turnId: string, callId: string): string {
  return `${turnId}:${callId}`;
}

/**
 * Holds in-flight ask_question tool calls until the desktop answers them over
 * WebSocket, or the turn is cancelled / ended.
 */
export class AskQuestionService {
  private readonly pending = new Map<string, PendingAsk>();

  ask(turnId: string, callId: string, request: AskQuestionRequest): Promise<AskQuestionResult> {
    const key = pendingKey(turnId, callId);
    if (this.pending.has(key)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `Ask question already pending for call ${callId}`, { callId, turnId });
    }
    return new Promise<AskQuestionResult>((resolve, reject) => {
      this.pending.set(key, { turnId, request, resolve, reject });
    });
  }

  answer(turnId: string, callId: string, answer: AskQuestionAnswer): AskQuestionResult {
    const key = pendingKey(turnId, callId);
    const pending = this.pending.get(key);
    if (!pending) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `No pending ask question for call ${callId}`, { callId, turnId });
    }
    const result = buildResult(pending.request, answer);
    this.pending.delete(key);
    pending.resolve(result);
    return result;
  }

  rejectTurn(turnId: string, reason = "Agent turn interrupted by the user"): void {
    for (const [key, pending] of [...this.pending.entries()]) {
      if (pending.turnId !== turnId) continue;
      this.pending.delete(key);
      pending.reject(new ApplicationError("AGENT_TURN_CANCELLED", reason, { turnId }));
    }
  }

  clearTurn(turnId: string): void {
    this.rejectTurn(turnId, "Agent turn ended before the ask question was answered");
  }

  hasPending(turnId: string, callId: string): boolean {
    return this.pending.has(pendingKey(turnId, callId));
  }
}

function buildResult(request: AskQuestionRequest, answer: AskQuestionAnswer): AskQuestionResult {
  if (answer.via === "text") {
    const text = (answer.text ?? "").trim();
    if (!text) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "Free-text ask answer must not be empty");
    }
    if (!request.allowFreeText) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "Free-text answers are not allowed for this question");
    }
    return { ok: true, data: { via: "text", answer: text }, meta: {} };
  }

  const optionIds = [...new Set(answer.optionIds ?? [])];
  if (optionIds.length === 0) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "At least one option id is required");
  }
  if (!request.multiSelect && optionIds.length > 1) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "Only one option may be selected for this question");
  }

  const byId = new Map(request.options.map((option) => [option.id, option]));
  const labels: string[] = [];
  for (const id of optionIds) {
    const option = byId.get(id);
    if (!option) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `Unknown option id: ${id}`, { optionId: id });
    }
    labels.push(option.label);
  }

  return {
    ok: true,
    data: {
      via: "option",
      answer: labels.join(", "),
      optionIds,
    },
    meta: {},
  };
}
