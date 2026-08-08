import { ipcMain } from "electron";
import type { IpcContext } from "./ipc-context.js";
import type {
  AgentCanvasArtifact,
  AgentConversationCheckpoint,
  AgentConversationMessage,
  AgentConversationModelBinding,
  AgentSubagentRun,
  AgentSubagentRunStatus,
  AgentSubagentStreamStep,
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
  ipcMain.handle("agent-conversations:reserve-assistant", (_event, id: string, traceId: string, options?: { replaceLastInterrupted?: boolean }) =>
    store().reserveAssistant(id, traceId, options));
  ipcMain.handle("agent-conversations:seal-assistant", (_event, id: string, traceId: string, message: AgentConversationMessage) =>
    store().sealAssistant(id, traceId, message));
  ipcMain.handle("agent-conversations:checkpoint", (_event, id: string, checkpoint: AgentConversationCheckpoint) =>
    store().saveCheckpoint(id, checkpoint));
  ipcMain.handle("agent-conversations:delete", (_event, id: string) => {
    ctx.agentToolGateway.endConversation?.(id);
    ctx.conversationTodos?.clear(id);
    return store().delete(id);
  });
  ipcMain.handle("agent-conversations:replace-interrupted", (_event, id: string, message: AgentConversationMessage) =>
    store().replaceLastInterrupted(id, message));
  ipcMain.handle("agent-conversations:set-workspace", (_event, id: string, workspace: string) =>
    store().setWorkspace(id, workspace));
  ipcMain.handle("agent-conversations:set-model", (_event, id: string, model: AgentConversationModelBinding | null) =>
    store().setModel(id, model));
  ipcMain.handle("agent-conversations:upsert-canvas-artifact", (_event, id: string, artifact: AgentCanvasArtifact) =>
    store().upsertCanvasArtifact(id, artifact));
  ipcMain.handle("agent-conversations:set-active-canvas-artifact", (_event, id: string, artifactId: string | null) =>
    store().setActiveCanvasArtifact(id, artifactId));
  ipcMain.handle("agent-conversations:upsert-subagent-run", (_event, id: string, run: AgentSubagentRun) =>
    store().upsertSubagentRun(id, run));
  ipcMain.handle("agent-conversations:set-active-subagent-run", (_event, id: string, runId: string | null) =>
    store().setActiveSubagentRun(id, runId));
  ipcMain.handle("agent-conversations:update-subagent-run-status", (_event, id: string, runId: string, status: AgentSubagentRunStatus, patch?: { summary?: string; error?: string; steps?: readonly AgentSubagentStreamStep[] }) =>
    store().updateSubagentRunStatus(id, runId, status, patch));

  ipcMain.handle("background-review:configure", (_event, settings: Record<string, unknown>) => {
    ctx.configureBackgroundReview(settings);
    return { ok: true };
  });
  ipcMain.handle("background-review:settings", () => ctx.backgroundReviewScheduler.getSettings());
}
