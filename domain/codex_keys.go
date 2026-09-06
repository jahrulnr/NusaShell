package domain

// AccountKeyPrefix returns the CredentialStore key prefix for additional
// accounts of a Codex provider: "{providerID}:account:".
func AccountKeyPrefix(providerID string) string {
	return providerID + ":account:"
}

// AccountKey returns the CredentialStore key for a specific account.
func AccountKey(providerID, accountID string) string {
	return AccountKeyPrefix(providerID) + accountID
}
