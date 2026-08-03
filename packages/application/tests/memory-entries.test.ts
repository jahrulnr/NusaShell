import { describe, expect, it } from "vitest";
import {
  MEMORY_LIMIT,
  USER_LIMIT,
  ENTRY_DELIMITER,
  limitFor,
  splitEntries,
  joinEntries,
  charsOf,
  usageOf,
  checkCapacity,
  findUniqueMatch,
  MATCH_AMBIGUOUS,
  MATCH_NOT_FOUND,
  MATCH_EMPTY,
  addEntry,
  replaceEntry,
  removeEntry,
} from "../src/index.js";

describe("memory-entries helpers", () => {
  describe("splitEntries / joinEntries", () => {
    it("returns empty array for empty or whitespace-only string", () => {
      expect(splitEntries("")).toEqual([]);
      expect(splitEntries("   ")).toEqual([]);
      expect(splitEntries("\n\n")).toEqual([]);
    });

    it("splits a single legacy entry without createdAt", () => {
      expect(splitEntries("hello world")).toEqual([{ text: "hello world", createdAt: null }]);
    });

    it("splits multiple §-delimited entries and trims each", () => {
      expect(splitEntries("first\n§\nsecond\n§\nthird")).toEqual([
        { text: "first", createdAt: null },
        { text: "second", createdAt: null },
        { text: "third", createdAt: null },
      ]);
    });

    it("handles bare § delimiter", () => {
      expect(splitEntries("one§two§three")).toEqual([
        { text: "one", createdAt: null },
        { text: "two", createdAt: null },
        { text: "three", createdAt: null },
      ]);
    });

    it("parses and round-trips createdAt metadata without counting it toward capacity", () => {
      const entries = [
        { text: "alpha", createdAt: "2026-08-02T12:00:00.000Z" },
        { text: "beta", createdAt: null },
      ];
      const joined = joinEntries(entries);
      expect(joined).toContain("<!--ns-created:2026-08-02T12:00:00.000Z-->\nalpha");
      expect(splitEntries(joined)).toEqual(entries);
      expect(charsOf(entries)).toBe(5 + ENTRY_DELIMITER.length + 4);
    });
  });

  describe("charsOf / usageOf", () => {
    it("counts total chars of joined entry text only", () => {
      expect(charsOf([{ text: "ab", createdAt: null }, { text: "cd", createdAt: null }])).toBe(
        2 + ENTRY_DELIMITER.length + 2,
      );
    });

    it("usageOf returns chars and limit for target", () => {
      const usage = usageOf([{ text: "x", createdAt: null }], "memory");
      expect(usage.chars).toBe(1);
      expect(usage.limit).toBe(MEMORY_LIMIT);
    });

    it("usageOf respects user limit", () => {
      const usage = usageOf([], "user");
      expect(usage.limit).toBe(USER_LIMIT);
    });
  });

  describe("checkCapacity", () => {
    it("passes when under limit", () => {
      expect(checkCapacity([{ text: "short", createdAt: null }], "memory")).toEqual({ ok: true, overflow: 0 });
    });

    it("fails when over limit", () => {
      const long = "x".repeat(MEMORY_LIMIT + 10);
      expect(checkCapacity([{ text: long, createdAt: null }], "memory")).toEqual({ ok: false, overflow: 10 });
    });
  });

  describe("findUniqueMatch", () => {
    it("returns MATCH_EMPTY for empty oldText", () => {
      expect(findUniqueMatch([{ text: "abc", createdAt: null }], "")).toBe(MATCH_EMPTY);
      expect(findUniqueMatch([{ text: "abc", createdAt: null }], "  ")).toBe(MATCH_EMPTY);
    });

    it("returns index for unique substring match", () => {
      expect(findUniqueMatch([{ text: "foo bar", createdAt: null }, { text: "baz", createdAt: null }], "bar")).toBe(0);
      expect(findUniqueMatch([{ text: "foo", createdAt: null }, { text: "baz qux", createdAt: null }], "qux")).toBe(1);
    });

    it("returns MATCH_AMBIGUOUS for ambiguous match", () => {
      expect(
        findUniqueMatch([{ text: "alpha", createdAt: null }, { text: "alpha beta", createdAt: null }], "alpha"),
      ).toBe(MATCH_AMBIGUOUS);
    });

    it("returns MATCH_NOT_FOUND when no match found", () => {
      expect(findUniqueMatch([{ text: "foo", createdAt: null }], "xyz")).toBe(MATCH_NOT_FOUND);
    });
  });

  describe("addEntry", () => {
    it("appends a new entry with createdAt from the clock", () => {
      const result = addEntry([{ text: "a", createdAt: null }], "b", () => new Date("2026-08-03T01:00:00.000Z"));
      expect(result).toEqual([
        { text: "a", createdAt: null },
        { text: "b", createdAt: "2026-08-03T01:00:00.000Z" },
      ]);
    });

    it("ignores empty content", () => {
      const result = addEntry([{ text: "a", createdAt: null }], "  ");
      expect(result).toEqual([{ text: "a", createdAt: null }]);
    });
  });

  describe("replaceEntry", () => {
    it("replaces matched entry text and preserves createdAt", () => {
      const { entries, matchedIndex } = replaceEntry(
        [{ text: "old text", createdAt: "2026-01-01T00:00:00.000Z" }, { text: "keep", createdAt: null }],
        "old",
        "new text",
      );
      expect(matchedIndex).toBe(0);
      expect(entries).toEqual([
        { text: "new text", createdAt: "2026-01-01T00:00:00.000Z" },
        { text: "keep", createdAt: null },
      ]);
    });

    it("removes entry when new content is empty", () => {
      const { entries, matchedIndex } = replaceEntry([{ text: "remove me", createdAt: null }], "remove", "");
      expect(matchedIndex).toBe(0);
      expect(entries).toEqual([]);
    });

    it("returns MATCH_AMBIGUOUS for ambiguous match", () => {
      const { entries, matchedIndex } = replaceEntry(
        [{ text: "dup", createdAt: null }, { text: "dup", createdAt: null }],
        "dup",
        "new",
      );
      expect(matchedIndex).toBe(MATCH_AMBIGUOUS);
      expect(entries).toEqual([{ text: "dup", createdAt: null }, { text: "dup", createdAt: null }]);
    });

    it("returns MATCH_NOT_FOUND when no match", () => {
      const { entries, matchedIndex } = replaceEntry([{ text: "foo", createdAt: null }], "bar", "baz");
      expect(matchedIndex).toBe(MATCH_NOT_FOUND);
      expect(entries).toEqual([{ text: "foo", createdAt: null }]);
    });
  });

  describe("removeEntry", () => {
    it("removes matched entry", () => {
      const { entries, matchedIndex } = removeEntry(
        [{ text: "a", createdAt: null }, { text: "b", createdAt: null }],
        "b",
      );
      expect(matchedIndex).toBe(1);
      expect(entries).toEqual([{ text: "a", createdAt: null }]);
    });

    it("returns MATCH_NOT_FOUND for no match", () => {
      const { entries, matchedIndex } = removeEntry([{ text: "a", createdAt: null }], "z");
      expect(matchedIndex).toBe(MATCH_NOT_FOUND);
      expect(entries).toEqual([{ text: "a", createdAt: null }]);
    });
  });

  describe("limitFor", () => {
    it("returns MEMORY_LIMIT for memory target", () => {
      expect(limitFor("memory")).toBe(MEMORY_LIMIT);
    });

    it("returns USER_LIMIT for user target", () => {
      expect(limitFor("user")).toBe(USER_LIMIT);
    });
  });
});
