package telemetry

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes telemetry.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "telemetry not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodTelemetryReport: rpcdispatch.DecodeReq(s.handleReport),
	}, "telemetry")(method, payload)
}
