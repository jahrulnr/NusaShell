package application

import (
	"encoding/json"

	"nusashell/contracts"
)

// dispatchLearning routes learning.* RPC methods to their handlers. Called
// by App.Dispatch for any method whose first segment is "learning".
func (a *App) dispatchLearning(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodLearningSearch:
		var req contracts.LearningSearchRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleLearningSearch(req)
	case contracts.MethodLearningGraph:
		return a.handleLearningGraph()
	case contracts.MethodLearningLog:
		var req contracts.LearningLogRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleLearningLog(req)
	case contracts.MethodLearningReviewTranscript:
		var req contracts.LearningReviewTranscriptRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleLearningReviewTranscript(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown learning method: " + method}
	}
}
