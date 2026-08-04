import { ipcMain, type OpenDialogOptions } from "electron";
import type { IpcContext } from "./ipc-context.js";

/** Register skills + learning IPC handlers. */
export function registerSkillsIpc(ctx: IpcContext): void {
  ipcMain.handle("skills:install", async (event) => {
    const owner = ctx.BrowserWindow.fromWebContents(event.sender);
    const options: OpenDialogOptions = {
      title: "Install an agent skill",
      buttonLabel: "Install skill",
      properties: ["openFile"],
      filters: [
        { name: "Agent skill packages", extensions: ["skill", "zip"] },
        { name: "All files", extensions: ["*"] },
      ],
    };
    const selection = owner
      ? await ctx.dialog.showOpenDialog(owner, options)
      : await ctx.dialog.showOpenDialog(options);
    if (selection.canceled || !selection.filePaths[0]) return null;
    const installed = await ctx.skillRegistry.installFromArchive(selection.filePaths[0]);
    await ctx.skillProvenance.markUser(installed.id);
    return installed;
  });

  ipcMain.handle("skills:list", () => ctx.skillRegistry.list());
  ipcMain.handle("skills:get", (_event, skillId: string) => ctx.skillRegistry.get(skillId));
  ipcMain.handle("skills:read", (_event, skillId: string, path?: string) =>
    ctx.skillRegistry.read(skillId, path));
  ipcMain.handle("skills:write", (_event, skillId: string, path: string, content: string) =>
    ctx.skillRegistry.write(skillId, path, content));
  ipcMain.handle("skills:delete", (_event, skillId: string) => ctx.skillRegistry.delete(skillId));

  ipcMain.handle("skills:pending:list", () => ctx.skillApprovalStaging.list());
  ipcMain.handle("skills:pending:approve", async (_event, id: string) => {
    const pending = await ctx.skillApprovalStaging.get(id);
    if (!pending) throw new Error(`Pending write not found: ${id}`);
    switch (pending.action) {
      case "create": {
        const detail = await ctx.skillRegistry.create(pending.skillId, pending.content);
        await ctx.skillProvenance.markAgent(pending.skillId);
        await ctx.skillApprovalStaging.remove(id);
        return detail;
      }
      case "edit": {
        const result = await ctx.skillRegistry.write(pending.skillId, "SKILL.md", pending.content);
        await ctx.skillApprovalStaging.remove(id);
        return result;
      }
      case "write_file": {
        const result = await ctx.skillRegistry.write(pending.skillId, pending.path, pending.content);
        await ctx.skillApprovalStaging.remove(id);
        return result;
      }
      case "delete": {
        await ctx.skillRegistry.delete(pending.skillId);
        await ctx.skillProvenance.clear(pending.skillId);
        await ctx.skillApprovalStaging.remove(id);
        return { deleted: pending.skillId };
      }
      default:
        throw new Error(`Unknown pending action: ${pending.action}`);
    }
  });
  ipcMain.handle("skills:pending:reject", (_event, id: string) => ctx.skillApprovalStaging.remove(id));

  ipcMain.handle("skills:curator:status", () => ({
    ...ctx.skillCuratorScheduler.getStatus(),
    scheduler: ctx.skillCuratorScheduler.getSettings(),
    curator: ctx.skillCurator.getSettings(),
  }));
  ipcMain.handle("skills:curator:run", (_event, dryRun: boolean) =>
    ctx.skillCuratorScheduler.runManual(dryRun));
  ipcMain.handle("skills:curator:configure", (_event, settings: Record<string, unknown>) => {
    if (settings.curator) ctx.configureCurator(settings.curator as Record<string, never>);
    if (settings.scheduler) ctx.configureCuratorScheduler(settings.scheduler as Record<string, never>);
    return {
      scheduler: ctx.skillCuratorScheduler.getSettings(),
      curator: ctx.skillCurator.getSettings(),
    };
  });

  ipcMain.handle("skills:pin", async (_event, skillId: string, pinned: boolean) => {
    await ctx.skillUsage.setPinned(skillId, pinned);
    return { ok: true };
  });
  ipcMain.handle("skills:restore", async (_event, skillId: string) => {
    await ctx.skillRegistry.restore(skillId);
    await ctx.skillUsage.setState(skillId, "active");
    // If this was a builtin skill the user previously deleted, unmark it so
    // the seeder resumes tracking and updating it on future startups.
    if (ctx.skillProvenance.unmarkBuiltinDeleted) {
      await ctx.skillProvenance.unmarkBuiltinDeleted(skillId);
    }
    return { ok: true };
  });
  ipcMain.handle("skills:archived:list", () => ctx.skillRegistry.listArchived());

  ipcMain.handle("learning:graph", () => ctx.learningGraph.buildGraph());
  ipcMain.handle("learning:node:get", (_event, nodeId: string) => ctx.learningGraph.getNode(nodeId));
  ipcMain.handle("learning:node:edit", (_event, nodeId: string, content: string) =>
    ctx.learningGraph.editNode(nodeId, content));
  ipcMain.handle("learning:node:delete", (_event, nodeId: string) =>
    ctx.learningGraph.deleteNode(nodeId));
}
