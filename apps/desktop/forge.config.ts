import type { ForgeConfig } from "@electron-forge/shared-types";
import { VitePlugin } from "@electron-forge/plugin-vite";
import { MakerAppImage } from "@reforged/maker-appimage";
import { MakerDeb } from "@electron-forge/maker-deb";
import { PublisherGithub } from "@electron-forge/publisher-github";
import { resolve } from "node:path";
import { readFileSync, writeFileSync } from "node:fs";
import { stageRuntimeDependencies } from "./scripts/package-runtime-dependencies.js";

const releaseVersion = readFileSync(resolve(__dirname, "..", "..", "VERSION"), "utf8").trim();

const config: ForgeConfig = {
  packagerConfig: {
    name: "NusaShell",
    appVersion: releaseVersion,
    buildVersion: releaseVersion,
    asar: {
      unpack: "**/*.node",
    },
    extraResource: [
      resolve(__dirname, "..", "..", "plugins"),
      resolve(__dirname, "..", "..", "resources", "agent"),
      resolve(__dirname, "assets", "nusashell.png"),
    ],
  },
  hooks: {
    readPackageJson: async (_forgeConfig, packageJson) => ({
      ...packageJson,
      version: releaseVersion,
    }),
    packageAfterCopy: async (_forgeConfig, buildPath) => {
      await stageRuntimeDependencies({ buildPath });
    },
    packageAfterPrune: async (_forgeConfig, buildPath) => {
      const packageJsonPath = resolve(buildPath, "package.json");
      const packageJson = JSON.parse(readFileSync(packageJsonPath, "utf8")) as Record<string, unknown>;

      packageJson.version = releaseVersion;
      writeFileSync(packageJsonPath, `${JSON.stringify(packageJson, null, 2)}\n`);
    },
  },
  makers: [
    new MakerAppImage({
      options: {
        name: "nusashell",
        productName: "NusaShell",
        bin: "NusaShell",
        icon: resolve(__dirname, "assets", "nusashell.png"),
      },
    }),
    new MakerDeb({
      options: {
        name: "nusashell",
        productName: "NusaShell",
        bin: "NusaShell",
        description: "A desktop shell for AI tools and visual MCP plugins.",
        maintainer: "NusaShell",
        icon: resolve(__dirname, "assets", "nusashell.png"),
        desktopTemplate: resolve(__dirname, "assets", "nusashell.desktop.ejs"),
      },
    }),
    {
      name: "@electron-forge/maker-zip",
      platforms: ["darwin", "win32"],
      config: {},
    },
  ],
  publishers: [
    new PublisherGithub({
      repository: {
        owner: process.env.GITHUB_REPO_OWNER ?? "nusashell",
        name: process.env.GITHUB_REPO_NAME ?? "nusashell",
      },
      prerelease: true,
    }),
  ],
  plugins: [
    new VitePlugin({
      build: [
        {
          entry: "src/main/index.ts",
          config: "vite.main.config.ts",
          target: "main",
        },
        {
          entry: "src/preload/index.ts",
          config: "vite.preload.config.ts",
          target: "preload",
        },
      ],
      renderer: [
        {
          name: "main_window",
          config: "vite.renderer.config.ts",
        },
      ],
    }),
  ],
};

export default config;
