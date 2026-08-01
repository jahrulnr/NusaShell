import { describe, expect, it } from "vitest";
import { AskQuestionService } from "../src/agent/services/ask-question-service.js";

describe("AskQuestionService", () => {
  it("resolves an option answer into joined labels", async () => {
    const asks = new AskQuestionService();
    const pending = asks.ask("turn-1", "call-1", {
      question: "Pick",
      options: [
        { id: "a", label: "Alpha" },
        { id: "b", label: "Beta" },
      ],
      allowFreeText: true,
      multiSelect: true,
    });
    const result = asks.answer("turn-1", "call-1", { via: "option", optionIds: ["a", "b"] });
    expect(result.data).toEqual({ via: "option", answer: "Alpha, Beta", optionIds: ["a", "b"] });
    await expect(pending).resolves.toEqual(result);
  });

  it("rejects free text when not allowed", () => {
    const asks = new AskQuestionService();
    void asks.ask("turn-1", "call-1", {
      question: "Pick",
      options: [{ id: "a", label: "Alpha" }],
      allowFreeText: false,
      multiSelect: false,
    });
    expect(() => asks.answer("turn-1", "call-1", { via: "text", text: "other" })).toThrow(/not allowed/i);
  });

  it("combines selected options and custom text into one answer (multi-select + free text)", () => {
    const asks = new AskQuestionService();
    void asks.ask("turn-1", "call-1", {
      question: "Pick and elaborate",
      options: [
        { id: "a", label: "Alpha" },
        { id: "b", label: "Beta" },
      ],
      allowFreeText: true,
      multiSelect: true,
    });
    const result = asks.answer("turn-1", "call-1", {
      via: "option",
      optionIds: ["a", "b"],
      text: "also consider Gamma",
    });
    expect(result.data.answer).toBe("Alpha, Beta — also consider Gamma");
    expect(result.data.optionIds).toEqual(["a", "b"]);
    expect(result.data.text).toBe("also consider Gamma");
  });

  it("rejects supplementary text on option answers when free text is not allowed", () => {
    const asks = new AskQuestionService();
    void asks.ask("turn-1", "call-1", {
      question: "Pick",
      options: [{ id: "a", label: "Alpha" }],
      allowFreeText: false,
      multiSelect: true,
    });
    expect(() =>
      asks.answer("turn-1", "call-1", { via: "option", optionIds: ["a"], text: "extra" }),
    ).toThrow(/not allowed/i);
  });
});
