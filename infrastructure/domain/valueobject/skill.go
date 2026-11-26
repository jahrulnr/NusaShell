package valueobject

import (
	"crypto/sha256"
	"encoding/hex"
)

// SkillDigest is the sha256 of a learned skill's whole SKILL.md, frontmatter
// included. The file is what the owner edits and what the loop rewrites, so a
// digest of part of it would miss a change to the rest.
func SkillDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
