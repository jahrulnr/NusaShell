const NS = "http://www.w3.org/2000/svg";

export class LearningController {
  constructor(shell) {
      this.shell = shell;
      this.graph = null;
      this.selectedNodeId = null;
      this.scrubberValue = 100;

      this.els = {
        statsSkills: document.getElementById("learning-stat-skills"),
        statsMemory: document.getElementById("learning-stat-memory"),
        statsAgent: document.getElementById("learning-stat-agent"),
        statsUsed: document.getElementById("learning-stat-used"),
        timelineList: document.getElementById("learning-timeline-list"),
        timelineCount: document.getElementById("learning-timeline-count"),
        empty: document.getElementById("learning-empty"),
        svg: document.getElementById("learning-constellation-svg"),
        constellationMeta: document.getElementById("learning-constellation-meta"),
        scrubber: document.getElementById("learning-scrubber"),
        scrubberValue: document.getElementById("learning-scrubber-value"),
        detailMeta: document.getElementById("learning-detail-meta"),
        detailEmpty: document.getElementById("learning-detail-empty"),
        editor: document.getElementById("learning-detail-editor"),
        saveBtn: document.getElementById("learning-save-btn"),
        deleteBtn: document.getElementById("learning-delete-btn"),
        refreshBtn: document.getElementById("learning-refresh-btn"),
      };

      this._bindEvents();
    }

    initialize() {
      this.refresh();
    }

    async refresh() {
      try {
        this.graph = await this.shell.learning.graph();
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
      const svg = this.els.svg;
      svg.innerHTML = "";

      const nodes = this.graph.nodes;
      if (nodes.length === 0) {
        this.els.constellationMeta.textContent = "No nodes";
        return;
      }

      const edges = this.graph.edges;
      this.els.constellationMeta.textContent = `${edges.length} edge${edges.length === 1 ? "" : "s"}`;

      const timestamps = nodes.map((n) => n.timestamp ?? 0);
      const minT = Math.min(...timestamps);
      const maxT = Math.max(...timestamps);
      const tRange = maxT - minT || 1;

      const clusterMap = new Map();
      this.graph.clusters.forEach((c, i) => clusterMap.set(c.category, i));
      const clusterCount = Math.max(this.graph.clusters.length, 1);

      const W = 600;
      const H = 400;
      const padX = 40;
      const padY = 30;

      const positions = new Map();
      for (const node of nodes) {
        const t = node.timestamp ?? 0;
        const x = padX + ((t - minT) / tRange) * (W - 2 * padX);
        const clusterIdx = clusterMap.get(node.category) ?? 0;
        const y = padY + (clusterIdx / clusterCount) * (H - 2 * padY);
        positions.set(node.id, { x, y });
      }

      for (const edge of edges) {
        const s = positions.get(edge.source);
        const t = positions.get(edge.target);
        if (!s || !t) continue;
        const line = document.createElementNS(NS, "line");
        line.setAttribute("x1", String(s.x));
        line.setAttribute("y1", String(s.y));
        line.setAttribute("x2", String(t.x));
        line.setAttribute("y2", String(t.y));
        line.classList.add("ledge");
        line.dataset.source = edge.source;
        line.dataset.target = edge.target;
        svg.appendChild(line);
      }

      for (const node of nodes) {
        const pos = positions.get(node.id);
        if (!pos) continue;

        const g = document.createElementNS(NS, "g");
        g.classList.add("lnode");
        if (node.kind === "memory") g.classList.add("memory");
        if (node.state === "stale") g.classList.add("stale");
        if (node.state === "archived") g.classList.add("archived");
        if (node.id === this.selectedNodeId) g.classList.add("selected");
        g.dataset.nodeId = node.id;

        const circle = document.createElementNS(NS, "circle");
        circle.setAttribute("cx", String(pos.x));
        circle.setAttribute("cy", String(pos.y));
        circle.setAttribute("r", "5");
        g.appendChild(circle);

        const text = document.createElementNS(NS, "text");
        text.setAttribute("x", String(pos.x + 8));
        text.setAttribute("y", String(pos.y + 3));
        text.textContent = this._shortLabel(node.label, 18);
        g.appendChild(text);

        g.addEventListener("click", () => this._selectNode(node.id));
        svg.appendChild(g);
      }
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

      for (const g of this.els.svg.querySelectorAll(".lnode")) {
        const id = g.dataset.nodeId;
        g.classList.toggle("faded", !visibleIds.has(id));
      }
      for (const line of this.els.svg.querySelectorAll(".ledge")) {
        const visible = visibleIds.has(line.dataset.source) && visibleIds.has(line.dataset.target);
        line.classList.toggle("faded", !visible);
      }

      for (const item of this.els.timelineList.querySelectorAll(".learning-timeline-item")) {
        const id = item.dataset.nodeId;
        item.style.opacity = visibleIds.has(id) ? "" : "0.3";
      }
    }

    async _selectNode(nodeId) {
      this.selectedNodeId = nodeId;

      this.els.timelineList.querySelectorAll(".learning-timeline-item").forEach((el) => {
        el.classList.toggle("active", el.dataset.nodeId === nodeId);
      });
      this.els.svg.querySelectorAll(".lnode").forEach((el) => {
        el.classList.toggle("selected", el.dataset.nodeId === nodeId);
      });

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
      } catch (err) {
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
          await this.refresh();
          await this._selectNode(this.selectedNodeId);
        } else {
          this.els.saveBtn.disabled = false;
          alert(result.error || "Edit failed");
          if (result.code === "node_stale") {
            await this.refresh();
          }
        }
      } catch (err) {
        this.els.saveBtn.disabled = false;
        console.error("[learning] edit failed:", err);
      }
    }

    async _deleteNode() {
      if (!this.selectedNodeId) return;
      const detail = await this.shell.learning.getNode(this.selectedNodeId).catch(() => null);
      const word = detail?.kind === "skill" ? "archive" : "remove";
      if (!confirm(`Are you sure you want to ${word} this ${detail?.kind ?? "node"}?`)) return;

      this.els.deleteBtn.disabled = true;
      try {
        const result = await this.shell.learning.deleteNode(this.selectedNodeId);
        if (result.ok) {
          this.selectedNodeId = null;
          this.els.detailEmpty.hidden = false;
          this.els.editor.hidden = true;
          this.els.deleteBtn.disabled = true;
          this.els.saveBtn.disabled = true;
          this.els.detailMeta.textContent = "Select a node";
          await this.refresh();
        } else {
          this.els.deleteBtn.disabled = false;
          alert(result.error || "Delete failed");
          if (result.code === "node_stale") {
            await this.refresh();
          }
        }
      } catch (err) {
        this.els.deleteBtn.disabled = false;
        console.error("[learning] delete failed:", err);
      }
    }

    _dayKey(timestamp) {
      if (!timestamp) return "unknown";
      const d = new Date(timestamp);
      if (isNaN(d.getTime())) return "unknown";
      return d.toISOString().slice(0, 10);
    }

    _shortLabel(label, max) {
      return label.length > max ? label.slice(0, max - 1) + "…" : label;
    }
  }
