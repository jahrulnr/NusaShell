package domain

// AskQuestionOption is a selectable choice in an ask_question prompt.
type AskQuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Image       string `json:"image,omitempty"`
}

// AskQuestionRequest is the validated payload the model sends to ask_question.
type AskQuestionRequest struct {
	Question     string              `json:"question"`
	Options      []AskQuestionOption `json:"options"`
	AllowFreeText bool               `json:"allow_free_text"`
	MultiSelect  bool                `json:"multi_select"`
}

// AskAnswerVia indicates whether the user answered via option or free text.
type AskAnswerVia string

const (
	AskAnswerViaOption AskAnswerVia = "option"
	AskAnswerViaText   AskAnswerVia = "text"
)

// AskQuestionAnswer is the user's response to an ask_question prompt.
type AskQuestionAnswer struct {
	Via       AskAnswerVia `json:"via"`
	OptionIDs []string     `json:"option_ids,omitempty"`
	Text      string       `json:"text,omitempty"`
}

// AskQuestionResult is the tool result returned to the model after the user
// answers. The Answer field is a human-readable summary (e.g. "Option A" or
// "custom text" or "Option A — custom note").
type AskQuestionResult struct {
	OK   bool   `json:"ok"`
	Via  string `json:"via"`
	Answer string `json:"answer"`
	OptionIDs []string `json:"option_ids,omitempty"`
	Text      string   `json:"text,omitempty"`
}

// MaxAskOptions is the maximum number of options allowed in an ask_question
// call (matches Electron).
const MaxAskOptions = 8

// ValidateAskQuestionRequest validates the model's ask_question input. Returns
// a normalised request with trimmed fields. This is a pure domain rule so both
// the tool handler and tests can call it without I/O.
func ValidateAskQuestionRequest(question string, options []AskQuestionOption, allowFreeText, multiSelect bool) (AskQuestionRequest, error) {
	q := ""
	if question != "" {
		q = question
	}
	if q == "" {
		return AskQuestionRequest{}, ErrAskQuestionEmpty
	}
	if len(options) == 0 {
		return AskQuestionRequest{}, ErrAskOptionsEmpty
	}
	if len(options) > MaxAskOptions {
		return AskQuestionRequest{}, ErrAskOptionsTooMany
	}
	seen := make(map[string]bool, len(options))
	cleaned := make([]AskQuestionOption, 0, len(options))
	for _, opt := range options {
		id := opt.ID
		label := opt.Label
		if id == "" || label == "" {
			return AskQuestionRequest{}, ErrAskOptionMissingIDLabel
		}
		if seen[id] {
			return AskQuestionRequest{}, ErrAskOptionDuplicateID
		}
		seen[id] = true
		cleaned = append(cleaned, AskQuestionOption{
			ID:          id,
			Label:       label,
			Description: opt.Description,
			Default:     opt.Default,
			Icon:        opt.Icon,
			Image:       opt.Image,
		})
	}
	return AskQuestionRequest{
		Question:      q,
		Options:       cleaned,
		AllowFreeText: allowFreeText,
		MultiSelect:   multiSelect,
	}, nil
}

// BuildAskQuestionResult validates the user's answer against the request and
// builds the tool result. Returns an error if the answer is invalid (unknown
// option id, empty free text, free text not allowed, multi-select violation).
func BuildAskQuestionResult(req AskQuestionRequest, answer AskQuestionAnswer) (AskQuestionResult, error) {
	if answer.Via == AskAnswerViaText {
		text := answer.Text
		if text == "" {
			return AskQuestionResult{}, ErrAskFreeTextEmpty
		}
		if !req.AllowFreeText {
			return AskQuestionResult{}, ErrAskFreeTextNotAllowed
		}
		return AskQuestionResult{OK: true, Via: string(AskAnswerViaText), Answer: text, Text: text}, nil
	}

	optionIDs := answer.OptionIDs
	if len(optionIDs) == 0 {
		return AskQuestionResult{}, ErrAskNoOptionSelected
	}
	// Deduplicate.
	seen := make(map[string]bool, len(optionIDs))
	unique := make([]string, 0, len(optionIDs))
	for _, id := range optionIDs {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if !req.MultiSelect && len(unique) > 1 {
		return AskQuestionResult{}, ErrAskMultiSelectViolation
	}
	byID := make(map[string]AskQuestionOption, len(req.Options))
	for _, opt := range req.Options {
		byID[opt.ID] = opt
	}
	labels := make([]string, 0, len(unique))
	for _, id := range unique {
		opt, ok := byID[id]
		if !ok {
			return AskQuestionResult{}, ErrAskUnknownOptionID
		}
		labels = append(labels, opt.Label)
	}
	// Append supplementary free text if present and allowed.
	text := answer.Text
	answerStr := joinLabels(labels)
	if text != "" {
		if !req.AllowFreeText {
			return AskQuestionResult{}, ErrAskFreeTextNotAllowed
		}
		answerStr = answerStr + " — " + text
	}
	result := AskQuestionResult{
		OK:        true,
		Via:       string(AskAnswerViaOption),
		Answer:    answerStr,
		OptionIDs: unique,
	}
	if text != "" {
		result.Text = text
	}
	return result, nil
}

// joinLabels joins option labels with ", ".
func joinLabels(labels []string) string {
	out := ""
	for i, l := range labels {
		if i > 0 {
			out += ", "
		}
		out += l
	}
	return out
}
