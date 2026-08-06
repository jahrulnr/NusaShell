// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

const sendRequest = vi.fn();
const onEvent = vi.fn((_type, _handler) => () => {});

vi.mock("../src/renderer/ws-client.js", () => ({
  sendRequest: (...args: unknown[]) => sendRequest(...args),
  onEvent: (...args: unknown[]) => onEvent(...args),
}));

vi.mock("../src/renderer/ui-dialogs.js", () => ({
  confirmDialog: vi.fn(async () => true),
}));

import { PipelinesController } from "../src/renderer/pipelines-controller.js";

function mountDom() {
  document.body.innerHTML = `
    <section data-view="pipelines"></section>
    <div id="pipelines-list"></div>
    <div id="pipelines-empty" hidden></div>
    <div id="pipelines-error" hidden><span id="pipelines-error-message"></span></div>
    <button id="pipelines-new-btn"></button>
    <div id="pipeline-modal"></div>
    <h2 id="pipeline-modal-title"></h2>
    <button id="pipeline-modal-close"></button>
    <button id="pipeline-modal-cancel"></button>
    <button id="pipeline-modal-save"></button>
    <input id="pipeline-field-name" />
    <input id="pipeline-field-description" />
    <select id="pipeline-field-trigger-kind"><option value="event">Event</option><option value="schedule">Schedule</option></select>
    <div id="pipeline-schedule-fields" hidden></div>
    <div id="pipeline-event-fields"></div>
    <input id="pipeline-field-schedule" />
    <input id="pipeline-field-event-pattern" />
    <input id="pipeline-field-event-plugin" />
    <div id="pipeline-steps-list"></div>
    <button id="pipeline-add-step-btn"></button>
    <div id="pipeline-details-modal"></div>
    <h2 id="pipeline-details-title"></h2>
    <span id="pipeline-details-status"></span>
    <div id="pipeline-details-meta"></div>
    <div id="pipeline-details-dag"></div>
    <div id="pipeline-details-output" hidden></div>
    <section id="pipeline-details-step" hidden></section>
    <button id="pipeline-details-close"></button>
    <button id="pipeline-details-ok"></button>
    <button id="pipeline-details-run"></button>
    <button id="pipeline-details-cancel" hidden></button>
  `;
}

describe("PipelinesController", () => {
  beforeEach(() => {
    mountDom();
    sendRequest.mockReset();
    onEvent.mockClear();
    onEvent.mockImplementation(() => () => {});
  });

  it("registers lifecycle listeners and unsubscribes on destroy", () => {
    const unsubs = [vi.fn(), vi.fn(), vi.fn(), vi.fn(), vi.fn()];
    let i = 0;
    onEvent.mockImplementation(() => unsubs[i++] ?? (() => {}));
    const controller = new PipelinesController({ notify: vi.fn() });
    expect(onEvent).toHaveBeenCalled();
    controller.destroy();
    for (const u of unsubs.slice(0, onEvent.mock.calls.length)) {
      expect(u).toHaveBeenCalled();
    }
  });

  it("keeps details Run disabled while a run is active and resets on completion", async () => {
    const notify = vi.fn();
    const controller = new PipelinesController({ notify });
    const runBtn = document.getElementById("pipeline-details-run");
    const cancelBtn = document.getElementById("pipeline-details-cancel");

    // Simulate: user opens details for an active pipeline and clicks Run.
    controller._detailsPipelineId = "p1";
    document.getElementById("pipeline-details-modal").classList.add("active");
    controller.openDetails({
      id: "p1", name: "P1", enabled: true, lastStatus: null,
      trigger: { kind: "event", pattern: "test" }, steps: [],
      lastRunId: null,
    });
    sendRequest.mockResolvedValue({ ok: true, runId: "r1" });
    const runPromise = controller._runPipeline("p1");
    await runPromise;
    // Fire-and-track: runId is tracked as active, so Run stays disabled.
    expect(controller._activeRunByPipeline.get("p1")).toBe("r1");
    expect(runBtn.disabled).toBe(true);
    expect(cancelBtn.hidden).toBe(false);

    // completion event resets the modal.
    const completedHandler = onEvent.mock.calls.find((c) => c[0] === "pipeline.completed")?.[1];
    await completedHandler({ pipelineId: "p1", runId: "r1", name: "P1" });
    expect(controller._activeRunByPipeline.has("p1")).toBe(false);
    expect(runBtn.disabled).toBe(false);
    expect(cancelBtn.hidden).toBe(true);
  });

  it("re-enables Run when run fails to start", async () => {
    const controller = new PipelinesController({ notify: vi.fn() });
    controller._detailsPipelineId = "p1";
    const runBtn = document.getElementById("pipeline-details-run");
    sendRequest.mockResolvedValue({ ok: false, error: "pipeline is disabled", errorCode: "PIPELINE_DISABLED" });
    await controller._runPipeline("p1");
    expect(runBtn.disabled).toBe(false);
    expect(controller._activeRunByPipeline.has("p1")).toBe(false);
  });

  it("renders a pipeline as an execution rail with secondary actions in the menu", () => {
    const controller = new PipelinesController({ notify: vi.fn() });
    const card = controller._renderCard({
      id: "kanban",
      name: "Kanban Work",
      description: "Read, choose, update, move.",
      enabled: true,
      lastStatus: "success",
      trigger: { kind: "event", pattern: "kanban.*" },
      steps: [
        { id: "read", name: "Read tickets", action: { type: "agent" } },
        { id: "move", name: "Move card", action: { type: "tool" } },
      ],
    });

    expect(card.className).toBe("pipeline-card");
    expect(card.dataset.status).toBe("success");
    expect(card.querySelectorAll(".pipeline-flow-step")).toHaveLength(2);
    expect(card.querySelector('[data-action="details"]')?.textContent).toBe("Inspect");
    expect(card.querySelector('[data-action="edit"]')).toBeNull();

    document.body.appendChild(card);
    card.querySelector<HTMLButtonElement>('[data-action="more"]')!.click();
    expect(card.querySelectorAll(".pipeline-card-menu button")).toHaveLength(3);
  });

  it("keeps the full DAG visible and renders run output as Markdown", async () => {
    const controller = new PipelinesController({ notify: vi.fn() });
    const pipeline = {
      id: "kanban",
      name: "Kanban Work",
      enabled: true,
      lastStatus: "ok",
      lastRunAt: "2026-08-07T00:00:00.000Z",
      trigger: { kind: "event", pattern: "kanban.*" },
      steps: [
        { id: "read", name: "Read tickets", action: { type: "agent" } },
        { id: "move", name: "Move card", dependsOn: ["read"], action: { type: "tool", pluginId: "kanban", toolName: "move" } },
      ],
    };
    sendRequest.mockResolvedValue({ runs: [{ runId: "run-1", stepRuns: [
      { stepId: "read", status: "ok", summary: "## Summary\n\n**Done**" },
      { stepId: "move", status: "ok", summary: "Moved 6 cards" },
    ] }] });

    controller.openDetails(pipeline);
    await controller._renderDetailsDag(pipeline);

    expect(document.querySelectorAll("#pipeline-details-dag .pipeline-node")).toHaveLength(2);
    expect(document.querySelector("#pipeline-details-output")?.textContent).toContain("Summary");
    expect(document.querySelector("#pipeline-details-output h2")).not.toBeNull();
    expect(document.querySelector("#pipeline-details-output")?.textContent).not.toContain("## Summary");
    expect(document.getElementById("pipeline-details-output")!.compareDocumentPosition(document.getElementById("pipeline-details-step")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    (document.querySelector("#pipeline-details-dag .pipeline-node") as HTMLButtonElement).click();
    expect(document.querySelector("#pipeline-details-step")?.textContent).toContain("Read tickets");
    expect(document.querySelector("#pipeline-details-step")?.textContent).toContain("Agent turn");
    expect(document.querySelectorAll("#pipeline-details-step .pipeline-step-inspector-section")).toHaveLength(2);
    expect(document.querySelector("#pipeline-details-step .pipeline-step-inspector-definition")?.textContent).toContain("Configured instruction");
    expect(document.querySelector("#pipeline-details-step .pipeline-step-inspector-result")?.textContent).toContain("Latest output");
  });
});
