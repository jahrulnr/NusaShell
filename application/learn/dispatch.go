package learn

import (
	"encoding/json"
	"strings"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes learning.* and experience.* RPC methods.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "learning not available"}
	}
	if strings.HasPrefix(method, "experience.") {
		return rpcdispatch.Table(map[string]rpcdispatch.Handler{
			contracts.MethodExperienceList:   rpcdispatch.DecodeReq(s.HandleExperienceList),
			contracts.MethodExperienceGet:    rpcdispatch.DecodeReq(s.HandleExperienceGet),
			contracts.MethodExperienceDelete: rpcdispatch.DecodeReq(s.HandleExperienceDelete),
		}, "experience")(method, payload)
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodLearningSearch:     rpcdispatch.DecodeReq(s.HandleLearningSearch),
		contracts.MethodLearningGraph:      rpcdispatch.NoPayload(s.HandleLearningGraph),
		contracts.MethodLearningLog:        rpcdispatch.DecodeReq(s.HandleLearningLog),
		contracts.MethodLearningLogDelete:  rpcdispatch.DecodeReq(s.HandleLearningLogDelete),
		contracts.MethodLearningJobsList:   rpcdispatch.NoPayload(s.HandleLearningJobsList),
		contracts.MethodLearningJobsStatus: rpcdispatch.DecodeReq(s.HandleLearningJobsStatus),
	}, "learning")(method, payload)
}
