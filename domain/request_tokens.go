package domain

// Request token estimation heuristics.
//
// These constants govern the rough token approximation used to predict
// whether a request will fit in the model's context window before
// sending it. Exact tokenization belongs to the model provider; these
// heuristics are deliberately conservative so the agent compacts early
// rather than overflowing.
const (
	// RequestTokenCharsPerToken is the rough chars-per-token ratio used
	// when estimating request size. Mirrors EstimateTokens.
	RequestTokenCharsPerToken = 4
	// RequestTokenPerMessageOverhead is the per-message token overhead
	// added to the char-based estimate (provider framing, role tags).
	RequestTokenPerMessageOverhead = 4
	// RequestTokenImageCost is the approximate token cost of one image
	// attachment added on top of the char-based estimate.
	RequestTokenImageCost = 150
	// RequestTokenSafetyBuffer is the multiplier applied to the final
	// token estimate so the agent compacts before the real count reaches
	// the limit.
	RequestTokenSafetyBuffer = 1.05
)
