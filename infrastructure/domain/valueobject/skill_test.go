package valueobject

import (
	"strings"
	"testing"
)

func TestSkillDigestIsTheHexSHA256OfTheWholeFile(t *testing.T) {
	cases := map[string]string{
		"":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"abc": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	}
	for content, want := range cases {
		if got := SkillDigest(content); got != want {
			t.Errorf("SkillDigest(%q) = %s, want %s", content, got, want)
		}
	}
}

func TestSkillDigestCoversTheFrontmatter(t *testing.T) {
	file := "---\nname: release-notes\ndescription: Use when writing notes\n---\n## Steps\n1. read the log"
	edited := strings.Replace(file, "Use when writing notes", "Use when writing release notes", 1)
	if SkillDigest(file) == SkillDigest(edited) {
		t.Error("an edit to the frontmatter left the digest unchanged")
	}
}
