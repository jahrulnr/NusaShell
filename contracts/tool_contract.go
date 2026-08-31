package contracts

// ToolContractVersion is the version of the built-in tool presentation
// contract. It changes only when the JSON shape or the meaning of the
// presentation fields changes in a way that requires a new frontend adapter.
const ToolContractVersion = 1

// ToolContractRefDTO is embedded in every tool presentation. The reference is
// intentionally small so stored snapshots and live events remain cheap while
// the catalog can carry the complete input/output description separately.
// Frontend cards use CSSClass as the base identity and expose the deterministic
// `${CSSClass}-request` and `${CSSClass}-result` sub-hooks.
type ToolContractRefDTO struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	CSSClass string `json:"css_class"`
}

// ToolContractPresentationDTO describes the visual variants a tool may emit.
// Dispatcher tools can expose more than one variant depending on their op;
// ordinary built-ins have one entry in each list.
type ToolContractPresentationDTO struct {
	Variants        []string `json:"variants"`
	Formats         []string `json:"formats"`
	RequestFields   []string `json:"request_fields,omitempty"`
	ResultFields    []string `json:"result_fields,omitempty"`
	AttachmentTypes []string `json:"attachment_types,omitempty"`
}

// ToolContractDTO is the complete frontend contract for one enabled tool.
// InputSchema is the same schema sent to the model, making the catalog a
// single source of truth for future request editors and validation.
type ToolContractDTO struct {
	Name         string                      `json:"name"`
	Description  string                      `json:"description,omitempty"`
	ID           string                      `json:"id"`
	Version      int                         `json:"version"`
	CSSClass     string                      `json:"css_class"`
	InputSchema  map[string]any              `json:"input_schema"`
	Presentation ToolContractPresentationDTO `json:"presentation"`
}

// ToolContractsRequest selects the workspace-sensitive tool roster. An empty
// workspace deliberately returns the roster without project-memory tools.
type ToolContractsRequest struct {
	Workspace string `json:"workspace,omitempty"`
}

// ToolContractsResult is returned by agent.tools.contracts.
type ToolContractsResult struct {
	Version int               `json:"version"`
	Tools   []ToolContractDTO `json:"tools"`
}
