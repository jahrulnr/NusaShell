import Database from "better-sqlite3";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));

export class SqliteDatabase {
  private readonly db: Database.Database;

  constructor(dbPath: string) {
    this.db = new Database(dbPath);
    this.db.pragma("journal_mode = WAL");
    this.runMigrations();
  }

  get raw(): Database.Database {
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
    const migrationFiles = ["001-init.sql"];

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
