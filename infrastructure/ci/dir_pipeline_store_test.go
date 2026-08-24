package ci

import (
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestDirPipelineStoreDiscover(t *testing.T) {
	dir := t.TempDir()
	pipelinesDir := filepath.Join(dir, "ci", "pipelines")
	if err := os.MkdirAll(pipelinesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelinesDir, "deploy.yaml"), []byte(`
name: Deploy
triggers:
  manual: true
jobs:
  build:
    steps:
      - run: make build
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelinesDir, "nightly.yaml"), []byte(`
name: Nightly
triggers:
  - every:
      cron: "0 0 * * *"
jobs:
  test:
    steps:
      - run: make test
`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := DirPipelineStore{Root: pipelinesDir}
	defs, err := store.Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
	byID := map[string]*domain.WorkflowDefinition{}
	for _, d := range defs {
		byID[d.ID] = d
	}
	deploy, ok := byID["deploy"]
	if !ok {
		t.Fatal("missing deploy pipeline")
	}
	if deploy.Name != "Deploy" {
		t.Fatalf("deploy name = %q", deploy.Name)
	}
	if deploy.Source.Kind != "file" {
		t.Fatalf("deploy source kind = %q, want file", deploy.Source.Kind)
	}
	if deploy.Source.Path == "" {
		t.Fatal("deploy source path is empty")
	}
}

func TestDirPipelineStoreDiscoverSkipsCorrupt(t *testing.T) {
	dir := t.TempDir()
	pipelinesDir := filepath.Join(dir, "ci", "pipelines")
	if err := os.MkdirAll(pipelinesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelinesDir, "good.yaml"), []byte(`
name: Good
triggers:
  manual: true
jobs:
  run:
    steps:
      - run: echo ok
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pipelinesDir, "bad.yaml"), []byte("not: valid: yaml: {{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := DirPipelineStore{Root: pipelinesDir}
	defs, err := store.Discover()
	if err != nil {
		t.Fatalf("Discover should skip corrupt files, got: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != "good" {
		t.Fatalf("expected only good pipeline, got %+v", defs)
	}
}

func TestDirPipelineStoreDiscoverEmptyDir(t *testing.T) {
	dir := t.TempDir()
	pipelinesDir := filepath.Join(dir, "ci", "pipelines")
	if err := os.MkdirAll(pipelinesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	store := DirPipelineStore{Root: pipelinesDir}
	defs, err := store.Discover()
	if err != nil {
		t.Fatalf("Discover on empty dir: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected 0 definitions, got %d", len(defs))
	}
}

func TestDirPipelineStoreDiscoverCreatesDir(t *testing.T) {
	dir := t.TempDir()
	pipelinesDir := filepath.Join(dir, "ci", "pipelines")

	store := DirPipelineStore{Root: pipelinesDir}
	_, err := store.Discover()
	if err != nil {
		t.Fatalf("Discover should create missing dir, got: %v", err)
	}
	if _, err := os.Stat(pipelinesDir); err != nil {
		t.Fatalf("dir was not created: %v", err)
	}
}
