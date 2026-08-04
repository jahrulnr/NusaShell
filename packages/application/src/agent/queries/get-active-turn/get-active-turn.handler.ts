import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { ActiveTurnProjectionPort } from "../../ports/active-turn-projection.port.js";
import type { GetActiveTurnQuery, GetActiveTurnResult } from "./get-active-turn.query.js";

export class GetActiveTurnHandler implements QueryHandler<GetActiveTurnQuery, GetActiveTurnResult> {
  constructor(private readonly activeTurns: ActiveTurnProjectionPort) {}

  async handle(query: GetActiveTurnQuery): Promise<GetActiveTurnResult> {
    return this.activeTurns.get(query.conversationId) ?? null;
  }
}
