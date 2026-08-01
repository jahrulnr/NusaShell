import { ipcMain } from "electron";
import type { IpcContext } from "./ipc-context.js";
import type {
  AgentConversationCheckpoint,
  AgentConversationMessage,
} from "../../shared/agent-conversation-contract.js";

/** Register agent conversation + background review IPC handlers. */
export function registerAgentIpc(ctx: IpcContext): void {
  const store = () => ctx.getAgentConversationStore();

  ipcMain.handle("agent-conversations:list", () => store().list());
  ipcMain.handle("agent-conversations:create", (_event, options?: { kind?: "agent" | "acp"; acp?: { providerId: string; sessionId?: string; workspace?: string } }) =>
    store().create(options));
  ipcMain.handle("agent-conversations:get", (_event, id: string) => store().get(id));
  ipcMain.handle("agent-conversations:append", (_event, id: string, message: AgentConversationMessage) =>
    store().appendMessage(id, message));
  ipcMain.handle("agent-conversations:checkpoint", (_event, id: string, checkpoint: AgentConversationCheckpoint) =>
    store().saveCheckpoint(id, checkpoint));
  ipcMain.handle("agent-conversations:delete", (_event, id: string) => store().delete(id));
  ipcMain.handle("agent-conversations:replace-interrupted", (_event, id: string, message: AgentConversationMessage) =>
    store().replaceLastInterrupted(id, message));
  ipcMain.handle("agent-conversations:set-workspace", (_event, id: string, workspace: string) =>
    store().setWorkspace(id, workspace));

  ipcMain.handle("background-review:configure", (_event, settings: Record<string, unknown>) => {
    ctx.configureBackgroundReview(settings);
    return { ok: true };
  });
  ipcMain.handle("background-review:settings", () => ctx.backgroundReviewScheduler.getSettings());
}
