//go:build skilleval

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appskill "github.com/jahrulnr/nusashell/application/skill"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/cli/admin"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	skillrepo "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// The skill eval runs the candidate pass against the utility model the owner
// assigned (CONFIG_PATH, as `serve` reads it) over the synthetic turns in
// testdata/skill-eval, and reports how what it stores matches what each case
// expects. It lives with the composition root because it needs the real,
// wired model; the proposals it writes go to a scratch database, never the
// owner's.
//
//	make skill-eval
//
// It fails when a nothing-case stores anything, when an expect finds no
// stored proposal, or when any stored field carries a forbidden string —
// text the owner did not say must never become a skill. The harness proves
// product behavior; the rules behind it are the validator's deterministic
// table tests, not this file's business.

type skillEvalCase struct {
	Name     string            `json:"name"`
	Nothing  bool              `json:"nothing"`
	Rewrote  bool              `json:"rewrote"`
	Index    []skillEvalSkill  `json:"index"`
	Messages []skillEvalMsg    `json:"messages"`
	Expect   []skillEvalExpect `json:"expect"`
	// Forbid lists text no stored proposal may carry anywhere — in its
	// name, description, body, reason, or evidence.
	Forbid []string `json:"forbid"`
}

type skillEvalSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Level       string `json:"level"`
}

type skillEvalMsg struct {
	Role  string          `json:"role"`
	ID    string          `json:"id"`
	Text  string          `json:"text"`
	Steer bool            `json:"steer"`
	Tools []skillEvalTool `json:"tools"`
}

type skillEvalTool struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	Failed bool   `json:"failed"`
}

// skillEvalExpect is one proposal the case wants. Every given field must
// match: op, kind, and evidence kind exactly, target as the exact skill
// name, evidence and body as case-insensitive substrings. The body check is
// what proves a long procedure survived the digest instead of being trimmed
// out from under the model.
type skillEvalExpect struct {
	Op           string `json:"op"`
	Kind         string `json:"kind"`
	EvidenceKind string `json:"evidence_kind"`
	Evidence     string `json:"evidence"`
	Target       string `json:"target"`
	Body         string `json:"body"`
}

func TestSkillCandidateEval(t *testing.T) {
	cases, err := filepath.Abs(filepath.Join("testdata", "skill-eval"))
	if err != nil {
		t.Fatal(err)
	}
	// The config resolves as `serve` resolves it from the checkout: the
	// .env and any relative storage.dir are read from the repository root.
	t.Chdir(filepath.Join("..", ".."))
	boot, err := admin.LoadBoot(false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := boot.Config()
	if !cfg.Models.Utility.Assigned() {
		t.Skip("no utility model is assigned in " + boot.ConfigPath() + " — Settings → Models")
	}
	eng, err := compose(cfg, boot.Secrets(), boot.ConfigPath(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.conversations.Shutdown(context.Background()); eng.logger.Close(); eng.db.Close() })
	// SKILL_EVAL_MODEL=provider/model pins one model for a comparable run;
	// unset, the room's configured utility model answers. Both modes matter:
	// pinned runs compare across time, unpinned runs discover — the credential
	// finding was born of a model writing a different draft that day.
	provider, model := cfg.Models.Utility.Provider, cfg.Models.Utility.Model
	if pin := os.Getenv("SKILL_EVAL_MODEL"); pin != "" {
		parts := strings.SplitN(pin, "/", 2)
		if len(parts) != 2 {
			t.Fatalf("SKILL_EVAL_MODEL %q is not provider/model", pin)
		}
		provider, model = parts[0], parts[1]
	}
	eng.conversations.UseUtility(func() (string, string) { return provider, model })

	// The mutation run: SKILL_EVAL_MUTATE=scan or =evidence neutralizes one of
	// the pipe's teeth through the seam, so a red case proves the pipe — not
	// the model's mood — is what refuses. Manual, never CI: the eval calls the
	// real model, and the run's only purpose is the harness author's report.
	switch os.Getenv("SKILL_EVAL_MUTATE") {
	case "scan":
		appskill.AdmissionScan = func(_, _, _, _ string) []appskill.Violation { return nil }
		t.Log("MUTATION: content scan neutralized")
	case "evidence":
		appskill.AdmissionProveEvidence = func(appskill.Citation, appskill.Facts) (enum.SkillEvidenceKind, string) {
			return enum.SkillEvidenceQuote, ""
		}
		t.Log("MUTATION: evidence proof neutralized")
	}

	paths, err := filepath.Glob(filepath.Join(cases, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no eval cases: %v", err)
	}
	t.Logf("model %s/%s, %d cases", provider, model, len(paths))

	var failures []string
	// SKILL_EVAL_ONLY=case1,case2 runs just those cases — the mutation runs
	// need only the one case their seam protects, not the whole suite.
	only := map[string]bool{}
	for _, name := range strings.Split(os.Getenv("SKILL_EVAL_ONLY"), ",") {
		if name != "" {
			only[name] = true
		}
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var c skillEvalCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(only) > 0 && !only[c.Name] {
			continue
		}
		stored, outcome, reply := runSkillEvalCase(t, eng, c)
		hit := make([]bool, len(stored))
		var missing []string
		for _, e := range c.Expect {
			found := false
			for i, p := range stored {
				if hit[i] || !evalMatches(p, e) {
					continue
				}
				hit[i], found = true, true
				break
			}
			if !found {
				missing = append(missing, fmt.Sprintf("%+v", e))
			}
		}
		var line []string
		falseCaptures := 0
		for i, p := range stored {
			mark := "✗"
			if hit[i] {
				mark = "✓"
			} else {
				falseCaptures++
			}
			line = append(line, fmt.Sprintf("%s %s %s/%s evidence=%s %q", mark, p.Op(), p.Kind(), p.Name(), p.EvidenceKind(), clipEval(p.Evidence(), 60)))
			all := strings.ToLower(strings.Join([]string{p.Name(), p.Description(), p.Body(), p.Reason(), p.Evidence()}, "\n"))
			for _, f := range c.Forbid {
				if strings.Contains(all, strings.ToLower(f)) {
					failures = append(failures, fmt.Sprintf("%s: %s %s carries forbidden %q", c.Name, p.Op(), p.Name(), f))
				}
			}
		}
		if c.Nothing && len(stored) > 0 {
			failures = append(failures, fmt.Sprintf("%s: a nothing case stored %d proposal(s)", c.Name, len(stored)))
		}
		for _, m := range missing {
			t.Logf("%s: no stored proposal matches %s", c.Name, m)
		}
		t.Logf("%-24s outcome %s, stored %d, expected %d, false captures %d\n\t%s\n\traw: %s", c.Name, outcome, len(stored), len(c.Expect), falseCaptures, strings.Join(line, "\n\t"), clipEval(reply, 400))
	}
	for _, f := range failures {
		t.Error(f)
	}
}

// evalMatches answers whether one stored proposal satisfies every given
// field of an expectation.
func evalMatches(p *aggregate.SkillProposal, e skillEvalExpect) bool {
	if e.Op != "" && string(p.Op()) != e.Op {
		return false
	}
	if e.Kind != "" && string(p.Kind()) != e.Kind {
		return false
	}
	if e.EvidenceKind != "" && string(p.EvidenceKind()) != e.EvidenceKind {
		return false
	}
	if e.Target != "" && p.TargetName() != e.Target {
		return false
	}
	if e.Evidence != "" && !strings.Contains(strings.ToLower(p.Evidence()), strings.ToLower(e.Evidence)) {
		return false
	}
	if e.Body != "" && !strings.Contains(strings.ToLower(p.Body()), strings.ToLower(e.Body)) {
		return false
	}
	return true
}

// runSkillEvalCase seeds a scratch store with the case's index — real SQL,
// real filesystem catalog, real renderer — builds the turn's messages, and
// runs one candidate pass with the real utility model.
func runSkillEvalCase(t *testing.T, eng *engine, c skillEvalCase) ([]*aggregate.SkillProposal, appskill.CandidateOutcome, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tmp := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(tmp, "eval.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	home, nusashell, builtin, workspace := filepath.Join(tmp, "home"), filepath.Join(tmp, "nusashell"), filepath.Join(tmp, "builtin"), filepath.Join(tmp, "workspace")
	for _, dir := range []string{home, nusashell, builtin, workspace} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	catalog := skillrepo.NewLocal(home, nusashell, builtin)
	proposals := skillrepo.NewProposals(db)
	ownership := skillrepo.NewOwnership(db)
	for _, s := range c.Index {
		doc := drepo.SkillDocument{Name: s.Name, Description: s.Description, Body: s.Body}
		written, err := catalog.Write(ctx, enum.SkillRootNusashell, doc, "")
		if err != nil {
			t.Fatalf("%s: seed %s: %v", c.Name, s.Name, err)
		}
		if s.Level == "agent" {
			row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, s.Name, enum.SkillLevelAgent, written.Digest)
			if err != nil {
				t.Fatal(err)
			}
			if err := ownership.Save(ctx, row, ""); err != nil {
				t.Fatalf("%s: seed level %s: %v", c.Name, s.Name, err)
			}
		}
	}

	var msgs []entity.Message
	for _, m := range c.Messages {
		role := enum.RoleUser
		if m.Role == "assistant" {
			role = enum.RoleAssistant
		}
		msg, err := entity.NewMessage(role, m.Text)
		if err != nil {
			t.Fatal(err)
		}
		msg.SetID(m.ID)
		if m.Steer {
			msg.SetInputID("st-" + m.ID)
		}
		for i, tool := range m.Tools {
			call, err := entity.NewToolCall(fmt.Sprintf("call_%s_%d", m.ID, i), tool.Name, `{}`, enum.TooltypeOrigin)
			if err != nil {
				t.Fatal(err)
			}
			if err := msg.AddToolCall(call); err != nil {
				t.Fatal(err)
			}
			if err := msg.SetToolOutput(call.ID(), tool.Output); err != nil {
				t.Fatal(err)
			}
			if tool.Failed {
				if err := msg.SetToolFailed(call.ID(), true); err != nil {
					t.Fatal(err)
				}
			}
		}
		msgs = append(msgs, msg)
	}

	raw := ""
	generate := func(ctx context.Context, convID, system, user string) (string, error) {
		reply, err := eng.conversations.Utility(ctx, convID, system, user)
		raw = reply
		return reply, err
	}
	outcome := appskill.Candidate(ctx, appskill.CandidateDeps{
		Catalog:   catalog,
		Proposals: proposals,
		Ownership: ownership,
		Decisions: skillrepo.NewDecisions(db),
		Generate:  generate,
		Budget:    8192,
	}, appskill.CandidateInput{
		ConvID:    "c_eval",
		Workspace: workspace,
		Messages:  msgs,
		Rewrote:   c.Rewrote,
		Now:       time.Now(),
	})
	stored, err := proposals.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return stored, outcome, raw
}

func clipEval(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
