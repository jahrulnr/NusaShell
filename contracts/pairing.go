package contracts

import "time"

// Pairing error codes. These extend the base ErrorCode set so the frontend
// can map each pairing failure to a specific, actionable message.
const (
	CodePairingRequired      ErrorCode = "PAIRING_REQUIRED"
	CodePairingExpired       ErrorCode = "PAIRING_EXPIRED"
	CodePairingRejected      ErrorCode = "PAIRING_REJECTED"
	CodePairingUsed          ErrorCode = "PAIRING_ALREADY_USED"
	CodePairingRevoked       ErrorCode = "PAIRING_REVOKED"
	CodePairingRateLimit     ErrorCode = "PAIRING_RATE_LIMITED"
	CodePairingNotFound      ErrorCode = "PAIRING_NOT_FOUND"
	CodePairingUnauthorized  ErrorCode = "PAIRING_UNAUTHORIZED"
	CodeRemoteAccessDisabled ErrorCode = "REMOTE_ACCESS_DISABLED"
)

// PairingChallengeCreateResult is returned by pairing.challenge.create.
// The challenge_id is used by the local UI to poll the challenge status.
// The code is the short pairing code shown to the user; it is never returned
// again after this call.
type PairingChallengeCreateResult struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	ExpiresIn   int    `json:"expires_in"` // seconds until expiry
}

// PairingChallengeStatusResult is returned by pairing.status (poll).
type PairingChallengeStatusResult struct {
	ChallengeID string `json:"challenge_id"`
	State       string `json:"state"` // pending | approved | rejected | used | expired
	ExpiresIn   int    `json:"expires_in"`
}

// PairingChallengeExchangeRequest is the body for the public /pairing/exchange
// route. The remote device presents the challenge_id + code to obtain a
// session token.
type PairingChallengeExchangeRequest struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
	DeviceLabel string `json:"device_label,omitempty"`
}

// PairingChallengeExchangeResult is returned by pairing.challenge.exchange.
// The token is never serialized into the public JSON response (json:"-"); it
// is set only as an HttpOnly cookie by the transport layer so it is not
// reachable from browser JavaScript. The application/transport layer still
// reads Token to set the cookie before serializing.
type PairingChallengeExchangeResult struct {
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PairingSessionDTO is one entry in the device/session list.
type PairingSessionDTO struct {
	ID          string    `json:"id"`
	DeviceLabel string    `json:"device_label"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Retained in the wire shape for compatibility. Revoked sessions are
	// terminal and are removed instead of returned in the list.
	Revoked bool `json:"revoked"`
}

// PairingSessionsListResult is returned by pairing.sessions.list.
type PairingSessionsListResult struct {
	Sessions []PairingSessionDTO `json:"sessions"`
}
