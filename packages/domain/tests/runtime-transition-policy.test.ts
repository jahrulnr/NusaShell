import { describe, expect, it } from "vitest";
import {
  PluginId,
  PluginVersion,
  RuntimeTransitionPolicy,
  type PluginRuntimeState,
} from "../src/index.js";

describe("RuntimeTransitionPolicy", () => {
  const allowed: Array<[PluginRuntimeState, PluginRuntimeState]> = [
    ["idle", "starting"],
    ["starting", "running"],
    ["starting", "crashed"],
    ["running", "stopping"],
    ["running", "background"],
    ["running", "crashed"],
    ["background", "running"],
    ["background", "stopping"],
    ["background", "crashed"],
    ["stopping", "idle"],
    ["stopping", "crashed"],
    ["crashed", "starting"],
    ["crashed", "idle"],
    ["idle", "disabled"],
    ["crashed", "disabled"],
    ["disabled", "idle"],
  ];

  it.each(allowed)("allows %s -> %s", (from, to) => {
    expect(RuntimeTransitionPolicy.canTransition(from, to)).toBe(true);
    const result = RuntimeTransitionPolicy.assertTransition(from, to);
    expect(result.ok).toBe(true);
  });

  it("rejects idle -> running", () => {
    expect(RuntimeTransitionPolicy.canTransition("idle", "running")).toBe(false);
    const result = RuntimeTransitionPolicy.assertTransition("idle", "running");
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.error.code).toBe("INVALID_RUNTIME_TRANSITION");
      expect(result.error.details).toEqual({ from: "idle", to: "running" });
    }
  });

  it("rejects disabled -> starting", () => {
    expect(RuntimeTransitionPolicy.canTransition("disabled", "starting")).toBe(
      false,
    );
  });
});

describe("PluginId", () => {
  it("accepts valid ids", () => {
    const result = PluginId.create("nusashell.notes");
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(PluginId.toString(result.value)).toBe("nusashell.notes");
    }
  });

  it("rejects empty ids", () => {
    const result = PluginId.create("");
    expect(result.ok).toBe(false);
  });
});

describe("PluginVersion", () => {
  it("accepts semver", () => {
    const result = PluginVersion.create("1.0.0");
    expect(result.ok).toBe(true);
  });

  it("rejects invalid semver", () => {
    const result = PluginVersion.create("not-a-version");
    expect(result.ok).toBe(false);
  });
});
