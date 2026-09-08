package conversation_test

import (
	"strings"
	"testing"
	"time"

	"nusashell/application/conversation"
	"nusashell/domain"
)

type inspectStore struct {
	convs  map[string]*domain.Conversation
	chunks map[string]map[int][]domain.Message
}

func (s *inspectStore) List() []*domain.Conversation {
	out := make([]*domain.Conversation, 0, len(s.convs))
	for _, c := range s.convs {
		out = append(out, c)
	}
	return out
}
func (s *inspectStore) Get(id string) (*domain.Conversation, error) {
	c, ok := s.convs[id]
	if !ok {
		return nil, errNotFound
	}
	return c, nil
}
func (s *inspectStore) Save(c *domain.Conversation) error {
	if s.convs == nil {
		s.convs = map[string]*domain.Conversation{}
	}
	s.convs[c.ID] = c
	return nil
}
func (s *inspectStore) Delete(id string) error { delete(s.convs, id); return nil }
func (s *inspectStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (s *inspectStore) GetChunk(id string, index int) ([]domain.Message, error) {
	if s.chunks == nil || s.chunks[id] == nil {
		return nil, errNotFound
	}
	msgs, ok := s.chunks[id][index]
	if !ok {
		return nil, errNotFound
	}
	return msgs, nil
}

var errNotFound = errString("not found")

type errString string

func (e errString) Error() string { return string(e) }

func newInspectService(store conversation.Store) *conversation.Service {
	return conversation.New(conversation.Deps{Store: store})
}

func TestRoomInfoAndReadTurns(t *testing.T) {
	now := time.Now()
	c := &domain.Conversation{
		ID:         "conv_1",
		Title:      "Auth work",
		Status:     "idle",
		Summary:    "Compacted handoff about middleware",
		Workspace:  "/proj",
		Model:      "prov:model",
		ChunkCount: 1,
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now,
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "fix auth", CreatedAt: now},
			{ID: "a1", Role: domain.RoleAssistant, Content: "checking", ToolCalls: []domain.ToolCall{
				{ID: "t1", Name: "file_read", Status: domain.ToolOK, Output: strings.Repeat("x", 50)},
			}, CreatedAt: now},
			{ID: "u2", Role: domain.RoleUser, Content: "also add tests", CreatedAt: now},
			{ID: "a2", Role: domain.RoleAssistant, Content: "done", CreatedAt: now},
		},
	}
	store := &inspectStore{
		convs: map[string]*domain.Conversation{"conv_1": c},
		chunks: map[string]map[int][]domain.Message{
			"conv_1": {0: {
				{ID: "ou1", Role: domain.RoleUser, Content: "old topic"},
				{ID: "oa1", Role: domain.RoleAssistant, Content: "old answer"},
			}},
		},
	}
	svc := newInspectService(store)

	info, err := svc.RoomInfo("conv_1", nil)
	if err != nil {
		t.Fatalf("RoomInfo: %v", err)
	}
	if info.TurnCount != 2 || info.MessageCount != 4 || info.ChunkCount != 1 || !info.HasSummary {
		t.Fatalf("info = %+v", info)
	}

	chunk := 0
	cinfo, err := svc.RoomInfo("conv_1", &chunk)
	if err != nil {
		t.Fatalf("RoomInfo chunk: %v", err)
	}
	if cinfo.TurnCount != 1 || cinfo.MessageCount != 2 || cinfo.ChunkIndex == nil || *cinfo.ChunkIndex != 0 {
		t.Fatalf("chunk info = %+v", cinfo)
	}

	read, err := svc.RoomRead("conv_1", nil, intPtr(1), intPtr(1))
	if err != nil {
		t.Fatalf("RoomRead: %v", err)
	}
	if read.Start != 1 || read.End != 1 || len(read.Messages) != 2 {
		t.Fatalf("read = %+v", read)
	}
	if read.Messages[0].Content != "also add tests" || read.Messages[1].Content != "done" {
		t.Fatalf("messages = %+v", read.Messages)
	}

	onlyFirst, err := svc.RoomRead("conv_1", nil, intPtr(0), intPtr(0))
	if err != nil {
		t.Fatalf("turn 0 only: %v", err)
	}
	if onlyFirst.Start != 0 || onlyFirst.End != 0 || len(onlyFirst.Messages) != 2 {
		t.Fatalf("turn 0 only = %+v", onlyFirst)
	}

	def, err := svc.RoomRead("conv_1", nil, nil, nil)
	if err != nil {
		t.Fatalf("default window: %v", err)
	}
	if def.Start != 0 || def.End != 1 {
		t.Fatalf("default window start/end = %d/%d, want 0/1", def.Start, def.End)
	}
}

func intPtr(v int) *int { return &v }

func TestPartitionTurnsKeepsSteerInsideParentTurn(t *testing.T) {
	msgs := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "do the thing"},
		{ID: "a1", Role: domain.RoleAssistant, Content: "working"},
		{ID: "s1", Role: domain.RoleUser, Content: "also check tests", Steer: true},
		{ID: "a2", Role: domain.RoleAssistant, Content: "tests ok"},
		{ID: "u2", Role: domain.RoleUser, Content: "next topic"},
		{ID: "a3", Role: domain.RoleAssistant, Content: "next"},
	}
	// Exercise through RoomRead so the package-level behavior stays covered
	// without exporting partitionTurns.
	store := &inspectStore{convs: map[string]*domain.Conversation{
		"conv_1": {ID: "conv_1", Title: "T", Messages: msgs},
	}}
	svc := newInspectService(store)
	info, err := svc.RoomInfo("conv_1", nil)
	if err != nil {
		t.Fatalf("RoomInfo: %v", err)
	}
	if info.TurnCount != 2 {
		t.Fatalf("turn_count = %d, want 2 (steer must not open a turn)", info.TurnCount)
	}
	read, err := svc.RoomRead("conv_1", nil, intPtr(0), intPtr(0))
	if err != nil {
		t.Fatalf("RoomRead: %v", err)
	}
	if len(read.Messages) != 4 {
		t.Fatalf("turn 0 messages = %d, want 4 (user+asst+steer+asst)", len(read.Messages))
	}
	if read.Messages[2].ID != "s1" || read.Messages[2].Turn != 0 {
		t.Fatalf("steer message = %+v, want id=s1 turn=0", read.Messages[2])
	}
}

func TestSearchRoomsMatchesMessageAndID(t *testing.T) {
	now := time.Now()
	store := &inspectStore{convs: map[string]*domain.Conversation{
		"conv_abc": {
			ID: "conv_abc", Title: "Other", UpdatedAt: now,
			Messages: []domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "phantom patch rollback"}},
		},
		"conv_xyz": {
			ID: "conv_xyz", Title: "Backend", Summary: "auth", UpdatedAt: now.Add(-time.Minute),
		},
	}}
	svc := newInspectService(store)

	total, items, err := svc.SearchRooms("", "phantom", 10, 0)
	if err != nil || total != 1 || items[0].ID != "conv_abc" || items[0].Match != "message" {
		t.Fatalf("message search = total=%d items=%+v err=%v", total, items, err)
	}
	total, items, err = svc.SearchRooms("", "conv_xyz", 10, 0)
	if err != nil || total != 1 || items[0].Match != "id" {
		t.Fatalf("id search = total=%d items=%+v err=%v", total, items, err)
	}
}

func TestSearchMessagesScoped(t *testing.T) {
	now := time.Now()
	store := &inspectStore{convs: map[string]*domain.Conversation{
		"conv_1": {
			ID: "conv_1", Title: "T", UpdatedAt: now,
			Messages: []domain.Message{
				{ID: "u1", Role: domain.RoleUser, Content: "hello world"},
				{ID: "a1", Role: domain.RoleAssistant, Content: "hi there"},
				{ID: "u2", Role: domain.RoleUser, Content: "world peace"},
			},
		},
	}}
	svc := newInspectService(store)
	total, hits, err := svc.SearchMessages("conv_1", "world", 10, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if total != 2 || len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].Turn != 0 || hits[1].Turn != 1 {
		t.Fatalf("turn indexes = %d,%d", hits[0].Turn, hits[1].Turn)
	}
}
