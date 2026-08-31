package domain

import "time"

// Background review agent policy constants.
//
// The review agent runs a bounded LLM turn after a conversation crosses
// the learning-review threshold. These constants govern the cooldown,
// tool-loop bound, transcript window, and per-field truncation caps so
// the review stays cheap and predictable. The agent itself lives in the
// application layer; the policy constants live here so the rules are
// visible at the layer that owns the memory model.
const (
	// DefaultReviewCooldown is the minimum interval between reviews for
	// one conversation.
	DefaultReviewCooldown = 15 * time.Minute
	// DefaultReviewMemoryEveryNTurns is the trigger threshold: a review
	// is considered after every N turns.
	DefaultReviewMemoryEveryNTurns = 10
	// DefaultReviewMaxToolRounds bounds the review agent's tool loop.
	DefaultReviewMaxToolRounds = 6
	// DefaultReviewTranscriptTailMsgs is how many recent messages the
	// review agent includes in its transcript window.
	DefaultReviewTranscriptTailMsgs = 40
	// DefaultReviewMaxTranscriptChars is the per-message truncation cap
	// in the review transcript.
	DefaultReviewMaxTranscriptChars = 4000
	// DefaultReviewMaxTranscriptTokens is the total transcript token cap
	// (~chars/4).
	DefaultReviewMaxTranscriptTokens = 30000
	// ReviewMaxToolArgsChars caps a single tool call's args in the
	// review transcript.
	ReviewMaxToolArgsChars = 500
	// ReviewMaxToolOutputChars caps a single tool result's output in the
	// review transcript.
	ReviewMaxToolOutputChars = 800
)
