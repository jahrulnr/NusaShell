package agent

import (
	"context"
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes agent turn and ask_question RPC methods. Conversation,
// todos, workspace listing, and tool contracts stay on the App dispatcher.
func (s *Service) Dispatch(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "agent service not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodTurnsStart: rpcdispatch.DecodeReq(func(req contracts.TurnStartRequest) (any, *contracts.RPCError) {
			return s.HandleTurnsStart(ctx, req)
		}),
		contracts.MethodTurnsStop: rpcdispatch.DecodeReq(s.HandleTurnsStop),
		contracts.MethodToolStop:  rpcdispatch.DecodeReq(s.HandleToolStop),
		contracts.MethodTurnsRetry: rpcdispatch.DecodeReq(func(req contracts.TurnRetryRequest) (any, *contracts.RPCError) {
			return s.HandleTurnsRetry(ctx, req)
		}),
		contracts.MethodTurnsSteer: rpcdispatch.DecodeReq(func(req contracts.TurnSteerRequest) (any, *contracts.RPCError) {
			return s.HandleTurnsSteer(ctx, req)
		}),
		contracts.MethodTurnsCancelSteer: rpcdispatch.DecodeReq(s.HandleTurnsCancelSteer),
		contracts.MethodTurnsActive:      rpcdispatch.DecodeReq(s.HandleTurnsActive),
		contracts.MethodAskAnswer:        rpcdispatch.DecodeReq(s.HandleAskAnswer),
		contracts.MethodAskCancel:        rpcdispatch.DecodeReq(s.HandleAskCancel),
		contracts.MethodAskPending:       rpcdispatch.DecodeReq(s.HandleAskPendingList),
	}, "agent")(method, payload)
}
