package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"nusashell/application"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// MemoryStore is an in-process implementation of the automation ports.
type MemoryStore struct {
	mu         sync.Mutex
	Workflows  map[string]*domain.WorkflowDefinition
	Runs       map[string]*domain.WorkflowRun
	Schedules  map[string]*domain.ScheduleRecord
	Events     []*domain.Event
	Deliveries map[string]string // key -> runID
	Waits      map[string]*domain.WaitRecord
	Logs       []domain.LogChunk
	Locks      map[string]string
	Debounce   map[string]time.Time
	Disabled   map[string]bool
	seq        map[string]uint64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		Workflows:  map[string]*domain.WorkflowDefinition{},
		Runs:       map[string]*domain.WorkflowRun{},
		Schedules:  map[string]*domain.ScheduleRecord{},
		Deliveries: map[string]string{},
		Waits:      map[string]*domain.WaitRecord{},
		Locks:      map[string]string{},
		Debounce:   map[string]time.Time{},
		Disabled:   map[string]bool{},
		seq:        map[string]uint64{},
	}
}

func (s *MemoryStore) Put(_ context.Context, w *domain.WorkflowDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *w
	s.Workflows[w.ID] = &cp
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*domain.WorkflowDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.Workflows[id]
	if !ok {
		return nil, fmt.Errorf("workflow %s not found", id)
	}
	cp := *w
	return &cp, nil
}

func (s *MemoryStore) List(_ context.Context) ([]*domain.WorkflowDefinition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.WorkflowDefinition
	for _, w := range s.Workflows {
		cp := *w
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Workflows, id)
	return nil
}

func (s *MemoryStore) Create(_ context.Context, run *domain.WorkflowRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := cloneRun(run)
	s.Runs[run.ID] = cp
	return nil
}

func (s *MemoryStore) GetRun(_ context.Context, id string) (*domain.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.Runs[id]
	if !ok {
		return nil, fmt.Errorf("run %s not found", id)
	}
	return cloneRun(r), nil
}

func (s *MemoryStore) ListRuns(_ context.Context, filter application.RunFilter) ([]*domain.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.WorkflowRun
	for _, r := range s.Runs {
		if filter.WorkflowID != "" && r.WorkflowID != filter.WorkflowID {
			continue
		}
		if filter.Workspace != "" && r.Workspace != filter.Workspace {
			continue
		}
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		out = append(out, cloneRun(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) Update(_ context.Context, run *domain.WorkflowRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Runs[run.ID] = cloneRun(run)
	return nil
}

func (s *MemoryStore) PutSchedule(_ context.Context, rec *domain.ScheduleRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.Schedules[rec.ID] = &cp
	return nil
}

func (s *MemoryStore) DueSchedules(_ context.Context, now time.Time, limit int) ([]*domain.ScheduleRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.ScheduleRecord
	for _, rec := range s.Schedules {
		if rec.Status == domain.SchedulePending && !rec.NextRunAt.After(now) {
			cp := *rec
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(out[j].NextRunAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ClaimSchedule(_ context.Context, id string, now time.Time) (*domain.ScheduleRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.Schedules[id]
	if !ok || rec.Status != domain.SchedulePending {
		return nil, nil
	}
	rec.Status = domain.ScheduleFired
	t := now
	rec.FiredAt = &t
	cp := *rec
	return &cp, nil
}

func (s *MemoryStore) ListSchedules(_ context.Context) ([]*domain.ScheduleRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.ScheduleRecord
	for _, rec := range s.Schedules {
		cp := *rec
		out = append(out, &cp)
	}
	return out, nil
}

func (s *MemoryStore) PutEvent(_ context.Context, ev *domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *ev
	s.Events = append(s.Events, &cp)
	return nil
}

func (s *MemoryStore) RecordDelivery(_ context.Context, eventID, triggerID, workflowID, runID string, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := domain.DeliveryKey(eventID, triggerID, workflowID)
	if _, ok := s.Deliveries[key]; ok {
		return false, nil
	}
	s.Deliveries[key] = runID
	return true, nil
}

func (s *MemoryStore) ListEvents(_ context.Context, limit int) ([]*domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]*domain.Event{}, s.Events...)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *MemoryStore) PutWait(_ context.Context, rec *domain.WaitRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.Waits[rec.ID] = &cp
	return nil
}

func (s *MemoryStore) DueWaits(_ context.Context, now time.Time, limit int) ([]*domain.WaitRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.WaitRecord
	for _, rec := range s.Waits {
		if rec.Status != domain.SchedulePending {
			continue
		}
		if rec.WakeAt != nil && !rec.WakeAt.After(now) {
			cp := *rec
			out = append(out, &cp)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ClaimWait(_ context.Context, id string) (*domain.WaitRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.Waits[id]
	if !ok || rec.Status != domain.SchedulePending {
		return nil, nil
	}
	rec.Status = domain.ScheduleFired
	cp := *rec
	return &cp, nil
}

func (s *MemoryStore) WaitingForEvent(_ context.Context, eventType string) ([]*domain.WaitRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.WaitRecord
	for _, rec := range s.Waits {
		if rec.Status == domain.SchedulePending && rec.EventType == eventType {
			cp := *rec
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *MemoryStore) Append(_ context.Context, chunk domain.LogChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq[chunk.JobID]++
	chunk.Sequence = s.seq[chunk.JobID]
	s.Logs = append(s.Logs, chunk)
	return nil
}

func (s *MemoryStore) Read(_ context.Context, jobID string, after uint64, limit int) ([]domain.LogChunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.LogChunk
	for _, c := range s.Logs {
		if c.JobID == jobID && c.Sequence > after {
			out = append(out, c)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *MemoryStore) Active(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.Locks[key]
	return id, ok, nil
}

func (s *MemoryStore) Acquire(_ context.Context, key, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Locks[key] = runID
	return nil
}

func (s *MemoryStore) Release(_ context.Context, key, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Locks[key] == runID {
		delete(s.Locks, key)
	}
	return nil
}

func (s *MemoryStore) Last(_ context.Context, workflowID, triggerID string) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.Debounce[workflowID+"/"+triggerID]
	return t, ok, nil
}

func (s *MemoryStore) Touch(_ context.Context, workflowID, triggerID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Debounce[workflowID+"/"+triggerID] = at
	return nil
}

func (s *MemoryStore) GetDisabled(_ context.Context, providerID string) (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.Disabled[providerID]
	return d, ok, nil
}

func (s *MemoryStore) SetDisabled(_ context.Context, providerID string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Disabled[providerID] = disabled
	return nil
}

func cloneRun(r *domain.WorkflowRun) *domain.WorkflowRun {
	b, _ := json.Marshal(r)
	var out domain.WorkflowRun
	_ = json.Unmarshal(b, &out)
	return &out
}

// FSArtifactStore writes tar-less files under data/ci/artifacts.
type FSArtifactStore struct {
	Root string
}

func (s FSArtifactStore) Put(_ context.Context, req application.ArtifactPutRequest) (domain.Artifact, error) {
	id := domain.NewID(domain.IDPrefixArt)
	dir := filepath.Join(s.Root, req.RunID, req.JobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return domain.Artifact{}, err
	}
	path := filepath.Join(dir, id+".bin")
	f, err := os.Create(path)
	if err != nil {
		return domain.Artifact{}, err
	}
	defer f.Close()
	n, err := io.Copy(f, req.Body)
	if err != nil {
		return domain.Artifact{}, err
	}
	return domain.Artifact{
		ID: id, RunID: req.RunID, JobID: req.JobID, Name: req.Name,
		Paths: req.Paths, Size: n, CreatedAt: clock.NewTime().Time(), ExpiresAt: req.Expires, Path: path,
	}, nil
}

func (s FSArtifactStore) List(_ context.Context, runID string) ([]domain.Artifact, error) {
	return nil, nil
}

func (s FSArtifactStore) Open(_ context.Context, artifactID string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not found")
}

// Adapters expose MemoryStore as the application ports whose method
// names collide (Get/List/Put/Due/Claim).

type WorkflowMem struct{ *MemoryStore }
type RunMem struct{ *MemoryStore }
type ScheduleMem struct{ *MemoryStore }
type EventMem struct{ *MemoryStore }
type WaitMem struct{ *MemoryStore }
type LogMem struct{ *MemoryStore }
type LockMem struct{ *MemoryStore }
type DebounceMem struct{ *MemoryStore }
type ProviderStateMem struct{ *MemoryStore }

func (a RunMem) Get(ctx context.Context, id string) (*domain.WorkflowRun, error) {
	return a.GetRun(ctx, id)
}
func (a RunMem) List(ctx context.Context, filter application.RunFilter) ([]*domain.WorkflowRun, error) {
	return a.ListRuns(ctx, filter)
}

func (a ScheduleMem) Put(ctx context.Context, rec *domain.ScheduleRecord) error {
	return a.PutSchedule(ctx, rec)
}
func (a ScheduleMem) Due(ctx context.Context, now time.Time, limit int) ([]*domain.ScheduleRecord, error) {
	return a.DueSchedules(ctx, now, limit)
}
func (a ScheduleMem) Claim(ctx context.Context, id string, now time.Time) (*domain.ScheduleRecord, error) {
	return a.ClaimSchedule(ctx, id, now)
}
func (a ScheduleMem) List(ctx context.Context) ([]*domain.ScheduleRecord, error) {
	return a.ListSchedules(ctx)
}

func (a WaitMem) Put(ctx context.Context, rec *domain.WaitRecord) error {
	return a.PutWait(ctx, rec)
}
func (a WaitMem) Due(ctx context.Context, now time.Time, limit int) ([]*domain.WaitRecord, error) {
	return a.DueWaits(ctx, now, limit)
}
func (a WaitMem) Claim(ctx context.Context, id string) (*domain.WaitRecord, error) {
	return a.ClaimWait(ctx, id)
}

func (a ProviderStateMem) Get(ctx context.Context, providerID string) (bool, bool, error) {
	return a.GetDisabled(ctx, providerID)
}
