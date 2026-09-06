package application

import (
	"fmt"
	"io"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"sync"
)

// --- from growth_fakes_test.go ---

type fakeMemoryRecordStore struct {
	items []*domain.MemoryRecord
}

func (f *fakeMemoryRecordStore) List() []*domain.MemoryRecord {
	if f == nil {
		return nil
	}
	return f.items
}

func (f *fakeMemoryRecordStore) Get(id string) (*domain.MemoryRecord, error) {
	for _, m := range f.items {
		if m != nil && m.ID == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("memory %s not found", id)
}

func (f *fakeMemoryRecordStore) Save(e *domain.MemoryRecord) error {
	if e == nil {
		return fmt.Errorf("nil memory")
	}
	for i, existing := range f.items {
		if existing.ID == e.ID {
			f.items[i] = e
			return nil
		}
	}
	f.items = append(f.items, e)
	return nil
}

func (f *fakeMemoryRecordStore) Delete(id string) error {
	for i, e := range f.items {
		if e.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("memory %s not found", id)
}

type fakeSettings struct {
	domain.Settings
}

func (f *fakeSettings) Get() domain.Settings        { return f.Settings }
func (f *fakeSettings) Set(s domain.Settings) error { f.Settings = s; return nil }

type cloningConvStore struct {
	mu              sync.Mutex
	conv            *domain.Conversation
	getCount        int
	injectAfterGet  bool
	injectedMessage *domain.Message
}

func (s *cloningConvStore) List() []*domain.Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []*domain.Conversation{s.conv}
}
func (s *cloningConvStore) Get(id string) (*domain.Conversation, error) {
	s.mu.Lock()
	s.getCount++
	c := *s.conv
	c.Messages = append([]domain.Message(nil), s.conv.Messages...)
	if s.injectAfterGet && s.getCount == 1 && s.injectedMessage != nil {
		s.conv.Messages = append(s.conv.Messages, *s.injectedMessage)
	}
	s.mu.Unlock()
	return &c, nil
}
func (s *cloningConvStore) Save(c *domain.Conversation) error {
	s.mu.Lock()
	saved := *c
	saved.Messages = append([]domain.Message(nil), c.Messages...)
	s.conv = &saved
	s.mu.Unlock()
	return nil
}
func (s *cloningConvStore) Delete(id string) error { return nil }
func (s *cloningConvStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (s *cloningConvStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, errNotFound
}

type fakeExperienceStore struct {
	items []*domain.Experience
}

func (f *fakeExperienceStore) List() []*domain.Experience { return f.items }
func (f *fakeExperienceStore) Get(id string) (*domain.Experience, error) {
	for _, e := range f.items {
		if e != nil && e.ID == id {
			return e, nil
		}
	}
	return nil, fmt.Errorf("experience %s not found", id)
}
func (f *fakeExperienceStore) Delete(id string) error {
	for i, e := range f.items {
		if e != nil && e.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("experience %s not found", id)
}
func (f *fakeExperienceStore) Save(e *domain.Experience) error {
	if e == nil {
		return fmt.Errorf("nil experience")
	}
	for i, existing := range f.items {
		if existing.ID == e.ID {
			f.items[i] = e
			return nil
		}
	}
	f.items = append(f.items, e)
	return nil
}
func (f *fakeExperienceStore) ListByConversation(conversationID string) []*domain.Experience {
	var out []*domain.Experience
	for _, e := range f.items {
		if e != nil && e.ConversationID == conversationID {
			out = append(out, e)
		}
	}
	return out
}

// --- from learned_params_fakes_test.go ---

// fakeLearnedParamStore is an in-memory LearnedParamStore for package tests.
type fakeLearnedParamStore struct {
	registry *domain.LearnedParamRegistry
	saves    int
}

func (f *fakeLearnedParamStore) Load() *domain.LearnedParamRegistry {
	if f.registry == nil {
		return domain.NewLearnedParamRegistry()
	}
	return f.registry
}

func (f *fakeLearnedParamStore) Save(r *domain.LearnedParamRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

// --- from stream_stub_test.go ---

// stubStream is a core.Stream that yields events from a pre-built response
// then ends with a DoneEvent.
type stubStream struct {
	events []core.Event
	idx    int
}

func (s *stubStream) Next() (core.Event, error) {
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func (s *stubStream) Close() error { return nil }

type eventsThenErrorStream struct {
	events   []core.Event
	err      error
	idx      int
	failOnce bool
}

func (s *eventsThenErrorStream) Next() (core.Event, error) {
	if s.idx < len(s.events) {
		event := s.events[s.idx]
		s.idx++
		return event, nil
	}
	if !s.failOnce {
		s.failOnce = true
		return nil, s.err
	}
	return nil, io.EOF
}

func (s *eventsThenErrorStream) Close() error { return nil }

// stubProviderContext wraps a core.Provider in a ProviderContext for tests.
func stubProviderContext(p core.Provider) ProviderContext {
	return ProviderContext{Provider: p, Kind: domain.ProviderChat}
}

func chatResponseToCore(r ChatResponse) *core.Response {
	resp := &core.Response{FinishReason: core.FinishReasonStop}
	if r.Content != "" {
		resp.Blocks = append(resp.Blocks, core.TextBlock{Text: r.Content})
	}
	if r.Reasoning != "" {
		resp.Blocks = append(resp.Blocks, core.ReasoningBlock{Text: r.Reasoning})
	}
	for _, tc := range r.ToolCalls {
		resp.Blocks = append(resp.Blocks, core.ToolUseBlock{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: jsonRaw(tc.Args),
		})
		resp.FinishReason = core.FinishReasonToolCall
	}
	return resp
}

func coreResponseEvents(resp *core.Response) []core.Event {
	var events []core.Event
	for _, b := range resp.Blocks {
		switch v := b.(type) {
		case core.TextBlock:
			events = append(events, core.ContentDelta{Text: v.Text})
		case core.ReasoningBlock:
			events = append(events, core.ReasoningDelta{Text: v.Text})
		case core.ToolUseBlock:
			idx := 0
			id := v.ID
			if id == "" {
				id = fmt.Sprintf("tool_%d", len(events))
			}
			events = append(events, core.ToolUseStart{ID: id, Name: v.Name, Index: &idx})
			events = append(events, core.ToolUseDelta{ID: id, Index: &idx, ArgumentsDelta: v.Arguments})
			events = append(events, core.ToolUseDone{ID: id, Index: &idx})
		}
	}
	events = append(events, core.DoneEvent{FinishReason: resp.FinishReason, Provider: "test-stub", Model: "test-model"})
	return events
}
