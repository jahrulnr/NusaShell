package domain

import "time"

// DefaultRateLimitWindow is assumed when a 429 response carries no
// Retry-After header. TokenRouter uses 1 minute; other gateways are
// similar.
const DefaultRateLimitWindow = time.Minute
