import { defineConfig } from "vite";

export default defineConfig({
  build: {
    outDir: ".vite/build",
    lib: {
      entry: "src/main/index.ts",
      formats: ["cjs"],
      fileName: () => "main.cjs",
    },
    rollupOptions: {
      external: ["electron", "better-sqlite3", "ws", "bufferutil", "utf-8-validate", "electron-updater"],
    },
    emptyOutDir: true,
  },
  resolve: {
    // Bundle workspace packages — they're ESM and Vite handles them
    conditions: ["import", "module", "default"],
  },
});
