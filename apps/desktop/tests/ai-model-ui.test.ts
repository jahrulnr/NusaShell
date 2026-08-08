import { describe, expect, it } from "vitest";
import { clampModelEffort, estimateContextTokens, formatContextUsage, formatEffortLabel, formatModelPickerLabel, modelCompatibility, modelEffortOptions, modelVisionStatus, resolveContextBadgeTokens, resolveRoomEffort, resolveRoomModel, searchModels, shouldApplyAcpUiUpdate } from "../src/renderer/ai-model-ui.js";

const visionModel = {
  id: "openai/gpt-5",
  label: "GPT-5",
  providerName: "OpenRouter",
  inputModes: ["text", "image", "file"],
  outputModes: ["text"],
  supportedEfforts: ["minimal", "low", "medium", "high", "xhigh"],
  defaultEffort: "medium",
  supportsTools: true,
};

describe("agent model UI projections", () => {
  it("labels a changed model as next-turn while another model is running", () => {
    expect(formatModelPickerLabel({
      model: { id: "claude-sonnet", key: "claude:sonnet" },
      effort: "high",
      source: "room",
      isRunning: true,
      liveModelKey: "openai:gpt-5",
    })).toBe("claude-sonnet · high · room · next turn");
  });

  it("shows the omit-effort sentinel as default, not auto", () => {
    expect(formatEffortLabel("auto")).toBe("default");
    expect(formatEffortLabel("high")).toBe("high");
    expect(formatModelPickerLabel({
      model: { id: "gpt-5" },
      effort: "auto",
    })).toBe("gpt-5 · default");
  });

  it("shows provider compatibility independently from effort", () => {
    expect(modelCompatibility(visionModel)).toEqual(["vision", "document", "tools", "reasoning"]);
  });

  it("reports confirmed, unavailable, and unknown vision support separately", () => {
    expect(modelVisionStatus(visionModel)).toBe("supported");
    expect(modelVisionStatus({ ...visionModel, inputModes: ["text"] })).toBe("unsupported");
    expect(modelVisionStatus({ ...visionModel, inputModes: [] })).toBe("unknown");
    expect(modelVisionStatus({ ...visionModel, inputModes: [], supportsVision: false })).toBe("unsupported");
  });

  it("searches model ID, label, and provider name", () => {
    expect(searchModels([visionModel], "openrouter")).toEqual([visionModel]);
    expect(searchModels([visionModel], "gpt-5")).toEqual([visionModel]);
    expect(searchModels([visionModel], "claude")).toEqual([]);
  });

  it("formats context usage as used of maximum", () => {
    expect(formatContextUsage(12_400, 200_000)).toBe("12k/200k context");
    expect(formatContextUsage(1_200_000, 1_000_000)).toBe("1.2M/1M context");
    expect(formatContextUsage(0, 0)).toBe("0 ctx");
  });

  it("estimates context from content, reasoning, tools, and steps", () => {
    expect(estimateContextTokens([
      { role: "user", content: "abcd" },
      {
        role: "assistant",
        content: "efgh",
        reasoning: "ijkl",
        steps: [{ type: "text", content: "mnop" }],
        toolCalls: [{ id: "1", name: "docs_list", ok: true }],
      },
    ])).toBeGreaterThan(estimateContextTokens([{ role: "user", content: "abcd" }]));
  });

  it("does not double-count content mirrored in steps", () => {
    const text = "This is a long assistant response that appears in both content and steps.";
    const withSteps = {
      role: "assistant",
      content: text,
      reasoning: text,
      steps: [{ type: "text", content: text }, { type: "reasoning", content: text }],
    };
    const stepsOnly = {
      role: "assistant",
      steps: [{ type: "text", content: text }, { type: "reasoning", content: text }],
    };
    // When steps are present, content/reasoning must be ignored — not added on top.
    expect(estimateContextTokens([withSteps])).toBe(estimateContextTokens([stepsOnly]));
  });

  it("falls back to content/reasoning/toolCalls when steps are absent", () => {
    const text = "Assistant reply without steps.";
    expect(estimateContextTokens([{
      role: "assistant",
      content: text,
      reasoning: text,
      toolCalls: [{ id: "1", name: "docs_list", ok: true }],
    }])).toBeGreaterThan(estimateContextTokens([{ role: "assistant", content: text }]));
  });

  // --- BH-CTX bug-hunt catalog (Wave A: estimator + format) ---

  it("bh-ctx-03: steps and body are not double-counted", () => {
    const text = "Long assistant response that appears in both content and steps.";
    const doubled = {
      role: "assistant",
      content: text,
      reasoning: text,
      steps: [{ type: "text", content: text }, { type: "reasoning", content: text }],
      toolCalls: [{ id: "1", name: "docs_list", ok: true }],
    };
    const stepsOnly = {
      role: "assistant",
      steps: [{ type: "text", content: text }, { type: "reasoning", content: text }],
    };
    // Used tokens must reflect roughly one copy, not ~2×.
    expect(estimateContextTokens([doubled])).toBe(estimateContextTokens([stepsOnly]));
    // And it must be less than the naive sum of all fields.
    const naiveSum = Math.ceil((text.length * 4 + text.length + text.length) / 4);
    expect(estimateContextTokens([doubled])).toBeLessThan(naiveSum);
  });

  it("bh-ctx-05: unknown window shows 'used ctx' form", () => {
    expect(formatContextUsage(8_000, 0)).toBe("8k ctx");
    expect(formatContextUsage(500, undefined)).toBe("500 ctx");
    expect(formatContextUsage(0, 0)).toBe("0 ctx");
  });

  it("bh-ctx-06: known window shows 'used/total context' form", () => {
    expect(formatContextUsage(12_400, 200_000)).toBe("12k/200k context");
    expect(formatContextUsage(1_200_000, 1_000_000)).toBe("1.2M/1M context");
    expect(formatContextUsage(300, 128_000)).toBe("300/128k context");
  });

  it("bh-ctx-07: empty thread shows 0/<window> context", () => {
    expect(formatContextUsage(0, 200_000)).toBe("0/200k context");
    expect(estimateContextTokens([])).toBe(0);
    expect(estimateContextTokens([{ role: "user", content: "" }])).toBe(0);
  });

  it("bh-ctx-09: hostile transcript text is treated as opaque length only", () => {
    const hostile = "<script>alert('xss')</script>'; DROP TABLE messages;--";
    const benign = "x".repeat(hostile.length);
    // Estimation is by character length only — no execution, no parsing.
    expect(estimateContextTokens([{ role: "user", content: hostile }]))
      .toBe(estimateContextTokens([{ role: "user", content: benign }]));
    // Badge formatting outputs plain status text, never embeds raw markup.
    const badge = formatContextUsage(estimateContextTokens([{ role: "user", content: hostile }]), 200_000);
    expect(badge).not.toContain("<script>");
    expect(badge).not.toContain("DROP TABLE");
  });

  it("clamps unsupported effort to the model default while preserving auto", () => {
    expect(clampModelEffort(visionModel, "auto")).toBe("auto");
    expect(clampModelEffort(visionModel, "max")).toBe("medium");
    expect(clampModelEffort(visionModel, "xhigh")).toBe("xhigh");
    // Empty catalog → auto only (no invented portable levels).
    expect(clampModelEffort({ ...visionModel, supportedEfforts: [], reasoningSupported: true }, "medium")).toBe("auto");
    expect(clampModelEffort({ ...visionModel, supportedEfforts: [], reasoningSupported: false }, "high")).toBe("auto");
  });

  it("exposes only catalog effort options in the picker", () => {
    expect(modelEffortOptions({ ...visionModel, supportedEfforts: [], reasoningSupported: true }))
      .toEqual([]);
    expect(modelEffortOptions(visionModel)).toEqual(["minimal", "low", "medium", "high", "xhigh"]);
  });
});

describe("resolveContextBadgeTokens", () => {
  it("uses estimatedTokens as the display signal", () => {
    expect(resolveContextBadgeTokens({ estimatedTokens: 8000, liveTokens: 0 })).toBe(8000);
  });

  it("never drops below already-streamed output (late context event)", () => {
    expect(resolveContextBadgeTokens({ estimatedTokens: 2000, liveTokens: 9000 })).toBe(9000);
  });

  it("returns 0 when no estimate or live tokens are provided", () => {
    expect(resolveContextBadgeTokens({})).toBe(0);
    expect(resolveContextBadgeTokens()).toBe(0);
  });

  // --- BH-CTX bug-hunt catalog (Wave B: live badge resolution) ---

  it("bh-ctx-01: multi-round tool turn does not sum prompt tokens", () => {
    // A turn with 4 tool rounds, each reporting ~12k prompt, gives a cumulative
    // inputTokens of ~48k. The badge must stay near the single-window estimate.
    const windowEstimate = 12_000;
    const cumulativeBilling = 48_000;
    expect(resolveContextBadgeTokens({
      estimatedTokens: windowEstimate,
      inputTokens: cumulativeBilling,
      liveTokens: 0,
    })).toBe(windowEstimate);
    // Even with no estimate, cumulative billing alone must not drive the badge.
    expect(resolveContextBadgeTokens({
      estimatedTokens: 0,
      inputTokens: cumulativeBilling,
      liveTokens: 0,
    })).toBe(0);
  });

  it("bh-ctx-04: prefers window estimate over cumulative usage on live updates", () => {
    // Cumulative billed input is larger than the window estimate — badge must
    // use the window estimate, ignoring the cumulative total for display.
    expect(resolveContextBadgeTokens({
      estimatedTokens: 15_000,
      inputTokens: 60_000,
      liveTokens: 0,
    })).toBe(15_000);
    // Live streamed output can exceed the estimate; take the richer value but
    // still never add cumulative billing on top.
    expect(resolveContextBadgeTokens({
      estimatedTokens: 15_000,
      inputTokens: 60_000,
      liveTokens: 20_000,
    })).toBe(20_000);
  });
});

// --- BH-CTX bug-hunt catalog (Wave C: controller contract) ---
//
// BH-CTX-02 (post-turn badge matches reopen) and BH-CTX-08 (ACP turn refreshes
// idle badge) are controller behaviors in agent-conversation-controller.js that
// require an Electron BrowserWindow to mount. They are covered by:
//   - resolveContextBadgeTokens ignoring cumulative inputTokens (Wave B), and
//   - the controller calling this.updateContextStatus() (which re-estimates
//     from persisted messages via refresh()) after both normal and ACP turns,
//     without overwriting with result.usage.inputTokens.
// See agent-conversation-controller.js: onContextUpdate passes the full event
// payload to resolveContextBadgeTokens; post-turn drops finalTokens overwrite;
// submitAcp finally calls updateContextStatus().
//
// The contract test below asserts the helper invariant that makes BH-CTX-02
// hold: after a turn, the idle estimate is the only signal — cumulative billing
// can never exceed it via the helper.

describe("BH-CTX-02/08 controller contract (helper-backed)", () => {
  it("bh-ctx-02: idle estimate is independent of cumulative billing", () => {
    // After refresh(), updateContextStatus() estimates from persisted messages.
    // A mid-turn cumulative billing figure must not leak into that idle value.
    const idleEstimate = 14_000;
    const midTurnCumulativeBilling = 56_000;
    expect(resolveContextBadgeTokens({
      estimatedTokens: idleEstimate,
      inputTokens: midTurnCumulativeBilling,
      liveTokens: 0,
    })).toBe(idleEstimate);
  });

  it("bh-ctx-08: ACP post-turn recompute uses estimate, not billing", () => {
    // submitAcp finally calls updateContextStatus() → estimate from persisted
    // thread. No ACP token events are emitted, so the badge relies on the
    // local estimate alone — cumulative billing is never injected.
    const acpEstimate = 9_000;
    expect(resolveContextBadgeTokens({
      estimatedTokens: acpEstimate,
      inputTokens: 100_000,
      liveTokens: 0,
    })).toBe(acpEstimate);
  });
});

describe("shouldApplyAcpUiUpdate", () => {
  it("returns true when still on the same ACP conversation", () => {
    expect(shouldApplyAcpUiUpdate({ activeId: "c1", activeKind: "acp", startedId: "c1" })).toBe(true);
  });

  it("returns false when the conversation is no longer ACP (switched to regular chat)", () => {
    expect(shouldApplyAcpUiUpdate({ activeId: "c1", activeKind: "chat", startedId: "c1" })).toBe(false);
    expect(shouldApplyAcpUiUpdate({ activeId: "c1", activeKind: undefined, startedId: "c1" })).toBe(false);
  });

  it("returns false when the user switched to a different conversation", () => {
    expect(shouldApplyAcpUiUpdate({ activeId: "c2", activeKind: "acp", startedId: "c1" })).toBe(false);
  });

  it("returns false when ids are missing", () => {
    expect(shouldApplyAcpUiUpdate({ activeId: "", activeKind: "acp", startedId: "c1" })).toBe(false);
    expect(shouldApplyAcpUiUpdate({ activeId: "c1", activeKind: "acp", startedId: "" })).toBe(false);
    expect(shouldApplyAcpUiUpdate({})).toBe(false);
    expect(shouldApplyAcpUiUpdate()).toBe(false);
  });

  it("returns false when switched to a different ACP conversation", () => {
    // Even if still ACP, a different conversation id means the await is stale.
    expect(shouldApplyAcpUiUpdate({ activeId: "acp-b", activeKind: "acp", startedId: "acp-a" })).toBe(false);
  });
});

describe("resolveRoomModel (ticket #38)", () => {
  const globalModel = { key: "global/claude", id: "claude-3", label: "Claude" };
  const roomModel = {
    key: "room/gpt",
    id: "gpt-5",
    label: "GPT",
    supportedEfforts: ["low", "medium", "high"],
    defaultEffort: "medium",
  };

  it("prefers the conversation's explicit model binding over the global default", () => {
    expect(resolveRoomModel(
      { kind: "agent", id: "c1", model: { modelKey: "room/gpt", effort: "high" } },
      [globalModel, roomModel],
      "global/claude",
    )).toEqual({ model: roomModel, effort: "high", source: "room", explicit: true });
  });

  it("falls back to the global active model when the room has no binding", () => {
    expect(resolveRoomModel(
      { kind: "agent", id: "c1" },
      [globalModel, roomModel],
      "global/claude",
    )).toEqual({ model: globalModel, effort: "auto", source: "global", explicit: false });
  });

  it("never inherits the Settings-page global effort into an unbound room", () => {
    // Room effort is independent of the settings/global effort. Unbound rooms
    // stay on "auto" so picking effort in another room (which also stamps the
    // settings default) cannot leak across conversations.
    expect(resolveRoomEffort(
      { kind: "agent", id: "c1" },
      [globalModel, roomModel],
      "global/claude",
      "high",
    )).toBe("auto");
    expect(resolveRoomEffort(
      { kind: "agent", id: "c1", model: { modelKey: "room/gpt", effort: "low" } },
      [globalModel, roomModel],
      "global/claude",
      "high",
    )).toBe("low");
  });

  it("forces auto when the bound model has no catalog effort levels", () => {
    expect(resolveRoomModel(
      { kind: "agent", id: "c1", model: { modelKey: "empty/key", effort: "medium" } },
      [{ key: "empty/key", id: "empty", supportedEfforts: [] }],
      "global/claude",
    )).toEqual({
      model: { key: "empty/key", id: "empty", supportedEfforts: [] },
      effort: "auto",
      source: "room",
      explicit: true,
    });
  });

  it("resolves the room model even when its key uses a bound cleanup (explicit flag)", () => {
    expect(resolveRoomModel(
      { kind: "agent", id: "c1", model: { modelKey: "room/gpt", effort: "auto" } },
      [globalModel, roomModel],
      "global/claude",
    )).toEqual({ model: roomModel, effort: "auto", source: "room", explicit: true });
  });

  it("returns null when the resolved model is not found in the catalog", () => {
    expect(resolveRoomModel(
      { kind: "agent", id: "c1", model: { modelKey: "missing/key", effort: "auto" } },
      [globalModel],
      "global/claude",
    )).toEqual({ model: null, effort: "auto", source: "room", explicit: true });
  });

  it("does not apply to ACP conversations (return null directly)", () => {
    expect(resolveRoomModel(
      { kind: "acp", id: "c1", acp: { providerId: "p" } },
      [globalModel],
      "global/claude",
    )).toBeNull();
  });

  it("returns null when no global model is set and the room has no binding", () => {
    expect(resolveRoomModel(
      { kind: "agent", id: "c1" },
      [globalModel],
      "",
    )).toEqual({ model: null, effort: "auto", source: "global", explicit: false });
  });
});
