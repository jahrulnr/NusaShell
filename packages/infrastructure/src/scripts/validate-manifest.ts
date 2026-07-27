import { readFile, stat, readdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { ManifestSchema } from "@nusashell/contracts";

async function validateFile(manifestPath: string): Promise<boolean> {
  let raw: string;
  try {
    raw = await readFile(manifestPath, "utf-8");
  } catch (err) {
    console.error(`  ✗ Cannot read: ${manifestPath} — ${String(err)}`);
    return false;
  }

  let parsedJson: unknown;
  try {
    parsedJson = JSON.parse(raw);
  } catch (err) {
    console.error(`  ✗ Invalid JSON: ${manifestPath} — ${String(err)}`);
    return false;
  }

  const result = ManifestSchema.safeParse(parsedJson);
  if (result.success) {
    console.log(`  ✓ ${manifestPath} — valid (id: ${result.data.id}, name: ${result.data.name})`);
    return true;
  }

  console.error(`  ✗ ${manifestPath} — validation failed:`);
  for (const issue of result.error.issues) {
    console.error(`    ${issue.path.join(".")}: ${issue.message}`);
  }
  return false;
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  if (args.length === 0) {
    console.error("Usage: validate-manifest <path-to-manifest.json | plugins-root-dir>");
    process.exit(1);
  }

  const target = resolve(args[0]!);
  let info;
  try {
    info = await stat(target);
  } catch (err) {
    console.error(`Error: cannot access "${target}" — ${String(err)}`);
    process.exit(1);
  }

  const paths: string[] = [];

  if (info.isFile()) {
    paths.push(target);
  } else if (info.isDirectory()) {
    let entries: string[];
    try {
      entries = await readdir(target);
    } catch (err) {
      console.error(`Error: cannot read directory "${target}" — ${String(err)}`);
      process.exit(1);
    }
    for (const entry of entries) {
      const fullPath = join(target, entry);
      let entryInfo;
      try {
        entryInfo = await stat(fullPath);
      } catch {
        continue;
      }
      if (entryInfo.isDirectory()) {
        paths.push(join(fullPath, "manifest.json"));
      }
    }
  }

  if (paths.length === 0) {
    console.error("No manifest.json files found.");
    process.exit(1);
  }

  console.log(`Validating ${paths.length} manifest(s)...\n`);

  let allOk = true;
  for (const p of paths) {
    const ok = await validateFile(p);
    if (!ok) allOk = false;
  }

  console.log("");
  if (allOk) {
    console.log("All manifests valid.");
    process.exit(0);
  } else {
    console.error("Some manifests failed validation.");
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(`Unexpected error: ${String(err)}`);
  process.exit(1);
});
