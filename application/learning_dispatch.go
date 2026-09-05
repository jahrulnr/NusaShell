package application

import (
	"encoding/json"

	"nusashell/contracts"
)

func (a *App) dispatchLearning(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodLearningSearch:     decodeReq(a.handleLearningSearch),
		contracts.MethodLearningGraph:      noPayload(a.handleLearningGraph),
		contracts.MethodLearningLog:        decodeReq(a.handleLearningLog),
		contracts.MethodLearningJobsList:   noPayload(a.handleLearningJobsList),
		contracts.MethodLearningJobsStatus: decodeReq(a.handleLearningJobsStatus),
	}, "learning")(method, payload)
}

func (a *App) handleLearningJobsList() (any, *contracts.RPCError) {
	out := make([]contracts.LearningJobDTO, 0)
	if a.LearningJobs == nil {
		return contracts.LearningJobListResult{Jobs: out}, nil
	}
	for _, j := range a.LearningJobs.List() {
		if j == nil {
			continue
		}
		out = append(out, contracts.LearningJobDTOFromDomain(j))
	}
	return contracts.LearningJobListResult{Jobs: out}, nil
}

func (a *App) handleLearningJobsStatus(req contracts.LearningJobStatusRequest) (any, *contracts.RPCError) {
	if req.ID == "" || a.LearningJobs == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "learning job not found"}
	}
	j, err := a.LearningJobs.Get(req.ID)
	if err != nil || j == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "learning job not found"}
	}
	return contracts.LearningJobDTOFromDomain(j), nil
}
