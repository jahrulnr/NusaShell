// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

const mount = vi.fn();
const destroy = vi.fn();
const resize = vi.fn();
const resetView = vi.fn();

vi.mock("../src/renderer/learning-sigma-graph.js", () => ({
  LearningSigmaGraph: vi.fn(function LearningSigmaGraph() {
    this.mount = mount;
    this.destroy = destroy;
    this.resize = resize;
    this.resetView = resetView;
    this.setSelected = vi.fn();
    this.setScrubber = vi.fn();
  }),
}));

import { LearningController } from "../src/renderer/learning-controller.js";

function installLearningDom() {
  document.body.innerHTML = `
    <button id="learning-refresh-btn"></button>
    <button id="learning-save-btn"></button><button id="learning-delete-btn"></button>
    <input id="learning-scrubber" /><span id="learning-scrubber-value"></span>
    <button id="learning-zoom-in"></button><button id="learning-zoom-out"></button><button id="learning-zoom-reset"></button>
    <span id="learning-zoom-value"></span><span id="learning-constellation-meta"></span>
    <div id="learning-constellation-canvas"></div><div class="learning-constellation-svg-wrap"></div>
    <button id="learning-tab-table"></button><button id="learning-tab-connections"></button>
    <section id="learning-panel-table"></section><section id="learning-panel-connections"></section>
    <span id="learning-stat-skills"></span><span id="learning-stat-memory"></span>
    <span id="learning-stat-agent"></span><span id="learning-stat-used"></span>
    <div id="learning-timeline-list"></div><span id="learning-timeline-count"></span><div id="learning-empty"></div>
    <span id="learning-detail-meta"></span><div id="learning-detail-empty"></div>
    <textarea id="learning-detail-editor"></textarea><div class="learning-detail-status"></div>
    <div class="learning-detail"><div class="skills-editor-actions"></div></div>
  `;
}

const graph = {
  stats: { learnedSkills: 1, memoryNodes: 0, agentCreated: 1, used: 0 },
  nodes: [{ id: "skill:one", label: "One", category: "test", state: "active", kind: "skill", timestamp: 1, useCount: 0 }],
  edges: [],
};

describe("LearningController graph lifecycle", () => {
  beforeEach(() => {
    installLearningDom();
    mount.mockClear();
    destroy.mockClear();
    globalThis.requestAnimationFrame = (callback) => callback();
  });

  it("does not mount Sigma while the Connections tab is hidden", async () => {
    const controller = new LearningController({ learning: { graph: vi.fn().mockResolvedValue(graph) } });

    await controller.initialize();

    expect(mount).not.toHaveBeenCalled();
  });

  it("mounts Sigma after Connections becomes visible", async () => {
    const controller = new LearningController({ learning: { graph: vi.fn().mockResolvedValue(graph) } });

    await controller.initialize();
    document.getElementById("learning-tab-connections").click();

    expect(mount).toHaveBeenCalledOnce();
  });
});
