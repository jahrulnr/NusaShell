package learn

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
func LearningNodeIDsFromTool(s *Service, toolCall domain.ToolCall, output string) []string {
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
		return learningNodeIDsFromFilePath(s, learningStringArg(args, "path"))
	}
	if root != "memory" && root != "skill" {
		return nil
	}

	ids := extractLearningOutputIDs(output)
	switch root {
	case "skill":
		// A save of an existing skill can be identified from args even when
		// an older toolbox output did not include the saved ID. For a new
		// skill, resolve its name after the save completed.
		if op == "save" && s != nil && s.deps.Skills != nil {
			if id := learningStringArg(args, "id"); id != "" {
				ids = appendLearningID(ids, id)
			} else if name := learningStringArg(args, "name"); name != "" {
				for _, skill := range s.deps.Skills.List() {
					if skill != nil && strings.EqualFold(strings.TrimSpace(skill.Name), strings.TrimSpace(name)) {
						ids = appendLearningID(ids, skill.ID)
					}
				}
			}
		}
		if len(ids) == 0 && s != nil && s.deps.Skills != nil {
			// This fallback keeps usage recording compatible with output
			// produced before skill list/search started returning IDs.
			for _, skillName := range extractLearningOutputNames(output) {
				for _, skill := range s.deps.Skills.List() {
					if skill != nil && strings.EqualFold(strings.TrimSpace(skill.Name), skillName) {
						ids = appendLearningID(ids, skill.ID)
					}
				}
			}
		}
	}
	return UniqueLearningIDs(ids)
}

// recordLearningUsage creates one undirected used_with edge for every pair of
// nodes observed in the same agent turn. Endpoints are sorted so reverse tool
// order cannot create a duplicate semantic edge.
func (s *Service) RecordUsage(ids []string) {
	if s == nil {
		return
	}
	ids = UniqueLearningIDs(ids)
	if len(ids) < 2 {
		return
	}
	graph := s.Graph()
	if graph == nil {
		return
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			_, _ = graph.AddEdge(ids[i], ids[j], domain.EdgeUsedWith, learningUsedWithWeight)
		}
	}
}

// RecordTurnPairs writes used_with edges for pairs that include at least one
// newly observed node. The caller owns the run-local seen set (TurnRun).
func (s *Service) RecordTurnPairs(allIDs, newIDs []string) {
	if s == nil || len(newIDs) == 0 || len(allIDs) < 2 {
		return
	}
	newSet := make(map[string]struct{}, len(newIDs))
	for _, id := range newIDs {
		newSet[id] = struct{}{}
	}
	sort.Strings(allIDs)
	graph := s.Graph()
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
	return UniqueLearningIDs(ids)
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

func UniqueLearningIDs(ids []string) []string {
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

func learningNodeIDsFromFilePath(s *Service, path string) []string {
	if s == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	path = filepath.Clean(path)
	ids := make([]string, 0, 2)
	if s.deps.User != nil && s.deps.User.Path() != "" && filepath.Clean(s.deps.User.Path()) == path {
		if user := s.deps.User.Load(); user != nil {
			for _, entry := range user.Entries {
				ids = appendLearningID(ids, entry.ID)
			}
		}
	}
	if s.deps.Skills == nil {
		return UniqueLearningIDs(ids)
	}
	for _, skill := range s.deps.Skills.List() {
		if skill == nil || skill.ID == "" {
			continue
		}
		roots := []string{}
		if skill.PluginDir != "" {
			roots = append(roots, filepath.Join(skill.PluginDir, skill.Name), filepath.Join(skill.PluginDir, skill.ID))
		} else if s.deps.DataDir != "" {
			roots = append(roots, filepath.Join(s.deps.DataDir, "skills", skill.Name), filepath.Join(s.deps.DataDir, "skills", skill.ID))
		}
		for _, root := range roots {
			rel, err := filepath.Rel(filepath.Clean(root), path)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				ids = appendLearningID(ids, skill.ID)
				break
			}
		}
	}
	return UniqueLearningIDs(ids)
}
