package automation

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRenameJSONKey renames the key at nested depth only.
func TestRenameJSONKey(t *testing.T) {
	in := `{"ID":"r1","Jobs":[{"FailureReason":"stale lease","Steps":[]}],"Status":"failed"}`
	out, err := renameJSONKey(in, "FailureReason", "Error")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	jobs := v["Jobs"].([]any)
	job := jobs[0].(map[string]any)
	if _, has := job["FailureReason"]; has {
		t.Fatal("FailureReason must be gone")
	}
	if got := job["Error"]; got != "stale lease" {
		t.Fatalf("Error = %v, want stale lease", got)
	}
}

// TestMigrateTaskStateJSONPreservesFailureMessage pins the one-time
// cleanup: an old-format run row (FailureReason) is rewritten to Error on
// open, and rows without the old key are untouched.
func TestMigrateTaskStateJSONPreservesFailureMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflows.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE runs (id TEXT PRIMARY KEY, workflow_id TEXT, workspace TEXT, status TEXT, json TEXT NOT NULL, created_at TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	oldBody := `{"ID":"run_old","Status":"failed","Jobs":[{"ID":"job_1","FailureReason":"build error","StartedAt":null}]}`
	cleanBody := `{"ID":"run_clean","Status":"success","Jobs":[{"ID":"job_2"}]}`
	if _, err := db.Exec(`INSERT INTO runs(id,workflow_id,workspace,status,json,created_at) VALUES('run_old','wf','','failed',?,'2026-08-31T00:00:00Z'),('run_clean','wf','','success',?,'2026-08-31T00:00:00Z')`, oldBody, cleanBody); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer store.Close()

	var got string
	if err := store.db.QueryRow(`SELECT json FROM runs WHERE id='run_old'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	job := v["Jobs"].([]any)[0].(map[string]any)
	if _, has := job["FailureReason"]; has {
		t.Fatal("FailureReason must be migrated")
	}
	if job["Error"] != "build error" {
		t.Fatalf("Error = %v, want build error", job["Error"])
	}

	// Idempotent: reopening does not change the migrated row.
	var again string
	if err := store.db.QueryRow(`SELECT json FROM runs WHERE id='run_old'`).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatal("migration must be idempotent")
	}

	// Clean rows untouched.
	var clean string
	if err := store.db.QueryRow(`SELECT json FROM runs WHERE id='run_clean'`).Scan(&clean); err != nil {
		t.Fatal(err)
	}
	if clean != cleanBody {
		t.Fatalf("clean row changed: %s", clean)
	}
	_ = os.Remove(path)
}
