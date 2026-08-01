import type { CommandHandler } from "../../../messaging/command.js";
import type { RemoveAiCommand } from "./remove-ai.command.js";
import type { AiConfigurationPort } from "../../ports/ai-configuration.port.js";

export class RemoveAiHandler implements CommandHandler<RemoveAiCommand> {
  readonly kind = "remove-ai" as const;

  constructor(private readonly port: AiConfigurationPort) {}

  handle(command: RemoveAiCommand): void {
    this.port.removeAi(command.providerId);
  }
}
