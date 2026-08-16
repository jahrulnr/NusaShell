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
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: 0, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "done with step 1"},
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
			name:  "awaiting-user: ends with question mark",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "which file should I edit?"},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueAwaitingUser},
		},
		{
			name:  "awaiting-user: ends with fullwidth question mark",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "どのファイルを編集しますか？"},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueAwaitingUser},
		},
		{
			name:  "awaiting-user: trailing whitespace ignored",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "which file?   \n"},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueAwaitingUser},
		},
		{
			name:  "no-open-todos: all completed",
			input: AutoContinueInput{Items: doneItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "all done"},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 0, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueNoOpenTodos},
		},
		{
			name:  "no-open-todos: empty list",
			input: AutoContinueInput{Items: nil, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "done"},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 0, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueNoOpenTodos},
		},
		{
			name:  "awaiting-background-jobs: jobs running",
			input: AutoContinueInput{Items: openItems, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, HasBackgroundJobs: true},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 0, MaxAutoContinues: 10, Reason: AutoContinueAwaitingBackground},
		},
		{
			name:  "max-reached: budget exhausted",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: 10, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "step done"},
			want:  AutoContinueDecision{ShouldContinue: false, OpenTodoCount: 2, ContinuesUsed: 10, MaxAutoContinues: 10, Reason: AutoContinueMaxReached},
		},
		{
			name:  "unlimited: 0 budget skips check",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: 999, MaxAutoContinues: 0, TurnOK: true, HasConversation: true, TurnText: "step done"},
			want:  AutoContinueDecision{ShouldContinue: true, OpenTodoCount: 2, ContinuesUsed: 999, MaxAutoContinues: 0, Reason: AutoContinueContinue},
		},
		{
			name:  "negative index clamped to 0",
			input: AutoContinueInput{Items: openItems, AutoContinueIndex: -5, MaxAutoContinues: 10, TurnOK: true, HasConversation: true, TurnText: "step"},
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

func TestEndsWithQuestion(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"hello?", true},
		{"hello？", true},
		{"hello?   ", true},
		{"hello?\n\n", true},
		{"hello", false},
		{"", false},
		{"hello.", false},
		{"? ", true},
	}
	for _, c := range cases {
		if got := endsWithQuestion(c.text); got != c.want {
			t.Errorf("endsWithQuestion(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
