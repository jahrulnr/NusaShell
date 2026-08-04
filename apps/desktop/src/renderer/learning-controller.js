import { LearningSigmaGraph } from "./learning-sigma-graph.js";

export class LearningController {
  constructor(shell) {
      this.shell = shell;
      this.graph = null;
      this.selectedNodeId = null;
      this.scrubberValue = 100;
      this.zoom = 1;
      this.sigmaGraph = null;
      this.activeTab = "table";

      this.els = {
        statsSkills: document.getElementById("learning-stat-skills"),
        statsMemory: document.getElementById("learning-stat-memory"),
        statsAgent: document.getElementById("learning-stat-agent"),
        statsUsed: document.getElementById("learning-stat-used"),
        timelineList: document.getElementById("learning-timeline-list"),
        timelineCount: document.getElementById("learning-timeline-count"),
        empty: document.getElementById("learning-empty"),
        graphCanvas: document.getElementById("learning-constellation-canvas"),
        constellationMeta: document.getElementById("learning-constellation-meta"),
        scrubber: document.getElementById("learning-scrubber"),
        scrubberValue: document.getElementById("learning-scrubber-value"),
        detailMeta: document.getElementById("learning-detail-meta"),
        detailEmpty: document.getElementById("learning-detail-empty"),
        editor: document.getElementById("learning-detail-editor"),
        saveBtn: document.getElementById("learning-save-btn"),
        deleteBtn: document.getElementById("learning-delete-btn"),
        detailActions: document.querySelector(".learning-detail .skills-editor-actions"),
        detailStatus: document.querySelector(".learning-detail-status"),
        refreshBtn: document.getElementById("learning-refresh-btn"),
        zoomIn: document.getElementById("learning-zoom-in"),
        zoomOut: document.getElementById("learning-zoom-out"),
        zoomReset: document.getElementById("learning-zoom-reset"),
        zoomValue: document.getElementById("learning-zoom-value"),
        constellationWrap: document.querySelector(".learning-constellation-svg-wrap"),
        tabTable: document.getElementById("learning-tab-table"),
        tabConnections: document.getElementById("learning-tab-connections"),
        panelTable: document.getElementById("learning-panel-table"),
        panelConnections: document.getElementById("learning-panel-connections"),
      };

      this._clearSelection();
      this._bindEvents();
    }

    initialize() {
      return this.refresh();
    }

    async refresh() {
      try {
        this.graph = await this.shell.learning.graph();
        if (this.selectedNodeId && !this.graph.nodes.some((node) => node.id === this.selectedNodeId)) {
          this._clearSelection();
        }
        this._renderStats();
        this._renderTimeline();
        this._renderConstellation();
        this._renderScrubber();
      } catch (err) {
        console.error("[learning] refresh failed:", err);
      }
    }

    _bindEvents() {
      this.els.refreshBtn.addEventListener("click", () => this.refresh());
      this.els.saveBtn.addEventListener("click", () => this._saveEdit());
      this.els.deleteBtn.addEventListener("click", () => this._deleteNode());
      this.els.scrubber.addEventListener("input", (e) => {
        this.scrubberValue = Number(e.target.value);
        this.els.scrubberValue.textContent = `${this.scrubberValue}%`;
        this._applyScrubber();
      });
      this.els.zoomIn.addEventListener("click", () => this.sigmaGraph?.zoomIn());
      this.els.zoomOut.addEventListener("click", () => this.sigmaGraph?.zoomOut());
      this.els.zoomReset.addEventListener("click", () => this.sigmaGraph?.resetView());
      this.els.tabTable.addEventListener("click", () => this._setTab("table"));
      this.els.tabConnections.addEventListener("click", () => this._setTab("connections"));
      window.addEventListener("resize", () => {
        this.sigmaGraph?.resize();
      });
    }

    _setTab(tab) {
      this.activeTab = tab;
      const connections = tab === "connections";
      this.els.tabTable.classList.toggle("active", !connections);
      this.els.tabConnections.classList.toggle("active", connections);
      this.els.tabTable.setAttribute("aria-selected", String(!connections));
      this.els.tabConnections.setAttribute("aria-selected", String(connections));
      this.els.panelTable.hidden = connections;
      this.els.panelConnections.hidden = !connections;
      if (connections) {
        requestAnimationFrame(() => {
          this.sigmaGraph?.resize();
          this.sigmaGraph?.resetView();
        });
      }
    }

    _renderStats() {
      if (!this.graph) return;
      const s = this.graph.stats;
      this.els.statsSkills.textContent = String(s.learnedSkills);
      this.els.statsMemory.textContent = String(s.memoryNodes);
      this.els.statsAgent.textContent = String(s.agentCreated);
      this.els.statsUsed.textContent = String(s.used);
    }

    _renderTimeline() {
      if (!this.graph) return;
      const nodes = [...this.graph.nodes].sort((a, b) => {
        const ta = a.timestamp ?? 0;
        const tb = b.timestamp ?? 0;
        return ta - tb;
      });

      this.els.timelineCount.textContent = `${nodes.length} nodes`;
      this.els.timelineList.innerHTML = "";

      if (nodes.length === 0) {
        this.els.empty.hidden = false;
        return;
      }
      this.els.empty.hidden = true;

      const groups = new Map();
      for (const node of nodes) {
        const day = this._dayKey(node.timestamp);
        if (!groups.has(day)) groups.set(day, []);
        groups.get(day).push(node);
      }

      for (const [day, groupNodes] of groups) {
        const group = document.createElement("div");
        group.className = "learning-timeline-group";

        const label = document.createElement("div");
        label.className = "learning-timeline-group-label";
        label.textContent = day;
        group.appendChild(label);

        for (const node of groupNodes) {
          group.appendChild(this._makeTimelineItem(node));
        }
        this.els.timelineList.appendChild(group);
      }
    }

    _makeTimelineItem(node) {
      const btn = document.createElement("button");
      btn.className = "learning-timeline-item";
      if (node.id === this.selectedNodeId) btn.classList.add("active");
      btn.type = "button";
      btn.dataset.nodeId = node.id;

      const dot = document.createElement("span");
      dot.className = `learning-timeline-dot ${node.kind === "memory" ? "memory" : node.state}`;
      btn.appendChild(dot);

      const label = document.createElement("strong");
      label.textContent = node.label;
      btn.appendChild(label);

      const meta = document.createElement("small");
      const parts = [node.category];
      if (node.useCount > 0) parts.push(`×${node.useCount}`);
      meta.textContent = parts.join(" · ");
      btn.appendChild(meta);

      if (node.pinned) {
        const pin = document.createElement("span");
        pin.className = "learning-timeline-pin";
        pin.textContent = "📌";
        pin.title = "Pinned";
        btn.appendChild(pin);
      }

      btn.addEventListener("click", () => this._selectNode(node.id));
      return btn;
    }

    _renderConstellation() {
      if (!this.graph) return;
      const nodes = this.graph.nodes;
      if (nodes.length === 0) {
        this.els.constellationMeta.textContent = "No nodes";
        this.sigmaGraph?.destroy();
        return;
      }

      const edges = this.graph.edges;
      this.els.constellationMeta.textContent = `Force layout · ${edges.length} edge${edges.length === 1 ? "" : "s"}`;
      if (!this.sigmaGraph) {
        this.sigmaGraph = new LearningSigmaGraph(this.els.graphCanvas, {
          onNodeSelect: (nodeId) => nodeId ? void this._selectNode(nodeId) : this._clearSelection(),
          onZoom: (percent) => {
            this.zoom = percent / 100;
            this.els.zoomValue.textContent = `${percent}%`;
          },
        });
      }
      this.sigmaGraph.mount(nodes, edges);
      this.sigmaGraph.setSelected(this.selectedNodeId);
      this.sigmaGraph.setScrubber(this.scrubberValue);
    }

    _renderScrubber() {
      if (!this.graph) return;
      this.els.scrubber.value = String(this.scrubberValue);
      this.els.scrubberValue.textContent = `${this.scrubberValue}%`;
      this._applyScrubber();
    }

    _applyScrubber() {
      if (!this.graph) return;
      const nodes = this.graph.nodes;
      const timestamps = nodes.map((n) => n.timestamp ?? 0);
      const maxT = Math.max(...timestamps);
      const cutoff = (this.scrubberValue / 100) * maxT;

      const visibleIds = new Set();
      for (const node of nodes) {
        if ((node.timestamp ?? 0) <= cutoff) {
          visibleIds.add(node.id);
        }
      }

      this.sigmaGraph?.setScrubber(this.scrubberValue);

      for (const item of this.els.timelineList.querySelectorAll(".learning-timeline-item")) {
        const id = item.dataset.nodeId;
        item.style.opacity = visibleIds.has(id) ? "" : "0.3";
      }
    }

    _setZoom(value) {
      if (value === 1) this.sigmaGraph?.resetView();
      else if (value > this.zoom) this.sigmaGraph?.zoomIn();
      else this.sigmaGraph?.zoomOut();
    }

    async _selectNode(nodeId) {
      this.selectedNodeId = nodeId;
      this._setStatus("");

      this.els.timelineList.querySelectorAll(".learning-timeline-item").forEach((el) => {
        el.classList.toggle("active", el.dataset.nodeId === nodeId);
      });
      this.sigmaGraph?.setSelected(nodeId);

      try {
        const detail = await this.shell.learning.getNode(nodeId);
        this.els.detailMeta.textContent = detail.label;
        this.els.detailEmpty.hidden = true;
        this.els.editor.hidden = false;
        this.els.editor.value = detail.content;
        this.els.editor.disabled = !detail.editable;
        this.els.saveBtn.disabled = !detail.editable;
        this.els.deleteBtn.disabled = false;
        this.els.deleteBtn.textContent = detail.kind === "skill" ? "Archive" : "Remove";
        this.els.detailActions.hidden = false;
      } catch (err) {
        this._clearSelection();
        this._setStatus(`Could not load this node: ${err.message || err}`, "error");
        console.error("[learning] getNode failed:", err);
      }
    }

    async _saveEdit() {
      if (!this.selectedNodeId) return;
      const content = this.els.editor.value;
      this.els.saveBtn.disabled = true;
      try {
        const result = await this.shell.learning.editNode(this.selectedNodeId, content);
        if (result.ok) {
          this.els.saveBtn.disabled = false;
          this._setStatus("Saved", "success");
          await this.refresh();
          if (this.selectedNodeId) await this._selectNode(this.selectedNodeId);
        } else {
          this.els.saveBtn.disabled = false;
          this._setStatus(result.error || "Could not save this node.", "error");
          if (result.code === "node_stale") {
            await this.refresh();
          }
        }
      } catch (err) {
        this.els.saveBtn.disabled = false;
        this._setStatus(`Could not save this node: ${err.message || err}`, "error");
        console.error("[learning] edit failed:", err);
      }
    }

    async _deleteNode() {
      if (!this.selectedNodeId) return;
      const detail = await this.shell.learning.getNode(this.selectedNodeId).catch(() => null);
      const word = detail?.kind === "skill" ? "archive" : "remove";
      const confirmed = await this._confirmMutation({
        action: word,
        kind: detail?.kind ?? "node",
        label: detail?.label ?? this.selectedNodeId,
      });
      if (!confirmed) return;

      this.els.deleteBtn.disabled = true;
      try {
        const result = await this.shell.learning.deleteNode(this.selectedNodeId);
        if (result.ok) {
          this._clearSelection();
          this._setStatus(detail?.kind === "skill" ? "Skill archived." : "Memory removed.", "success");
          await this.refresh();
        } else {
          this.els.deleteBtn.disabled = false;
          this._setStatus(result.error || `Could not ${word} this node.`, "error");
          if (result.code === "node_stale") {
            await this.refresh();
          }
        }
      } catch (err) {
        this.els.deleteBtn.disabled = false;
        this._setStatus(`Could not ${word} this node: ${err.message || err}`, "error");
        console.error("[learning] delete failed:", err);
      }
    }

    _clearSelection() {
      this.selectedNodeId = null;
      if (!this.els) return;
      this.els.detailEmpty.hidden = false;
      this.els.editor.hidden = true;
      this.els.editor.value = "";
      this.els.editor.disabled = true;
      this.els.deleteBtn.disabled = true;
      this.els.saveBtn.disabled = true;
      this.els.detailMeta.textContent = "Select a node";
      this.els.detailActions.hidden = true;
      this.els.timelineList?.querySelectorAll(".learning-timeline-item.active").forEach((el) => el.classList.remove("active"));
      this.sigmaGraph?.setSelected(null);
    }

    _setStatus(message, type = "info") {
      const status = this.els.detailStatus;
      if (!status) return;
      status.textContent = message;
      status.hidden = !message;
      status.dataset.type = type;
    }

    _confirmMutation({ action, kind, label }) {
      return new Promise((resolve) => {
        const overlay = document.createElement("div");
        overlay.className = "learning-confirm-overlay";
        const dialog = document.createElement("div");
        dialog.className = "learning-confirm-dialog";
        dialog.setAttribute("role", "dialog");
        dialog.setAttribute("aria-modal", "true");
        dialog.setAttribute("aria-label", `${action} ${kind}`);
        const title = document.createElement("strong");
        title.textContent = `${action === "archive" ? "Archive skill" : "Remove memory"}?`;
        const copy = document.createElement("p");
        copy.textContent = `${label} will ${action === "archive" ? "leave Learning and stay available in Archived skills" : "be removed from saved memory"}.`;
        const actions = document.createElement("div");
        actions.className = "learning-confirm-actions";
        const cancel = document.createElement("button");
        cancel.className = "mini-btn";
        cancel.type = "button";
        cancel.textContent = "Cancel";
        const confirmButton = document.createElement("button");
        confirmButton.className = "mini-btn danger";
        confirmButton.type = "button";
        confirmButton.textContent = action === "archive" ? "Archive" : "Remove";
        actions.append(cancel, confirmButton);
        dialog.append(title, copy, actions);
        overlay.appendChild(dialog);
        document.body.appendChild(overlay);

        const finish = (value) => {
          overlay.remove();
          resolve(value);
        };
        overlay.addEventListener("click", (event) => {
          if (event.target === overlay) finish(false);
        });
        const onKeyDown = (event) => {
          if (event.key === "Escape") finish(false);
        };
        overlay.addEventListener("keydown", onKeyDown);
        cancel.addEventListener("click", () => finish(false));
        confirmButton.addEventListener("click", () => finish(true));
        confirmButton.focus();
      });
    }

    _dayKey(timestamp) {
      if (timestamp == null || timestamp === 0) return "unknown";
      const d = new Date(timestamp);
      if (isNaN(d.getTime())) return "unknown";
      return d.toISOString().slice(0, 10);
    }

    _shortLabel(label, max) {
      return label.length > max ? label.slice(0, max - 1) + "…" : label;
    }
  }
