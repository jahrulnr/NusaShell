package transport

import (
	dtransport "github.com/jahrulnr/nusashell/infrastructure/delivery/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// UsagePayload is the shared Usage wire shape — one definition in the
// delivery-level transport package, reused here so HTTP boundaries keep
// a single import.
type UsagePayload = dtransport.UsagePayload

// MarshalUsage projects a domain Usage onto the wire shape.
func MarshalUsage(u valueobject.Usage) UsagePayload {
	return dtransport.MarshalUsage(u)
}
