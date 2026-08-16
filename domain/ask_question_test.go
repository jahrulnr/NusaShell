package domain

import "testing"

func TestValidateAskQuestionRequest(t *testing.T) {
	validOpts := []AskQuestionOption{
		{ID: "a", Label: "Option A"},
		{ID: "b", Label: "Option B"},
	}

	cases := []struct {
		name      string
		question  string
		options   []AskQuestionOption
		allowFree bool
		multi     bool
		wantErr   error
	}{
		{"valid", "Which?", validOpts, true, false, nil},
		{"empty question", "", validOpts, true, false, ErrAskQuestionEmpty},
		{"no options", "Which?", nil, true, false, ErrAskOptionsEmpty},
		{"too many options", "Which?", make([]AskQuestionOption, 9), true, false, ErrAskOptionsTooMany},
		{"missing id", "Which?", []AskQuestionOption{{Label: "No ID"}}, true, false, ErrAskOptionMissingIDLabel},
		{"missing label", "Which?", []AskQuestionOption{{ID: "x"}}, true, false, ErrAskOptionMissingIDLabel},
		{"duplicate id", "Which?", []AskQuestionOption{{ID: "a", Label: "A"}, {ID: "a", Label: "B"}}, true, false, ErrAskOptionDuplicateID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateAskQuestionRequest(c.question, c.options, c.allowFree, c.multi)
			if c.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantErr != nil && err == nil {
				t.Fatalf("expected error %v, got nil", c.wantErr)
			}
			if c.wantErr != nil && err != c.wantErr {
				t.Fatalf("error = %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestBuildAskQuestionResult(t *testing.T) {
	req := AskQuestionRequest{
		Question:      "Which?",
		Options:       []AskQuestionOption{{ID: "a", Label: "Option A"}, {ID: "b", Label: "Option B"}},
		AllowFreeText: true,
		MultiSelect:   false,
	}

	cases := []struct {
		name    string
		req     AskQuestionRequest
		answer  AskQuestionAnswer
		wantErr error
		wantAns string
	}{
		{
			name:    "option answer",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaOption, OptionIDs: []string{"a"}},
			wantAns: "Option A",
		},
		{
			name:    "free text answer",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaText, Text: "custom reply"},
			wantAns: "custom reply",
		},
		{
			name:    "option + supplementary text",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaOption, OptionIDs: []string{"a"}, Text: "note"},
			wantAns: "Option A — note",
		},
		{
			name:    "empty free text",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaText, Text: ""},
			wantErr: ErrAskFreeTextEmpty,
		},
		{
			name:    "free text not allowed",
			req:     AskQuestionRequest{Question: "Q", Options: req.Options, AllowFreeText: false},
			answer:  AskQuestionAnswer{Via: AskAnswerViaText, Text: "hello"},
			wantErr: ErrAskFreeTextNotAllowed,
		},
		{
			name:    "no option selected",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaOption, OptionIDs: nil},
			wantErr: ErrAskNoOptionSelected,
		},
		{
			name:    "multi-select violation",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaOption, OptionIDs: []string{"a", "b"}},
			wantErr: ErrAskMultiSelectViolation,
		},
		{
			name:    "unknown option id",
			req:     req,
			answer:  AskQuestionAnswer{Via: AskAnswerViaOption, OptionIDs: []string{"z"}},
			wantErr: ErrAskUnknownOptionID,
		},
		{
			name:    "multi-select allowed",
			req:     AskQuestionRequest{Question: "Q", Options: req.Options, MultiSelect: true},
			answer:  AskQuestionAnswer{Via: AskAnswerViaOption, OptionIDs: []string{"a", "b"}},
			wantAns: "Option A, Option B",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := BuildAskQuestionResult(c.req, c.answer)
			if c.wantErr == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantErr != nil && err != c.wantErr {
				t.Fatalf("error = %v, want %v", err, c.wantErr)
			}
			if c.wantErr == nil && result.Answer != c.wantAns {
				t.Fatalf("answer = %q, want %q", result.Answer, c.wantAns)
			}
		})
	}
}
