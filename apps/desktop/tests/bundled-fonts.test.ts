import { describe, expect, it } from "vitest";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const rendererDir = fileURLToPath(new URL("../src/renderer/", import.meta.url));
const fontsDir = join(rendererDir, "fonts");
const fontsCssPath = join(fontsDir, "fonts.css");
const indexHtmlPath = join(rendererDir, "index.html");

describe("bundled UI fonts", () => {
  it("loads local fonts.css from the renderer instead of Google Fonts", () => {
    const indexHtml = readFileSync(indexHtmlPath, "utf8");
    expect(indexHtml).toContain('href="fonts/fonts.css"');
    expect(indexHtml).not.toMatch(/fonts\.googleapis\.com|fonts\.gstatic\.com/);
  });

  it("does not reference remote font hosts under the desktop renderer", () => {
    const offlineProbe = /fonts\.googleapis\.com|fonts\.gstatic\.com/;
    for (const rel of ["index.html", "fonts/fonts.css"]) {
      const text = readFileSync(join(rendererDir, rel), "utf8");
      expect(text).not.toMatch(offlineProbe);
    }
  });

  it("ships every woff2 referenced by fonts.css", () => {
    expect(existsSync(fontsCssPath)).toBe(true);
    const fontsCss = readFileSync(fontsCssPath, "utf8");
    const localRefs = [...fontsCss.matchAll(/url\(\.\/([^)]+\.woff2)\)/g)].map((match) => match[1]);
    expect(localRefs.length).toBeGreaterThan(0);

    for (const fileName of new Set(localRefs)) {
      const absolute = join(fontsDir, fileName);
      expect(existsSync(absolute), `missing font file: ${fileName}`).toBe(true);
      expect(readFileSync(absolute).byteLength).toBeGreaterThan(1000);
    }

    const onDisk = readdirSync(fontsDir).filter((name) => name.endsWith(".woff2"));
    expect(onDisk.length).toBe(new Set(localRefs).size);
  });

  it("declares the same families and weights the app previously loaded from Google Fonts", () => {
    const fontsCss = readFileSync(fontsCssPath, "utf8");
    for (const family of ["IBM Plex Mono", "IBM Plex Sans", "Space Grotesk"]) {
      expect(fontsCss).toContain(`font-family: '${family}'`);
    }

    const faceWeights = (family: string) =>
      [...fontsCss.matchAll(
        new RegExp(
          `font-family: '${family}';\\s*font-style: normal;\\s*font-weight: (\\d+);`,
          "g",
        ),
      )].map((match) => Number(match[1]));

    expect(new Set(faceWeights("IBM Plex Sans"))).toEqual(new Set([400, 500, 600]));
    expect(new Set(faceWeights("IBM Plex Mono"))).toEqual(new Set([400, 500, 600]));
    expect(new Set(faceWeights("Space Grotesk"))).toEqual(new Set([500, 600, 700]));
    expect(fontsCss).toContain("font-display: swap");
  });
});
