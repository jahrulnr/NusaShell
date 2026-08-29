package domain

import "testing"

func TestDecideAutoContinue(t *testing.T) {
	openItems := []TodoItem{
		{ID: "1", Content: "a", Status: TodoInProgress},
		{ID: "2", Content: "b", Status: TodoPending},
	}
	doneItems := []TodoItem{
		{ID: "1", Content: "a", Status: TodoCompleted},
	}

	cases := []struct {
		name  string
		input AutoContinueInput
		want  AutoContinueDecision
	}{
		{
			name:  "continue: open todos, turn ok, budget left",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: 0, MaxAutoContinues: 10, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: true, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueContinue},
		},
		{
			name:  "no-conversation: no conversation bound",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: false},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueNoConversation},
		},
		{
			name:  "turn-not-ok: turn failed",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: false, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueTurnNotOK},
		},
		{
			name:  "plain-text question does not pause: ask_question is the only user gate",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: true, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueContinue},
		},
		{
			name:  "no-open-todos: all completed",
			input: AutoContinueInput{Items: doneItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 0, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueNoOpenTodos},
		},
		{
			name:  "no-open-todos: empty list",
			input: AutoContinueInput{Items: nil, MaxAutoContinues: 10, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 0, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueNoOpenTodos},
		},
		{
			name:  "awaiting-background-jobs: jobs running",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, HasBackgroundJobs: true},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueAwaitingBackground},
		},
		{
			name:  "max-reached: budget exhausted",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: 10, MaxAutoContinues: 10, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 10, MaxAutoContinues: 10, Reason: AutoContinueMaxReached},
		},
		{
			name:  "unlimited: 0 budget skips check",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: 999, MaxAutoContinues: 0, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: true, OpenTodoCount: 2, ContinuesUsed: 999, MaxAutoContinues: 0, Reason: AutoContinueContinue},
		},
		{
			name:  "negative index clamped to 0",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: -5, MaxAutoContinues: 10, TurnOK: true, HasConversation: true},
			want:  AutoContinueDecision{ShouldContinue: true, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueContinue},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DecideAutoContinue(c.input)
			if got != c.want {
				t.Fatalf("DecideAutoContinue = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestNormalizeMaxAutoContinues(t *testing.T) {
	cases := []struct {
		input int
		want  int
	}{
		{-1, DefaultMaxAutoContinues},
		{-100, DefaultMaxAutoContinues},
		{0, 0}, // unlimited sentinel
		{1, 1},
		{10, 10},
		{MaxAutoContinuesCap, MaxAutoContinuesCap},
		{MaxAutoContinuesCap + 1, MaxAutoContinuesCap},
		{999999, MaxAutoContinuesCap},
	}
	for _, c := range cases {
		if got := NormalizeMaxAutoContinues(c.input); got != c.want {
			t.Errorf("NormalizeMaxAutoContinues(%d) = %d, want %d", c.input, got, c.want)
		}
	}
}
