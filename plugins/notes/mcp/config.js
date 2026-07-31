import path from "node:path";
import { fileURLToPath } from "node:url";

let _dirname;
try {
  const _filename = fileURLToPath(import.meta.url);
  _dirname = path.dirname(_filename);
} catch {
  _dirname = typeof __dirname !== "undefined" ? __dirname : process.cwd();
}

export function notesDataFile() {
  const envFile = process.env.NUSASHELL_NOTES_DATA_FILE;
  if (envFile) return path.resolve(envFile);
  return path.join(_dirname, "..", "notes.json");
}
