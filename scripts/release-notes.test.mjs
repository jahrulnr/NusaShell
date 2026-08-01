import { describe, expect, it } from "vitest";
import { extractReleaseNotes } from "./release-notes.mjs";

describe("extractReleaseNotes", () => {
  it("returns only the changelog body matching the VERSION value", () => {
    const changelog = `# Changelog

## [0.0.55] - 2026-08-02

### Added

- Cross-platform builds.

### Fixed

- Release metadata.

## [0.0.54] - 2026-08-01

### Added

- Previous release.
`;

    expect(extractReleaseNotes(changelog, "0.0.55\n")).toBe(`### Added

- Cross-platform builds.

### Fixed

- Release metadata.`);
  });

  it("accepts a v-prefixed version without changing the changelog format", () => {
    const changelog = "## [1.2.3] - 2026-08-02\n\n### Fixed\n\n- A fix.\n";

    expect(extractReleaseNotes(changelog, "v1.2.3")).toBe("### Fixed\n\n- A fix.");
  });

  it("fails when CHANGELOG.md has no section for the requested version", () => {
    const changelog = "## [1.2.2] - 2026-08-01\n\n### Added\n\n- Older.\n";

    expect(() => extractReleaseNotes(changelog, "1.2.3")).toThrow(
      "CHANGELOG.md has no section for version 1.2.3",
    );
  });

  it("fails when the matching changelog section has no release notes", () => {
    const changelog = "## [1.2.3] - 2026-08-02\n\n## [1.2.2] - 2026-08-01\n";

    expect(() => extractReleaseNotes(changelog, "1.2.3")).toThrow(
      "CHANGELOG.md section for version 1.2.3 is empty",
    );
  });
});
