package domain

import "strings"

// SplitQualifiedModel splits a "provider:model" string into its providerID and
// modelID parts. ok is false when the string lacks a colon or the providerID
// would be empty.
func SplitQualifiedModel(s string) (providerID, modelID string, ok bool) {
	idx := strings.IndexByte(s, ':')
	if idx <= 0 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

// RequiresKey reports whether a provider kind needs a user-supplied API key.
// Local endpoints (LM Studio via chat kind) work without a key.
func RequiresKey(kind ProviderKind) bool {
	return KindCaps(kind).RequiresKey
}
