function formatBytes(value) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

export class SkillsController {
  constructor({ shell, notify, log }) {
    this.api = shell?.skills;
    this.notify = notify;
    this.log = log;
    this.skills = [];
    this.selectedSkillId = "";
    this.selectedPath = "";
    this.savedContent = "";
  }

  async initialize() {
    this.bind();
    if (!this.api) {
      this.setUnavailable();
      return;
    }
    await this.refresh();
    await this.refreshPending();
    await this.refreshCuratorStatus();
    await this.refreshArchived();
  }

  bind() {
    document.querySelector("#skills-search")?.addEventListener("input", () => this.renderSkills());
    document.querySelector("#skills-install-btn")?.addEventListener("click", () => void this.install());
    document.querySelector("#skill-save-btn")?.addEventListener("click", () => void this.save());
    document.querySelector("#skill-delete-btn")?.addEventListener("click", () => void this.deleteSelected());
    document.querySelector("#skill-editor")?.addEventListener("input", () => this.syncDirtyState());
    document.querySelector("#skills-pending-refresh")?.addEventListener("click", () => void this.refreshPending());
    document.querySelector("#skills-archived-refresh")?.addEventListener("click", () => void this.refreshArchived());
    document.querySelector("#skills-curator-run")?.addEventListener("click", () => void this.runCurator(false));
    document.querySelector("#skills-curator-dry-run")?.addEventListener("click", () => void this.runCurator(true));
    document.querySelector("#skill-pin-btn")?.addEventListener("click", () => void this.togglePin());
  }

  async refresh(preferredId = this.selectedSkillId) {
    this.skills = [...await this.api.list()];
    const nextId = this.skills.some((skill) => skill.id === preferredId)
      ? preferredId
      : (this.skills[0]?.id ?? "");
    this.renderSkills();
    if (nextId) await this.selectSkill(nextId);
    else this.clearSelection();
  }

  renderSkills() {
    const list = document.querySelector("#skills-list");
    const empty = document.querySelector("#skills-empty");
    const count = document.querySelector("#skills-count");
    if (!list || !empty || !count) return;
    const query = document.querySelector("#skills-search")?.value.trim().toLowerCase() ?? "";
    const filtered = this.skills.filter((skill) =>
      `${skill.name} ${skill.description}`.toLowerCase().includes(query));
    count.textContent = `${this.skills.length} ${this.skills.length === 1 ? "skill" : "skills"}`;
    empty.hidden = this.skills.length !== 0;
    list.textContent = "";
    for (const skill of filtered) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "skills-list-item";
      button.dataset.skillId = skill.id;
      button.setAttribute("role", "option");
      button.setAttribute("aria-selected", String(skill.id === this.selectedSkillId));
      button.classList.toggle("active", skill.id === this.selectedSkillId);
      const name = document.createElement("strong");
      name.textContent = skill.name;
      const description = document.createElement("span");
      description.textContent = skill.description;
      const meta = document.createElement("small");
      meta.textContent = `${skill.fileCount} files`;
      button.append(name, description, meta);
      button.addEventListener("click", () => void this.selectSkill(skill.id));
      list.append(button);
    }
  }

  async selectSkill(skillId) {
    if (!await this.canLeaveEditor()) return;
    try {
      const detail = await this.api.get(skillId);
      this.selectedSkillId = skillId;
      this.selectedPath = "";
      this.savedContent = "";
      this.renderSkills();
      this.renderFiles(detail);
      this.resetEditor();
      document.querySelector("#skill-delete-btn").disabled = false;
      document.querySelector("#skill-pin-btn").disabled = false;
      const firstFile = detail.files.find((entry) => entry.path === "SKILL.md")
        ?? detail.files.find((entry) => entry.type === "file");
      if (firstFile) await this.openFile(firstFile);
    } catch (error) {
      this.notify(`Could not open skill: ${errorMessage(error)}`, "error");
    }
  }

  renderFiles(detail) {
    const tree = document.querySelector("#skills-file-tree");
    const count = document.querySelector("#skills-file-count");
    tree.textContent = "";
    count.textContent = `${detail.fileCount} ${detail.fileCount === 1 ? "file" : "files"}`;
    for (const entry of detail.files) {
      const row = document.createElement(entry.type === "file" ? "button" : "div");
      row.className = `skill-file-row ${entry.type}`;
      row.style.setProperty("--file-depth", String(entry.path.split("/").length - 1));
      row.setAttribute("role", "treeitem");
      row.setAttribute("aria-label", entry.path);
      const icon = document.createElement("span");
      icon.className = "skill-file-icon";
      icon.textContent = entry.type === "directory" ? "⌄" : (entry.editable ? "≡" : "◇");
      const label = document.createElement("span");
      label.className = "skill-file-name";
      label.textContent = entry.path.split("/").at(-1);
      row.append(icon, label);
      if (entry.type === "file") {
        row.type = "button";
        const size = document.createElement("small");
        size.textContent = formatBytes(entry.sizeBytes);
        row.append(size);
        row.addEventListener("click", () => void this.openFile(entry));
      }
      tree.append(row);
    }
  }

  async openFile(entry) {
    if (!await this.canLeaveEditor()) return;
    try {
      const file = await this.api.read(this.selectedSkillId, entry.path);
      this.selectedPath = entry.path;
      document.querySelectorAll(".skill-file-row.file").forEach((row) =>
        row.classList.toggle("active", row.getAttribute("aria-label") === entry.path));
      document.querySelector("#skill-file-title").textContent = entry.path;
      document.querySelector("#skill-file-meta").textContent =
        `${formatBytes(file.sizeBytes)} · ${file.editable ? "UTF-8 text · editable" : "Binary or large file · read-only"}`;
      document.querySelector("#skills-editor-empty").hidden = true;
      const editor = document.querySelector("#skill-editor");
      const binary = document.querySelector("#skill-binary-view");
      editor.hidden = !file.editable;
      binary.hidden = file.editable;
      if (file.editable) {
        this.savedContent = file.content ?? "";
        editor.value = this.savedContent;
      } else {
        this.savedContent = "";
        binary.textContent = `${entry.path}\n\nThis file is not rendered or editable.\nSize: ${formatBytes(file.sizeBytes)}`;
      }
      this.syncDirtyState();
    } catch (error) {
      this.notify(`Could not read file: ${errorMessage(error)}`, "error");
    }
  }

  async install() {
    try {
      const installed = await this.api.install();
      if (!installed) return;
      await this.refresh(installed.id);
      this.notify(`${installed.name} installed.`, "success");
    } catch (error) {
      this.notify(`Could not install skill: ${errorMessage(error)}`, "error");
    }
  }

  async save() {
    const editor = document.querySelector("#skill-editor");
    if (!this.selectedSkillId || !this.selectedPath || editor.hidden) return;
    const button = document.querySelector("#skill-save-btn");
    button.disabled = true;
    try {
      await this.api.write(this.selectedSkillId, this.selectedPath, editor.value);
      this.savedContent = editor.value;
      this.syncDirtyState();
      if (this.selectedPath === "SKILL.md") await this.refresh(this.selectedSkillId);
      this.notify(`${this.selectedPath} saved.`, "success");
    } catch (error) {
      this.notify(`Could not save file: ${errorMessage(error)}`, "error");
      this.syncDirtyState();
    }
  }

  async deleteSelected() {
    if (!this.selectedSkillId || !await this.canLeaveEditor()) return;
    const skill = this.skills.find((item) => item.id === this.selectedSkillId);
    if (!await confirmDialog({ title: "Delete skill?", message: `Delete ${skill?.name ?? this.selectedSkillId} from this device?`, confirmLabel: "Delete", danger: true })) return;
    try {
      await this.api.delete(this.selectedSkillId);
      const deletedName = skill?.name ?? this.selectedSkillId;
      this.selectedSkillId = "";
      await this.refresh();
      this.notify(`${deletedName} deleted.`, "success");
    } catch (error) {
      this.notify(`Could not delete skill: ${errorMessage(error)}`, "error");
    }
  }

  syncDirtyState() {
    const editor = document.querySelector("#skill-editor");
    const dirty = !editor.hidden && editor.value !== this.savedContent;
    const save = document.querySelector("#skill-save-btn");
    save.disabled = !dirty;
    save.textContent = dirty ? "Save changes" : "Saved";
  }

  async canLeaveEditor() {
    const editor = document.querySelector("#skill-editor");
    return editor.hidden || editor.value === this.savedContent
      || await confirmDialog({ title: "Discard changes?", message: "Your unsaved edits will be lost.", confirmLabel: "Discard", danger: true });
  }

  resetEditor() {
    this.selectedPath = "";
    this.savedContent = "";
    document.querySelector("#skill-file-title").textContent = "No file selected";
    document.querySelector("#skill-file-meta").textContent = "Choose a text file to inspect it.";
    document.querySelector("#skills-editor-empty").hidden = false;
    document.querySelector("#skill-editor").hidden = true;
    document.querySelector("#skill-binary-view").hidden = true;
    document.querySelector("#skill-save-btn").disabled = true;
  }

  clearSelection() {
    this.selectedSkillId = "";
    document.querySelector("#skills-file-tree").textContent = "";
    document.querySelector("#skills-file-count").textContent = "Select a skill";
    document.querySelector("#skill-delete-btn").disabled = true;
    document.querySelector("#skill-pin-btn").disabled = true;
    document.querySelector("#skill-pin-btn").textContent = "Pin";
    this.resetEditor();
    this.renderSkills();
  }

  setUnavailable() {
    document.querySelector("#skills-install-btn").disabled = true;
    document.querySelector("#skills-empty").hidden = false;
    document.querySelector("#skills-empty strong").textContent = "Skills bridge unavailable";
    document.querySelector("#skills-empty span").textContent = "Restart NusaShell after rebuilding the desktop preload.";
    this.log?.("warn", "Skills preload bridge is unavailable");
  }

  async refreshPending() {
    const container = document.querySelector("#skills-pending-list");
    if (!container) return;
    try {
      const pending = await this.api.pendingList();
      const countEl = document.querySelector("#skills-pending-count");
      if (countEl) countEl.textContent = `${pending.length} pending`;
      container.textContent = "";
      if (pending.length === 0) {
        const empty = document.createElement("p");
        empty.className = "skills-pending-empty";
        empty.textContent = "No pending skill writes.";
        container.append(empty);
        return;
      }
      for (const item of pending) {
        const row = document.createElement("div");
        row.className = "skills-pending-item";
        const info = document.createElement("div");
        info.className = "skills-pending-info";
        const action = document.createElement("strong");
        action.textContent = `${item.action}: ${item.skillId}`;
        const path = document.createElement("small");
        path.textContent = item.path !== "SKILL.md" ? item.path : "";
        info.append(action, path);
        const approve = document.createElement("button");
        approve.type = "button";
        approve.textContent = "Approve";
        approve.addEventListener("click", () => void this.approvePending(item.id));
        const reject = document.createElement("button");
        reject.type = "button";
        reject.textContent = "Reject";
        reject.addEventListener("click", () => void this.rejectPending(item.id));
        row.append(info, approve, reject);
        container.append(row);
      }
    } catch {
      container.textContent = "Could not load pending writes.";
    }
  }

  async approvePending(id) {
    try {
      await this.api.pendingApprove(id);
      this.notify("Pending skill write approved.", "success");
      await this.refresh();
      await this.refreshPending();
    } catch (error) {
      this.notify(`Could not approve: ${errorMessage(error)}`, "error");
    }
  }

  async rejectPending(id) {
    try {
      await this.api.pendingReject(id);
      this.notify("Pending skill write rejected.", "info");
      await this.refreshPending();
    } catch (error) {
      this.notify(`Could not reject: ${errorMessage(error)}`, "error");
    }
  }

  async refreshCuratorStatus() {
    const container = document.querySelector("#skills-curator-status");
    if (!container) return;
    try {
      const status = await this.api.curatorStatus();
      container.textContent = `Last run: ${status.lastRunAt ?? "never"} · Running: ${status.running ? "yes" : "no"}`;
    } catch {
      container.textContent = "Curator status unavailable.";
    }
  }

  async runCurator(dryRun = false) {
    try {
      const result = await this.api.curatorRun(dryRun);
      const count = result?.changes?.length ?? 0;
      this.notify(`Curator ${dryRun ? "dry-run" : "run"} complete: ${count} change(s).`, "success");
      await this.refreshCuratorStatus();
      if (!dryRun) { await this.refresh(); await this.refreshArchived(); }
    } catch (error) {
      this.notify(`Curator run failed: ${errorMessage(error)}`, "error");
    }
  }

  async pinSkill(skillId, pinned) {
    try {
      await this.api.pin(skillId, pinned);
      this.notify(`${skillId} ${pinned ? "pinned" : "unpinned"}.`, "success");
      this.updatePinButton(pinned);
    } catch (error) {
      this.notify(`Could not pin: ${errorMessage(error)}`, "error");
    }
  }

  async togglePin() {
    if (!this.selectedSkillId) return;
    const btn = document.querySelector("#skill-pin-btn");
    const isPinned = btn?.dataset.pinned === "true";
    await this.pinSkill(this.selectedSkillId, !isPinned);
  }

  updatePinButton(pinned) {
    const btn = document.querySelector("#skill-pin-btn");
    if (!btn) return;
    btn.dataset.pinned = String(pinned);
    btn.textContent = pinned ? "Unpin" : "Pin";
  }

  async restoreSkill(skillId) {
    try {
      await this.api.restore(skillId);
      this.notify(`${skillId} restored.`, "success");
      await this.refresh();
    } catch (error) {
      this.notify(`Could not restore: ${errorMessage(error)}`, "error");
    }
  }

  async refreshArchived() {
    const container = document.querySelector("#skills-archived-list");
    if (!container) return;
    try {
      const archived = await this.api.archivedList();
      const countEl = document.querySelector("#skills-archived-count");
      if (countEl) countEl.textContent = `${archived.length} archived`;
      container.textContent = "";
      if (archived.length === 0) {
        const empty = document.createElement("p");
        empty.className = "skills-archived-empty";
        empty.textContent = "No archived skills.";
        container.append(empty);
        return;
      }
      for (const skill of archived) {
        const row = document.createElement("div");
        row.className = "skills-archived-item";
        const info = document.createElement("div");
        info.className = "skills-archived-info";
        const name = document.createElement("strong");
        name.textContent = skill.name;
        const desc = document.createElement("small");
        desc.textContent = skill.description;
        info.append(name, desc);
        const restore = document.createElement("button");
        restore.type = "button";
        restore.textContent = "Restore";
        restore.addEventListener("click", () => void this.restoreSkill(skill.id));
        row.append(info, restore);
        container.append(row);
      }
    } catch {
      container.textContent = "Could not load archived skills.";
    }
  }
}
import { confirmDialog } from "./ui-dialogs.js";
