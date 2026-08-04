import { describe, expect, it } from "vitest";
import { GeminiAcpExtension } from "../src/acp/extensions/gemini-acp-extension.js";

const geminiSessionNew = {
  sessionId: "sess-1",
  modes: {
    currentModeId: "default",
    availableModes: [
      { id: "default", name: "Default", description: "Ask before acting" },
      { id: "autoEdit", name: "Auto Edit", description: "Apply edits automatically" },
      { id: "yolo", name: "YOLO", description: "Auto-apply all tool permissions" },
      { id: "plan", name: "Plan", description: "Plan only, no execution" },
    ],
  },
  models: {
    currentModelId: "gemini-2.5-pro",
    availableModels: [
      { modelId: "gemini-2.5-pro", name: "Gemini 2.5 Pro", description: "Frontier model" },
      { modelId: "gemini-2.5-flash", name: "Gemini 2.5 Flash", description: "Fast and lightweight" },
    ],
  },
};

describe("GeminiAcpExtension", () => {
  const ext = new GeminiAcpExtension();

  it("matches only the gemini providerId", () => {
    expect(ext.matches("gemini")).toBe(true);
    expect(ext.matches("cursor")).toBe(false);
    expect(ext.matches("codex")).toBe(false);
  });

  describe("normalizeSessionConfig", () => {
    it("maps modes+models into synthetic mode+model configOptions", () => {
      const options = ext.normalizeSessionConfig!(geminiSessionNew);
      expect(options).toBeDefined();
      expect(options).toHaveLength(2);
      const mode = options!.find((o) => o.id === "mode");
      expect(mode).toBeDefined();
      expect(mode!.type).toBe("select");
      expect(mode!.currentValue).toBe("default");
      expect(mode!.options).toHaveLength(4);
      expect(mode!.options![0]).toEqual({
        value: "default",
        name: "Default",
        description: "Ask before acting",
      });
      const model = options!.find((o) => o.id === "model");
      expect(model).toBeDefined();
      expect(model!.type).toBe("select");
      expect(model!.currentValue).toBe("gemini-2.5-pro");
      expect(model!.options).toHaveLength(2);
      expect(model!.options![0]).toEqual({
        value: "gemini-2.5-pro",
        name: "Gemini 2.5 Pro",
        description: "Frontier model",
      });
    });

    it("returns undefined for baseline configOptions shape (defer to core parser)", () => {
      const baseline = { sessionId: "s", configOptions: [{ id: "x", name: "X", type: "select", currentValue: "a" }] };
      expect(ext.normalizeSessionConfig!(baseline)).toBeUndefined();
    });

    it("returns undefined when modes+models are both absent", () => {
      expect(ext.normalizeSessionConfig!({ sessionId: "s" })).toBeUndefined();
    });

    it("handles missing currentModeId gracefully", () => {
      const partial = {
        sessionId: "s",
        modes: { availableModes: [{ id: "yolo", name: "YOLO" }] },
        models: { availableModels: [{ modelId: "m1", name: "M1" }] },
      };
      const options = ext.normalizeSessionConfig!(partial);
      expect(options).toBeDefined();
      expect(options!.find((o) => o.id === "mode")!.currentValue).toBe("");
    });
  });

  describe("applyConfigOption", () => {
    it("routes mode to session/set_mode with { sessionId, modeId }", () => {
      const d = ext.applyConfigOption!("sess-1", "mode", "yolo");
      expect(d).toBeDefined();
      expect(d!.method).toBe("session/set_mode");
      expect(d!.params).toEqual({ sessionId: "sess-1", modeId: "yolo" });
    });

    it("routes model to session/set_model with { sessionId, modelId }", () => {
      const d = ext.applyConfigOption!("sess-1", "model", "gemini-2.5-flash");
      expect(d).toBeDefined();
      expect(d!.method).toBe("session/set_model");
      expect(d!.params).toEqual({ sessionId: "sess-1", modelId: "gemini-2.5-flash" });
    });

    it("returns undefined for unknown configId (fall back to baseline)", () => {
      expect(ext.applyConfigOption!("sess-1", "unknown", "x")).toBeUndefined();
    });

    it("returns undefined for mode with boolean value (not a select change)", () => {
      expect(ext.applyConfigOption!("sess-1", "mode", true)).toBeUndefined();
    });
  });
});
