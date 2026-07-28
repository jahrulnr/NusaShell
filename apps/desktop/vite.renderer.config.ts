import { defineConfig } from "vite";
import { resolve } from "node:path";

export default defineConfig({
  root: resolve(__dirname, "src", "renderer"),
  build: {
    outDir: resolve(__dirname, ".vite", "renderer"),
    emptyOutDir: true,
    rollupOptions: {
      input: resolve(__dirname, "src", "renderer", "index.html"),
    },
  },
});
