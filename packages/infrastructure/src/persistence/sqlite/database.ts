import type DatabaseType from "better-sqlite3";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));

// Lazy-load better-sqlite3 only when a database is actually created.
// This prevents a SIGSEGV when Electron loads the bundle but SQLite is not used.
function loadBetterSqlite3(): typeof import("better-sqlite3") {
  return require("better-sqlite3");
}

export class SqliteDatabase {
  private readonly db: DatabaseType.Database;

  constructor(dbPath: string) {
    const Database = loadBetterSqlite3();
    this.db = new Database(dbPath);
    this.db.pragma("journal_mode = WAL");
    this.runMigrations();
  }

  get raw(): DatabaseType.Database {
    return this.db;
  }

  close(): void {
    this.db.close();
  }

  private runMigrations(): void {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY,
        applied_at TEXT NOT NULL
      );
    `);

    const migrationsDir = join(__dirname, "migrations");
    const migrationFiles = ["001-init.sql", "002-jobs.sql", "003-job-outputs-traceid.sql"];

    for (const file of migrationFiles) {
      const version = parseInt(file.split("-")[0]!, 10);
      const applied = this.db
        .prepare("SELECT version FROM schema_migrations WHERE version = ?")
        .get(version) as { version: number } | undefined;

      if (applied) continue;

      const sql = readFileSync(join(migrationsDir, file), "utf-8");
      this.db.exec(sql);
      this.db
        .prepare("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)")
        .run(version, new Date().toISOString());
    }
  }
}
