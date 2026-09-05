package jsonstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

const (
	growthExperiencesFile = "growth/experiences.jsonl"
	growthMemoriesFile    = "growth/memories.jsonl"
	growthJobsFile        = "growth/jobs.jsonl"
	growthOpsFile         = "growth/operations.jsonl"
)

func (s *Store) loadGrowth() error {
	if err := os.MkdirAll(filepath.Join(s.dir, "growth"), 0o755); err != nil {
		return err
	}
	if err := loadJSONL(filepath.Join(s.dir, growthExperiencesFile), &s.experiences); err != nil {
		return err
	}
	if err := loadJSONL(filepath.Join(s.dir, growthMemoriesFile), &s.memoryRecords); err != nil {
		return err
	}
	if err := loadJSONL(filepath.Join(s.dir, growthJobsFile), &s.learningJobs); err != nil {
		return err
	}
	return loadJSONL(filepath.Join(s.dir, growthOpsFile), &s.learningOps)
}

func loadJSONL[T any](path string, dst *[]*T) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		*dst = append(*dst, &v)
	}
	return nil
}

func upsertJSONL[T any](s *Store, items *[]*T, v *T, idOf func(*T) string, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(v)
	cur := *items
	for i, existing := range cur {
		if idOf(existing) == idOf(v) {
			cur[i] = stored
			*items = cur
			return s.writeJSONL(path, cur)
		}
	}
	cur = append(cur, stored)
	*items = cur
	return s.writeJSONL(path, cur)
}

// Experiences adapter.

type Experiences struct{ S *Store }

func (e *Experiences) List() []*domain.Experience {
	return listSlice(e.S, e.S.experiences)
}

func (e *Experiences) Get(id string) (*domain.Experience, error) {
	return getSlice(e.S, e.S.experiences, id, func(x *domain.Experience) string { return x.ID }, "experience")
}

func (e *Experiences) Save(v *domain.Experience) error {
	if v == nil || strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("experience id required")
	}
	return upsertJSONL(e.S, &e.S.experiences, v, func(x *domain.Experience) string { return x.ID }, growthExperiencesFile)
}

func (e *Experiences) ListByConversation(conversationID string) []*domain.Experience {
	out := []*domain.Experience{}
	for _, x := range e.List() {
		if x.ConversationID == conversationID {
			out = append(out, x)
		}
	}
	return out
}

// MemoryRecords adapter.

type MemoryRecords struct{ S *Store }

func (m *MemoryRecords) List() []*domain.MemoryRecord {
	return listSlice(m.S, m.S.memoryRecords)
}

func (m *MemoryRecords) Get(id string) (*domain.MemoryRecord, error) {
	return getSlice(m.S, m.S.memoryRecords, id, func(x *domain.MemoryRecord) string { return x.ID }, "memory")
}

func (m *MemoryRecords) Save(v *domain.MemoryRecord) error {
	if v == nil || strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("memory id required")
	}
	return upsertJSONL(m.S, &m.S.memoryRecords, v, func(x *domain.MemoryRecord) string { return x.ID }, growthMemoriesFile)
}

func (m *MemoryRecords) Delete(id string) error {
	next, err := removeSlice(m.S, m.S.memoryRecords, id, func(x *domain.MemoryRecord) string { return x.ID }, "memory", func(items []*domain.MemoryRecord) error {
		return m.S.writeJSONL(growthMemoriesFile, items)
	})
	if err != nil {
		return err
	}
	m.S.memoryRecords = next
	return nil
}

// LearningJobs adapter.

type LearningJobs struct{ S *Store }

func (j *LearningJobs) List() []*domain.LearningJob {
	return listSlice(j.S, j.S.learningJobs)
}

func (j *LearningJobs) Get(id string) (*domain.LearningJob, error) {
	return getSlice(j.S, j.S.learningJobs, id, func(x *domain.LearningJob) string { return x.ID }, "learning job")
}

func (j *LearningJobs) Save(v *domain.LearningJob) error {
	if v == nil || strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("job id required")
	}
	return upsertJSONL(j.S, &j.S.learningJobs, v, func(x *domain.LearningJob) string { return x.ID }, growthJobsFile)
}

// LearningOps adapter.

type LearningOps struct{ S *Store }

func (o *LearningOps) List() []*domain.LearningOperation {
	return listSlice(o.S, o.S.learningOps)
}

func (o *LearningOps) Save(v *domain.LearningOperation) error {
	if v == nil || strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("operation id required")
	}
	return upsertJSONL(o.S, &o.S.learningOps, v, func(x *domain.LearningOperation) string { return x.ID }, growthOpsFile)
}
