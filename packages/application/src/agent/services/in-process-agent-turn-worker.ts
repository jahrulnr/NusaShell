import type { AgentTurnResult, RunAgentTurnInput } from "./agent-turn-runner.js";

export interface AgentTurnWorker {
  run(input: RunAgentTurnInput): Promise<AgentTurnResult>;
}

/** Explicit MVP worker boundary; later detached execution can replace this implementation. */
export class InProcessAgentTurnWorker implements AgentTurnWorker {
  constructor(private readonly execute: (input: RunAgentTurnInput) => Promise<AgentTurnResult>) {}

  run(input: RunAgentTurnInput): Promise<AgentTurnResult> {
    return this.execute(input);
  }
}
