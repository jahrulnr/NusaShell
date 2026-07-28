// Electron entry point — bootstraps the TypeScript main process via tsx.
// This file is plain JS so Electron can load it directly, then it
// registers tsx as the ESM loader and imports the real main process.
import { register } from "node:module";
import { pathToFileURL } from "node:url";

register("tsx/esm", pathToFileURL("./").href);

import("./src/main/index.ts");
