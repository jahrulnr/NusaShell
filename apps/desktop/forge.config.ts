import type { ForgeConfig } from "@electron-forge/shared-types";
import { VitePlugin } from "@electron-forge/plugin-vite";
import { MakerAppImage } from "@reforged/maker-appimage";
import { MakerDeb } from "@electron-forge/maker-deb";
import { PublisherGithub } from "@electron-forge/publisher-github";
import { resolve } from "node:path";

const config: ForgeConfig = {
  packagerConfig: {
    name: "NusaShell",
    asar: true,
    extraResource: [resolve(__dirname, "..", "..", "plugins", "examples")],
  },
  makers: [
    new MakerAppImage({
      options: {
        name: "nusashell",
      },
    }),
    new MakerDeb({
      options: {
        name: "nusashell",
      },
    }),
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
