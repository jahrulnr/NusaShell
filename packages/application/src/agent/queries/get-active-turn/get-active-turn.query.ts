import type { ActiveTurnSnapshot } from "../../ports/active-turn-projection.port.js";

export interface GetActiveTurnQuery {
  readonly kind: "get-active-turn";
  readonly conversationId: string;
}

export type GetActiveTurnResult = ActiveTurnSnapshot | null;
