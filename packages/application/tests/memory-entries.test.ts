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

    it("splits a single entry", () => {
      expect(splitEntries("hello world")).toEqual([{ text: "hello world" }]);
    });

    it("splits multiple §-delimited entries and trims each", () => {
      expect(splitEntries("first\n§\nsecond\n§\nthird")).toEqual([
        { text: "first" },
        { text: "second" },
        { text: "third" },
      ]);
    });

    it("handles bare § delimiter", () => {
      expect(splitEntries("one§two§three")).toEqual([
        { text: "one" },
        { text: "two" },
        { text: "three" },
      ]);
    });

    it("round-trips join then split", () => {
      const entries = [{ text: "alpha" }, { text: "beta" }];
      const joined = joinEntries(entries);
      expect(splitEntries(joined)).toEqual(entries);
    });
  });

  describe("charsOf / usageOf", () => {
    it("counts total chars of joined entries", () => {
      expect(charsOf([{ text: "ab" }, { text: "cd" }])).toBe(2 + ENTRY_DELIMITER.length + 2);
    });

    it("usageOf returns chars and limit for target", () => {
      const usage = usageOf([{ text: "x" }], "memory");
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
      expect(checkCapacity([{ text: "short" }], "memory")).toEqual({ ok: true, overflow: 0 });
    });

    it("fails when over limit", () => {
      const long = "x".repeat(MEMORY_LIMIT + 10);
      expect(checkCapacity([{ text: long }], "memory")).toEqual({ ok: false, overflow: 10 });
    });
  });

  describe("findUniqueMatch", () => {
    it("returns MATCH_EMPTY for empty oldText", () => {
      expect(findUniqueMatch([{ text: "abc" }], "")).toBe(MATCH_EMPTY);
      expect(findUniqueMatch([{ text: "abc" }], "  ")).toBe(MATCH_EMPTY);
    });

    it("returns index for unique substring match", () => {
      expect(findUniqueMatch([{ text: "foo bar" }, { text: "baz" }], "bar")).toBe(0);
      expect(findUniqueMatch([{ text: "foo" }, { text: "baz qux" }], "qux")).toBe(1);
    });

    it("returns MATCH_AMBIGUOUS for ambiguous match", () => {
      expect(findUniqueMatch([{ text: "alpha" }, { text: "alpha beta" }], "alpha")).toBe(MATCH_AMBIGUOUS);
    });

    it("returns MATCH_NOT_FOUND when no match found", () => {
      expect(findUniqueMatch([{ text: "foo" }], "xyz")).toBe(MATCH_NOT_FOUND);
    });
  });

  describe("addEntry", () => {
    it("appends a new entry", () => {
      const result = addEntry([{ text: "a" }], "b");
      expect(result).toEqual([{ text: "a" }, { text: "b" }]);
    });

    it("ignores empty content", () => {
      const result = addEntry([{ text: "a" }], "  ");
      expect(result).toEqual([{ text: "a" }]);
    });
  });

  describe("replaceEntry", () => {
    it("replaces matched entry text", () => {
      const { entries, matchedIndex } = replaceEntry([{ text: "old text" }, { text: "keep" }], "old", "new text");
      expect(matchedIndex).toBe(0);
      expect(entries).toEqual([{ text: "new text" }, { text: "keep" }]);
    });

    it("removes entry when new content is empty", () => {
      const { entries, matchedIndex } = replaceEntry([{ text: "remove me" }], "remove", "");
      expect(matchedIndex).toBe(0);
      expect(entries).toEqual([]);
    });

    it("returns MATCH_AMBIGUOUS for ambiguous match", () => {
      const { entries, matchedIndex } = replaceEntry([{ text: "dup" }, { text: "dup" }], "dup", "new");
      expect(matchedIndex).toBe(MATCH_AMBIGUOUS);
      expect(entries).toEqual([{ text: "dup" }, { text: "dup" }]);
    });

    it("returns MATCH_NOT_FOUND when no match", () => {
      const { entries, matchedIndex } = replaceEntry([{ text: "foo" }], "bar", "baz");
      expect(matchedIndex).toBe(MATCH_NOT_FOUND);
      expect(entries).toEqual([{ text: "foo" }]);
    });
  });

  describe("removeEntry", () => {
    it("removes matched entry", () => {
      const { entries, matchedIndex } = removeEntry([{ text: "a" }, { text: "b" }], "b");
      expect(matchedIndex).toBe(1);
      expect(entries).toEqual([{ text: "a" }]);
    });

    it("returns MATCH_NOT_FOUND for no match", () => {
      const { entries, matchedIndex } = removeEntry([{ text: "a" }], "z");
      expect(matchedIndex).toBe(MATCH_NOT_FOUND);
      expect(entries).toEqual([{ text: "a" }]);
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
