package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchLearning routes learning.* RPC methods to their handlers. Called
// by App.Dispatch for any method whose first segment is "learning".
func (a *App) dispatchLearning(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodLearningSearch:           decodeReq(a.handleLearningSearch),
		contracts.MethodLearningGraph:            noPayload(a.handleLearningGraph),
		contracts.MethodLearningLog:              decodeReq(a.handleLearningLog),
		contracts.MethodLearningReviewTranscript: decodeReq(a.handleLearningReviewTranscript),
	}, "learning")(method, payload)
}
