package application

import (
	"fmt"
	"sync"

	"nusashell/domain"
)

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
