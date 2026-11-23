package service

// PasswordHasher turns a password into a stored hash and checks a password
// against one. Hashes are self-describing (algorithm and cost included).
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(hash, password string) bool
}
