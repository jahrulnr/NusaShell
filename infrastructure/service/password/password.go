// Package password implements the PasswordHasher port with bcrypt.
package password

import (
	"golang.org/x/crypto/bcrypt"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Bcrypt hashes with bcrypt at the given cost.
type Bcrypt struct{ Cost int }

var _ dservice.PasswordHasher = Bcrypt{}

func (b Bcrypt) Hash(password string) (string, error) {
	cost := b.Cost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	return string(h), err
}

func (Bcrypt) Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
