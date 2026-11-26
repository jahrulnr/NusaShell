package enum

import "testing"

func TestSkillKindParsesToolAndMethodOnly(t *testing.T) {
	for _, v := range []string{"tool", "method"} {
		if _, ok := ParseSkillKind(v); !ok {
			t.Errorf("kind %q rejected", v)
		}
	}
	for _, v := range []string{"", "Tool", "skill", "tools"} {
		if _, ok := ParseSkillKind(v); ok {
			t.Errorf("kind %q accepted", v)
		}
	}
}

func TestSkillEvidenceKindParsesTheFourKindsOnly(t *testing.T) {
	for _, v := range []string{"quote", "rewrite", "repeat", "call"} {
		if _, ok := ParseSkillEvidenceKind(v); !ok {
			t.Errorf("evidence kind %q rejected", v)
		}
	}
	for _, v := range []string{"", "page", "assistant", "file", "Quote"} {
		if _, ok := ParseSkillEvidenceKind(v); ok {
			t.Errorf("evidence kind %q accepted", v)
		}
	}
}

func TestSkillProposalOpParsesCreateAndPatchOnly(t *testing.T) {
	for _, v := range []string{"create", "patch"} {
		if _, ok := ParseSkillProposalOp(v); !ok {
			t.Errorf("op %q rejected", v)
		}
	}
	for _, v := range []string{"", "archive", "Patch", "update"} {
		if _, ok := ParseSkillProposalOp(v); ok {
			t.Errorf("op %q accepted", v)
		}
	}
}

func TestSkillProposalStatusHasNoFailedState(t *testing.T) {
	for _, v := range []string{"pending", "accepted", "dismissed"} {
		if _, ok := ParseSkillProposalStatus(v); !ok {
			t.Errorf("status %q rejected", v)
		}
	}
	for _, v := range []string{"", "failed", "Pending"} {
		if _, ok := ParseSkillProposalStatus(v); ok {
			t.Errorf("status %q accepted", v)
		}
	}
}

func TestSkillLevelParsesUserAndAgentOnly(t *testing.T) {
	for _, v := range []string{"user", "agent"} {
		if _, ok := ParseSkillLevel(v); !ok {
			t.Errorf("level %q rejected", v)
		}
	}
	for _, v := range []string{"", "owner", "global"} {
		if _, ok := ParseSkillLevel(v); ok {
			t.Errorf("level %q accepted", v)
		}
	}
}

func TestSkillRootParsesTheFourRootsOnly(t *testing.T) {
	for _, v := range []string{"builtin", "nusashell", "global", "workspace"} {
		if _, ok := ParseSkillRoot(v); !ok {
			t.Errorf("root %q rejected", v)
		}
	}
	for _, v := range []string{"", "Global", "learned", "user"} {
		if _, ok := ParseSkillRoot(v); ok {
			t.Errorf("root %q accepted", v)
		}
	}
}
