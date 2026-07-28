import { defineConfig } from "vite";

export default defineConfig({
  build: {
    outDir: ".vite/build",
    lib: {
      entry: "src/preload/index.cjs",
      formats: ["cjs"],
      fileName: () => "preload.cjs",
    },
    rollupOptions: {
      external: ["electron"],
      output: {
        entryFileNames: "preload.cjs",
      },
    },
    emptyOutDir: false,
  },
});
