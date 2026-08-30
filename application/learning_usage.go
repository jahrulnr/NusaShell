package application

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"nusashell/domain"
)

const (
	learningUsedWithWeight = 0.45
	maxLearningIDsPerTool  = 24
)

// learningNodeIDsFromTool extracts learning nodes that a successful tool
// exposed or changed. Tool outputs use YAML front matter plus JSONL, so the
// parser deliberately reads only structured id fields instead of searching
// arbitrary prose for ID-shaped strings.
func learningNodeIDsFromTool(app *App, toolCall domain.ToolCall, output string) []string {
	name := strings.TrimSpace(toolCall.Name)
	args := learningToolArgs(toolCall.Args)
	root, op := name, ""
	if strings.HasPrefix(name, "memory_") {
		root, op = "memory", strings.TrimPrefix(name, "memory_")
	} else if strings.HasPrefix(name, "skill_") {
		root, op = "skill", strings.TrimPrefix(name, "skill_")
	} else if name == "memory" || name == "skill" {
		op = learningStringArg(args, "op")
	}

	if name == "file_read" {
		return learningNodeIDsFromFilePath(app, learningStringArg(args, "path"))
	}
	if root != "memory" && root != "skill" {
		return nil
	}

	ids := extractLearningOutputIDs(output)
	switch root {
	case "memory":
		switch op {
		case "replace":
			if learningStringArg(args, "target") == "fragment" {
				ids = appendLearningID(ids, learningStringArg(args, "id"))
			}
			if learningStringArg(args, "target") == "primary" && app != nil && app.Primary != nil {
				if primary := app.Primary.Load(); primary != nil {
					for _, entry := range primary.Entries {
						ids = appendLearningID(ids, entry.ID)
					}
				}
			}
		}
	case "skill":
		// A save of an existing skill can be identified from args even when
		// an older toolbox output did not include the saved ID. For a new
		// skill, resolve its name after the save completed.
		if op == "save" && app != nil && app.Skills != nil {
			if id := learningStringArg(args, "id"); id != "" {
				ids = appendLearningID(ids, id)
			} else if name := learningStringArg(args, "name"); name != "" {
				for _, skill := range app.Skills.List() {
					if skill != nil && strings.EqualFold(strings.TrimSpace(skill.Name), strings.TrimSpace(name)) {
						ids = appendLearningID(ids, skill.ID)
					}
				}
			}
		}
		if len(ids) == 0 && app != nil && app.Skills != nil {
			// This fallback keeps usage recording compatible with output
			// produced before skill list/search started returning IDs.
			for _, skillName := range extractLearningOutputNames(output) {
				for _, skill := range app.Skills.List() {
					if skill != nil && strings.EqualFold(strings.TrimSpace(skill.Name), skillName) {
						ids = appendLearningID(ids, skill.ID)
					}
				}
			}
		}
	}
	return uniqueLearningIDs(ids)
}

// recordLearningUsage creates one undirected used_with edge for every pair of
// nodes observed in the same agent turn. Endpoints are sorted so reverse tool
// order cannot create a duplicate semantic edge.
func (a *App) recordLearningUsage(ids []string) {
	if a == nil {
		return
	}
	ids = uniqueLearningIDs(ids)
	if len(ids) < 2 {
		return
	}
	graph := a.graph()
	if graph == nil {
		return
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			_, _ = graph.AddEdge(ids[i], ids[j], domain.EdgeUsedWith, learningUsedWithWeight)
		}
	}
}

// recordLearningTurnNodes adds newly observed nodes to the run-local set and
// emits only pairs involving at least one new node. This prevents repeated
// tool rounds from repeatedly writing the same relationship while still
// connecting nodes discovered in different rounds of one turn.
func (a *App) recordLearningTurnNodes(run *TurnRun, ids []string) {
	if a == nil || run == nil {
		return
	}
	ids = uniqueLearningIDs(ids)
	if len(ids) == 0 {
		return
	}
	run.learningNodesMu.Lock()
	if run.learningNodes == nil {
		run.learningNodes = make(map[string]struct{})
	}
	newIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := run.learningNodes[id]; ok {
			continue
		}
		run.learningNodes[id] = struct{}{}
		newIDs = append(newIDs, id)
	}
	allIDs := make([]string, 0, len(run.learningNodes))
	for id := range run.learningNodes {
		allIDs = append(allIDs, id)
	}
	run.learningNodesMu.Unlock()
	if len(newIDs) == 0 {
		return
	}
	newSet := make(map[string]struct{}, len(newIDs))
	for _, id := range newIDs {
		newSet[id] = struct{}{}
	}
	sort.Strings(allIDs)
	graph := a.graph()
	if graph == nil {
		return
	}
	for i := 0; i < len(allIDs); i++ {
		for j := i + 1; j < len(allIDs); j++ {
			if _, leftNew := newSet[allIDs[i]]; !leftNew {
				if _, rightNew := newSet[allIDs[j]]; !rightNew {
					continue
				}
			}
			_, _ = graph.AddEdge(allIDs[i], allIDs[j], domain.EdgeUsedWith, learningUsedWithWeight)
		}
	}
}

func learningToolArgs(raw string) map[string]json.RawMessage {
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &args) != nil {
		return nil
	}
	return args
}

func learningStringArg(args map[string]json.RawMessage, key string) string {
	if raw, ok := args[key]; ok {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func extractLearningOutputIDs(output string) []string {
	ids := make([]string, 0, 4)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			for _, key := range []string{"id", "fragment_id"} {
				prefix := key + ":"
				if !strings.HasPrefix(line, prefix) {
					continue
				}
				value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), "\"'")
				ids = appendLearningID(ids, value)
			}
			continue
		}
		var item map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &item) != nil {
			continue
		}
		for _, key := range []string{"id", "fragment_id"} {
			var value string
			if json.Unmarshal(item[key], &value) == nil {
				ids = appendLearningID(ids, value)
			}
		}
	}
	return uniqueLearningIDs(ids)
}

func extractLearningOutputNames(output string) []string {
	var names []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var item struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(line), &item) == nil && strings.TrimSpace(item.Name) != "" {
			names = append(names, strings.TrimSpace(item.Name))
		}
	}
	return names
}

func appendLearningID(ids []string, id string) []string {
	id = strings.TrimSpace(id)
	if id == "" || len(ids) >= maxLearningIDsPerTool {
		return ids
	}
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func uniqueLearningIDs(ids []string) []string {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
		if len(unique) >= maxLearningIDsPerTool {
			break
		}
	}
	sort.Strings(unique)
	return unique
}

func learningNodeIDsFromFilePath(app *App, path string) []string {
	if app == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	path = filepath.Clean(path)
	ids := make([]string, 0, 2)
	if app.Primary != nil && app.Primary.Path() != "" && filepath.Clean(app.Primary.Path()) == path {
		if primary := app.Primary.Load(); primary != nil {
			for _, entry := range primary.Entries {
				ids = appendLearningID(ids, entry.ID)
			}
		}
	}
	if app.Skills == nil {
		return uniqueLearningIDs(ids)
	}
	for _, skill := range app.Skills.List() {
		if skill == nil || skill.ID == "" {
			continue
		}
		roots := []string{}
		if skill.PluginDir != "" {
			roots = append(roots, filepath.Join(skill.PluginDir, skill.Name), filepath.Join(skill.PluginDir, skill.ID))
		} else if app.DataDir != "" {
			roots = append(roots, filepath.Join(app.DataDir, "skills", skill.Name), filepath.Join(app.DataDir, "skills", skill.ID))
		}
		for _, root := range roots {
			rel, err := filepath.Rel(filepath.Clean(root), path)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				ids = appendLearningID(ids, skill.ID)
				break
			}
		}
	}
	return uniqueLearningIDs(ids)
}
