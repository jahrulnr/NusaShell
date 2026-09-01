package domain

// CapabilityProviderKind classifies who implements a logical capability.
type CapabilityProviderKind string

const (
	CapabilityBuiltin CapabilityProviderKind = "builtin"
	CapabilityMCP     CapabilityProviderKind = "mcp"
)

// CapabilityKind is the surface a capability exposes.
type CapabilityKind string

const (
	CapabilityAction CapabilityKind = "action"
	CapabilityEvent  CapabilityKind = "event"
)

// CapabilityStatus is provider availability, not workflow run state.
type CapabilityStatus string

const (
	CapAvailable  CapabilityStatus = "available"
	CapStarting   CapabilityStatus = "starting"
	CapNotRunning CapabilityStatus = "not_running"
	CapDisabled   CapabilityStatus = "disabled"
	CapMissing    CapabilityStatus = "missing"
	CapError      CapabilityStatus = "error"
)

// AutoStartPolicy is per-automation override of MCP auto-start.
type AutoStartPolicy string

const (
	AutoStartInherit             AutoStartPolicy = "inherit"
	AutoStartAlwaysRequireActive AutoStartPolicy = "always_require_active"
	AutoStartAllow               AutoStartPolicy = "allow_auto_start"
)

// DefaultAutoStart is the recommended automation default. It inherits the
// user's plugin autostart preference: a server whose autostart is off stays
// off unless something explicitly enables it (mcp_enable) or an automation
// author opts in with auto_start: allow. AutoStartAllow would ignore the user
// flag and silently spawn servers, which reads as a security concern in the UI.
const DefaultAutoStart = AutoStartInherit

// WorkflowAvailability is derived from capability status + auto-start.
type WorkflowAvailability string

const (
	AvailRunnable        WorkflowAvailability = "runnable"
	AvailPendingProvider WorkflowAvailability = "pending_provider"
	AvailBlocked         WorkflowAvailability = "blocked"
	AvailError           WorkflowAvailability = "error"
)

// CapabilityBinding is the runtime resolution of a logical name.
type CapabilityBinding struct {
	Capability string
	ProviderID string
	Kind       CapabilityProviderKind
	Status     CapabilityStatus
	Reason     string
	AutoStart  bool
}

// MapAvailability converts provider process state into workflow
// availability. Provider process state is never used as workflow state.
func MapAvailability(status CapabilityStatus, autoStart bool) WorkflowAvailability {
	switch status {
	case CapAvailable:
		return AvailRunnable
	case CapStarting:
		if autoStart {
			return AvailPendingProvider
		}
		return AvailBlocked
	case CapNotRunning:
		if autoStart {
			return AvailPendingProvider
		}
		return AvailBlocked
	case CapDisabled, CapMissing:
		return AvailBlocked
	case CapError:
		return AvailError
	default:
		return AvailBlocked
	}
}

// AllowsAutoStart reports whether a stopped provider may be started to
// satisfy this CI workflow. disabled_by_user always wins.
func AllowsAutoStart(provider CapabilityStatus, policy AutoStartPolicy, serverAutoStart bool) bool {
	if provider == CapDisabled || provider == CapMissing || provider == CapError {
		return false
	}
	if policy == "" {
		policy = DefaultAutoStart
	}
	switch policy {
	case AutoStartAlwaysRequireActive:
		return false
	case AutoStartAllow:
		return provider == CapNotRunning || provider == CapStarting
	case AutoStartInherit:
		return serverAutoStart && (provider == CapNotRunning || provider == CapStarting)
	default:
		return false
	}
}

// ValidationLevel is one of the three independent workflow checks.
type ValidationLevel string

const (
	ValidationSyntax       ValidationLevel = "syntax"
	ValidationCapabilities ValidationLevel = "capabilities"
	ValidationProviders    ValidationLevel = "providers"
)

// ValidationIssue is a structured path-addressed error.
type ValidationIssue struct {
	Path    string
	Code    string
	Message string
	Level   ValidationLevel
}

// ValidationResult must not conflate INVALID, BLOCKED, and VALID.
type ValidationResult struct {
	Syntax         string // OK | INVALID
	Capabilities   string // OK | INVALID | SKIPPED
	Providers      string // OK | BLOCKED | SKIPPED
	ProviderID     string
	ProviderStatus CapabilityStatus
	Issues         []ValidationIssue
}

// Verdict is VALID, INVALID, or BLOCKED.
func (r ValidationResult) Verdict() string {
	if r.Syntax == "INVALID" || r.Capabilities == "INVALID" {
		return "INVALID"
	}
	if r.Providers == "BLOCKED" {
		return "BLOCKED"
	}
	return "VALID"
}

func (r *ValidationResult) Add(issue ValidationIssue) {
	r.Issues = append(r.Issues, issue)
	switch issue.Level {
	case ValidationSyntax:
		r.Syntax = "INVALID"
	case ValidationCapabilities:
		r.Capabilities = "INVALID"
	case ValidationProviders:
		r.Providers = "BLOCKED"
		if issue.Code != "" && r.ProviderStatus == "" {
			switch issue.Code {
			case "provider_disabled":
				r.ProviderStatus = CapDisabled
			case "provider_missing":
				r.ProviderStatus = CapMissing
			case "provider_error":
				r.ProviderStatus = CapError
			case "provider_not_running":
				r.ProviderStatus = CapNotRunning
			}
		}
	}
}

// NewValidationResult starts as OK on every level.
func NewValidationResult() ValidationResult {
	return ValidationResult{Syntax: "OK", Capabilities: "OK", Providers: "OK"}
}
