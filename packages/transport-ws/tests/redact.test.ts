import { describe, expect, it } from "vitest";
import { redactArgs, redactString, redactValue } from "../src/mapping/redact.js";

describe("redactArgs", () => {
  it("redacts values whose key matches a secret pattern", () => {
    expect(redactArgs({ password: "hunter2", apiKey: "sk-abc123", name: "notes" })).toEqual({
      password: "[REDACTED]",
      apiKey: "[REDACTED]",
      name: "notes",
    });
  });

  it("redacts nested objects", () => {
    expect(redactArgs({ config: { token: "abc", port: 8080 } })).toEqual({
      config: { token: "[REDACTED]", port: 8080 },
    });
  });

  it("redacts strings inside non-secret keys that contain bearer tokens", () => {
    const out = redactArgs({ header: "Authorization: Bearer abc123def456ghi789jkl012mno345" });
    expect(out.header).toBe("Authorization: Bearer [REDACTED]");
  });

  it("passes through primitives and null", () => {
    expect(redactArgs({ count: 5, active: true, note: null })).toEqual({
      count: 5,
      active: true,
      note: null,
    });
  });
});

describe("redactString", () => {
  it("redacts Bearer tokens", () => {
    expect(redactString("Bearer abc123def456ghi789jkl012mno345")).toBe("Bearer [REDACTED]");
  });

  it("redacts Authorization headers", () => {
    expect(redactString("Authorization: Basic dXNlcjpwYXNzYWRtaW4=")).toBe("Authorization: [REDACTED]");
  });

  it("redacts sk- prefixed API keys", () => {
    expect(redactString("key: sk-1234567890abcdefghijklmnopqrstuvwxyz")).toBe("key: sk-[REDACTED]");
  });

  it("does not redact short strings", () => {
    expect(redactString("hello world")).toBe("hello world");
  });

  it("does not apply long-token pattern to large base64 payloads", () => {
    const big = "A".repeat(3000);
    expect(redactString(big)).toBe(big);
  });
});

describe("redactValue", () => {
  it("redacts strings", () => {
    expect(redactValue("Bearer abc123def456ghi789jkl012mno345")).toBe("Bearer [REDACTED]");
  });

  it("redacts objects", () => {
    expect(redactValue({ secret: "abc" })).toEqual({ secret: "[REDACTED]" });
  });

  it("redacts arrays of objects", () => {
    expect(redactValue([{ token: "x" }, { name: "ok" }])).toEqual([
      { token: "[REDACTED]" },
      { name: "ok" },
    ]);
  });

  it("passes through null and numbers", () => {
    expect(redactValue(null)).toBe(null);
    expect(redactValue(42)).toBe(42);
  });
});
