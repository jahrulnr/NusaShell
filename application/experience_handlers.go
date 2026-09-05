package application

import (
	"encoding/json"
	"strings"

	"nusashell/contracts"
)

func (a *App) dispatchExperience(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return tableDispatcher(map[string]rpcHandler{
		contracts.MethodExperienceList: noPayload(a.handleExperienceList),
		contracts.MethodExperienceGet:  decodeReq(a.handleExperienceGet),
	}, "experience")(method, payload)
}

func (a *App) handleExperienceList() (any, *contracts.RPCError) {
	out := make([]contracts.ExperienceDTO, 0)
	if a.Experiences == nil {
		return contracts.ExperienceListResult{Experiences: out}, nil
	}
	for _, e := range a.Experiences.List() {
		if e == nil {
			continue
		}
		out = append(out, contracts.ExperienceDTOFromDomain(e))
	}
	return contracts.ExperienceListResult{Experiences: out}, nil
}

func (a *App) handleExperienceGet(req contracts.ExperienceIDRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.ID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "experience id is required"}
	}
	if a.Experiences == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	e, err := a.Experiences.Get(req.ID)
	if err != nil || e == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "experience not found"}
	}
	return contracts.ExperienceGetResult{Experience: contracts.ExperienceDTOFromDomain(e)}, nil
}
