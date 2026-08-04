import type { AsyncToolRuntime, AsyncToolPeekResult } from "../../services/async-tool-runtime.js";
import type { KillToolJobCommand } from "./kill-tool-job.command.js";
import type { CommandResult } from "../../../messaging/command.js";

export class KillToolJobHandler {
  constructor(private readonly runtime: AsyncToolRuntime) {}

  async handle(command: KillToolJobCommand): CommandResult<AsyncToolPeekResult | { ok: false; error: string }> {
    const result = this.runtime.kill(command.handleId);
    if (!result) {
      return { ok: false, error: `Unknown handleId: ${command.handleId}` };
    }
    return result;
  }
}
