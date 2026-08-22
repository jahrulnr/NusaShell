package application

import (
	"testing"

	"nusashell/domain"
)

// fakeSettingsStoreWithThreshold is a settings store that returns a
// configurable LearningReviewThreshold and ReviewModel.
type fakeSettingsStoreWithThreshold struct {
	threshold   int
	reviewModel string
}

func (f *fakeSettingsStoreWithThreshold) Get() domain.Settings {
	return domain.Settings{LearningReviewThreshold: f.threshold, ReviewModel: f.reviewModel}
}
func (f *fakeSettingsStoreWithThreshold) Set(s domain.Settings) error {
	f.threshold = s.LearningReviewThreshold
	f.reviewModel = s.ReviewModel
	return nil
}

func TestIncrementTurnCounterBelowThreshold(t *testing.T) {
	app := &App{
		turnsSinceReview:     map[string]int{},
		toolCallsSinceReview: map[string]int{},
		Settings:             &fakeSettingsStoreWithThreshold{threshold: 5},
		Memory:               &fakeMemoryStore{},
	}
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	for i := 0; i < 4; i++ {
		app.incrementTurnCounter("conv_1")
	}
	if app.turnsSinceReview["conv_1"] != 4 {
		t.Errorf("counter = %d, want 4", app.turnsSinceReview["conv_1"])
	}
}

func TestIncrementTurnCounterAtThresholdFlushes(t *testing.T) {
	app := &App{
		turnsSinceReview:     map[string]int{},
		toolCallsSinceReview: map[string]int{},
		Settings:             &fakeSettingsStoreWithThreshold{threshold: 3},
		Memory:               &fakeMemoryStore{},
		Conversations:        &fakeConversationStore{},
	}
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	for i := 0; i < 3; i++ {
		app.incrementTurnCounter("conv_1")
	}
	if app.turnsSinceReview["conv_1"] != 0 {
		t.Errorf("counter after flush = %d, want 0", app.turnsSinceReview["conv_1"])
	}
}

func TestIncrementTurnCounterDisabled(t *testing.T) {
	app := &App{
		turnsSinceReview:     map[string]int{},
		toolCallsSinceReview: map[string]int{},
		Settings:             &fakeSettingsStoreWithThreshold{threshold: 0},
		Memory:               &fakeMemoryStore{},
	}
	app.ReviewAgent = NewBackgroundReviewAgent(app, DefaultReviewSettings())
	for i := 0; i < 100; i++ {
		app.incrementTurnCounter("conv_1")
	}
	if app.turnsSinceReview["conv_1"] != 0 {
		t.Errorf("counter when disabled = %d, want 0", app.turnsSinceReview["conv_1"])
	}
}

func TestIncrementTurnCounterNoReviewer(t *testing.T) {
	app := &App{
		turnsSinceReview:     map[string]int{},
		toolCallsSinceReview: map[string]int{},
		Settings:             &fakeSettingsStoreWithThreshold{threshold: 1},
	}
	app.incrementTurnCounter("conv_1")
	if app.turnsSinceReview["conv_1"] != 0 {
		t.Errorf("counter with no reviewer = %d, want 0", app.turnsSinceReview["conv_1"])
	}
}

// fakeConversationStore is a minimal store for flush tests.
type fakeConversationStore struct {
	conv *domain.Conversation
}

func (f *fakeConversationStore) Get(id string) (*domain.Conversation, error) {
	if f.conv != nil && f.conv.ID == id {
		return f.conv, nil
	}
	return &domain.Conversation{ID: id, Messages: []domain.Message{
		{Role: domain.RoleUser, Content: "remember that the api key is stored in env"},
		{Role: domain.RoleAssistant, Content: "Error: connection refused\nFix: check if server is running"},
	}}, nil
}
func (f *fakeConversationStore) Save(c *domain.Conversation) error { f.conv = c; return nil }
func (f *fakeConversationStore) List() []*domain.Conversation      { return nil }
func (f *fakeConversationStore) Delete(id string) error            { return nil }
func (f *fakeConversationStore) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	return 0, nil
}
func (f *fakeConversationStore) GetChunk(id string, index int) ([]domain.Message, error) {
	return nil, nil
}
