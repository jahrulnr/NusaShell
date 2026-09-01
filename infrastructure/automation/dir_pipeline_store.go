package automation

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nusashell/domain"
)

// PipelinesDir is the subdirectory under the data dir that holds one
// YAML file per pipeline definition. Files are the source of truth for
// file-sourced workflows; the Automation workflow registry (workflows.db) is
// the scheduler index that mirrors them.
const PipelinesDir = "automation/pipelines"

// DirPipelineStore discovers pipeline YAML files under a data
// directory. Each <name>.yaml file becomes a WorkflowDefinition with
// ID <name> and Source.Kind "file". Unparseable files are still listed
// (Enabled=false, Source.ParseError set) so they appear as invalid
// instead of vanishing from the registry.
type DirPipelineStore struct {
	Root string // absolute path to the pipelines directory
}

// Discover scans the pipelines directory, parses every .yaml file, and
// returns the resulting workflow definitions sorted by ID. The
// directory is created if it does not exist.
func (s DirPipelineStore) Discover() ([]*domain.WorkflowDefinition, error) {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	var out []*domain.WorkflowDefinition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(s.Root, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".yaml")
		w, err := ParseYAML(raw)
		if err != nil {
			out = append(out, &domain.WorkflowDefinition{
				ID:      id,
				Name:    id,
				Enabled: false,
				Source:  domain.WorkflowSource{Kind: "file", Path: path, ParseError: err.Error()},
			})
			continue
		}
		w.ID = id
		if w.Name == "" {
			w.Name = id
		}
		w.Source = domain.WorkflowSource{Kind: "file", Path: path}
		if len(w.Triggers) == 0 {
			w.Triggers = []domain.Trigger{{ID: "t1", Kind: domain.TriggerManual, Family: domain.FamilyManual, Manual: true}}
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
