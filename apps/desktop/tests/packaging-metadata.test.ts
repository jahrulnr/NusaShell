import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

describe("desktop packaging metadata", () => {
  it("declares the author required by Windows packaging", () => {
    const packageJson = JSON.parse(
      readFileSync(new URL("../package.json", import.meta.url), "utf8"),
    ) as { readonly author?: unknown };

    expect(packageJson.author).toEqual(expect.any(String));
    expect((packageJson.author as string).trim()).not.toBe("");
  });
});
