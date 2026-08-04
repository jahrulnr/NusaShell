import { describe, expect, it } from "vitest";
import { computeAcpTryOrder } from "../src/main/acp-routing-order.js";

describe("computeAcpTryOrder", () => {
  it("returns empty for no connected providers", () => {
    expect(computeAcpTryOrder({ connectedIds: [] })).toEqual([]);
  });

  it("puts default first when connected", () => {
    expect(
      computeAcpTryOrder({
        defaultProviderId: "gemini",
        fallbackProviderIds: ["cursor"],
        connectedIds: ["cursor", "gemini"],
      }),
    ).toEqual(["gemini", "cursor"]);
  });

  it("skips default when not connected", () => {
    expect(
      computeAcpTryOrder({
        defaultProviderId: "codex",
        fallbackProviderIds: ["cursor"],
        connectedIds: ["cursor", "gemini"],
      }),
    ).toEqual(["cursor", "gemini"]);
  });

  it("filters fallback to connected only", () => {
    expect(
      computeAcpTryOrder({
        fallbackProviderIds: ["codex", "cursor"],
        connectedIds: ["cursor", "gemini"],
      }),
    ).toEqual(["cursor", "gemini"]);
  });

  it("falls back to manifest order when no routing configured", () => {
    expect(
      computeAcpTryOrder({ connectedIds: ["cursor", "gemini", "codex"] }),
    ).toEqual(["cursor", "gemini", "codex"]);
  });

  it("deduplicates: default in fallback does not appear twice", () => {
    expect(
      computeAcpTryOrder({
        defaultProviderId: "gemini",
        fallbackProviderIds: ["gemini", "cursor"],
        connectedIds: ["cursor", "gemini"],
      }),
    ).toEqual(["gemini", "cursor"]);
  });
});
