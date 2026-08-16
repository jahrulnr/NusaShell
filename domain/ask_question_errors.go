package domain

import "errors"

// Sentinel errors for ask_question validation. These are returned by the pure
// domain validators and wrapped by the tool/RPC handlers.
var (
	ErrAskQuestionEmpty       = errors.New("question must not be empty")
	ErrAskOptionsEmpty        = errors.New("options must be a non-empty array")
	ErrAskOptionsTooMany      = errors.New("options must contain at most 8 items")
	ErrAskOptionMissingIDLabel = errors.New("each option requires non-empty id and label")
	ErrAskOptionDuplicateID   = errors.New("duplicate option id")
	ErrAskFreeTextEmpty       = errors.New("free-text ask answer must not be empty")
	ErrAskFreeTextNotAllowed  = errors.New("free-text answers are not allowed for this question")
	ErrAskNoOptionSelected    = errors.New("at least one option id is required")
	ErrAskMultiSelectViolation = errors.New("only one option may be selected for this question")
	ErrAskUnknownOptionID     = errors.New("unknown option id")
)
