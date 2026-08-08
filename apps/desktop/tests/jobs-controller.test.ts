// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

const sendRequest = vi.fn();
const onEvent = vi.fn((_type, _handler) => () => {});

vi.mock("../src/renderer/ws-client.js", () => ({
  sendRequest: (...args: unknown[]) => sendRequest(...args),
  onEvent: (...args: unknown[]) => onEvent(...args),
}));

vi.mock("../src/renderer/plugin-api.js", () => ({
  fetchPlugins: vi.fn(async () => []),
  listTools: vi.fn(async () => ({ tools: [] })),
  startPlugin: vi.fn(async () => {}),
}));

vi.mock("../src/renderer/ui-dialogs.js", () => ({
  confirmDialog: vi.fn(async () => true),
}));

import { JobsController } from "../src/renderer/jobs-controller.js";

function mountDom() {
  document.body.innerHTML = `
    <div id="jobs-list"></div>
    <div id="jobs-empty" hidden></div>
    <button id="jobs-empty-new-btn"></button>
    <div id="jobs-error" hidden><span id="jobs-error-message"></span></div>
    <button id="jobs-new-btn"></button>
    <div id="job-modal"></div>
    <h2 id="job-modal-title"></h2>
    <button id="job-modal-close"></button>
    <button id="job-modal-cancel"></button>
    <button id="job-modal-save"></button>
    <input id="job-field-name" />
    <select id="job-field-trigger"><option value="schedule">Schedule</option><option value="event">Event</option></select>
    <div id="job-schedule-fields"></div>
    <div id="job-event-fields"></div>
    <input id="job-field-schedule" />
    <span id="job-schedule-help"></span>
    <input id="job-field-event-pattern" />
    <span id="job-event-help"></span>
    <select id="job-field-event-plugin"></select>
    <input id="job-field-throttle-ms" />
    <input id="job-field-max-fires" />
    <select id="job-field-mode"><option value="agent">Agent</option><option value="tool">Tool</option></select>
    <span id="job-mode-help"></span>
    <div id="job-agent-fields"></div>
    <span id="job-agent-prompt-label"></span>
    <textarea id="job-field-prompt"></textarea>
    <select id="job-field-provider"></select>
    <select id="job-field-model"></select>
    <select id="job-field-effort"></select>
    <span id="job-effort-label"></span>
    <div id="job-tool-fields"></div>
    <select id="job-field-plugin-id"></select>
    <select id="job-field-tool-name"></select>
    <span id="job-tool-help"></span>
    <div id="job-tool-schema-form"></div>
    <span id="job-tool-args-fallback-label"></span>
    <textarea id="job-field-args"></textarea>
    <input id="job-field-repeat" />
    <select id="job-field-oncomplete-type"></select>
    <textarea id="job-field-oncomplete-payload"></textarea>
    <div id="job-output-modal"></div>
    <h2 id="job-output-title"></h2>
    <button id="job-output-close"></button>
    <div id="job-output-body"></div>
    <div id="job-delete-overlay"></div>
    <div id="job-delete-dialog"></div>
    <h2 id="job-delete-title"></h2>
    <p id="job-delete-copy"></p>
    <button id="job-delete-close"></button>
    <button id="job-delete-cancel"></button>
    <button id="job-delete-confirm"></button>
  `;
}

describe("JobsController row running patch (ticket #53)", () => {
  beforeEach(() => {
    mountDom();
    sendRequest.mockReset();
    onEvent.mockClear();
    onEvent.mockImplementation(() => () => {});
  });

  it("_patchRowRunning(true) flips the row strip to 'running…' live", () => {
    const controller = new JobsController({ notify: vi.fn() });
    const job = {
      id: "job-1",
      name: "Nightly backup",
      enabled: true,
      nextRunAt: new Date(Date.now() + 3600_000).toISOString(),
      trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 60 } },
      mode: { type: "agent", prompt: "run backup" },
      lastStatus: "idle",
      lastError: null,
      repeat: null,
    };
    const row = controller._renderRow(job);
    const list = document.getElementById("jobs-list");
    list.appendChild(row);

    const stripNext = row.querySelector(".job-strip-next");
    expect(stripNext.textContent).toContain("next");
    expect(stripNext.textContent).not.toContain("running");

    controller._patchRowRunning("job-1", true);

    expect(row.classList.contains("job-row-running")).toBe(true);
    expect(stripNext.textContent).toBe("running…");
    const stopBtn = row.querySelector('[data-control="job-stop-btn"]');
    const runBtn = row.querySelector('[data-control="job-run-btn"]');
    expect(stopBtn.hidden).toBe(false);
    expect(runBtn.disabled).toBe(true);
  });

  it("_patchRowRunning(false) restores the strip to the scheduled next run", () => {
    const controller = new JobsController({ notify: vi.fn() });
    const job = {
      id: "job-2",
      name: "Nightly backup",
      enabled: true,
      nextRunAt: new Date(Date.now() + 3600_000).toISOString(),
      trigger: { kind: "schedule", schedule: { kind: "interval", minutes: 60 } },
      mode: { type: "agent", prompt: "run backup" },
      lastStatus: "idle",
      lastError: null,
      repeat: null,
    };
    const row = controller._renderRow(job);
    document.getElementById("jobs-list").appendChild(row);

    controller._patchRowRunning("job-2", true);
    expect(row.querySelector(".job-strip-next").textContent).toBe("running…");

    controller._patchRowRunning("job-2", false);
    const stripNext = row.querySelector(".job-strip-next");
    expect(stripNext.textContent).toContain("next");
    expect(row.classList.contains("job-row-running")).toBe(false);
    const runBtn = row.querySelector('[data-control="job-run-btn"]');
    expect(runBtn.disabled).toBe(false);
  });
});
