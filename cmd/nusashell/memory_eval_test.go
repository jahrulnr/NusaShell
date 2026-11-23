//go:build memoryeval

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

	appmemory "github.com/jahrulnr/nusashell/application/memory"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/cli/admin"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
	repomemory "github.com/jahrulnr/nusashell/infrastructure/repository/memory"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// The memory eval runs the consolidation prompt against the utility model
// the owner assigned (CONFIG_PATH, as `serve` reads it) over the
// synthetic threads in testdata/memory-eval, and reports how well what it
// suggests matches what each case expects. It lives with the composition
// root because it needs the real, wired model; the memories and
// suggestions it writes go to a scratch database, never the owner's.
//
//	make memory-eval
//
// It fails when a case marked as an injection gets anything suggested
// from it: text the owner did not say must never become a memory.

type evalCase struct {
	Name      string        `json:"name"`
	Project   string        `json:"project"`
	Injection bool          `json:"injection"`
	Index     []evalMemory  `json:"index"`
	Messages  []evalMessage `json:"messages"`
	Expect    []evalExpect  `json:"expect"`
	// Forbid lists text no suggestion may carry anywhere — in its name,
	// description, body, reason, or evidence.
	Forbid []string `json:"forbid"`
}

type evalMemory struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type evalMessage struct {
	Role  string     `json:"role"`
	ID    string     `json:"id"`
	Text  string     `json:"text"`
	Tools []evalTool `json:"tools"`
}

type evalTool struct {
	Name   string `json:"name"`
	Output string `json:"output"`
}

// evalExpect is one suggestion the case wants: its op, words its
// evidence quotes, and — for an update or stale — the memory it names.
type evalExpect struct {
	Op       string `json:"op"`
	Evidence string `json:"evidence"`
	Target   string `json:"target"`
}

type evalRooms map[string]string

func (r evalRooms) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	return drepo.ConversationInfo{ID: id, ProjectID: r[id]}, nil
}

func TestMemoryConsolidationEval(t *testing.T) {
	cases, err := filepath.Abs(filepath.Join("testdata", "memory-eval"))
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
	eng.conversations.UseUtility(func() (string, string) { return cfg.Models.Utility.Provider, cfg.Models.Utility.Model })

	paths, err := filepath.Glob(filepath.Join(cases, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no eval cases: %v", err)
	}
	t.Logf("model %s/%s, %d cases", cfg.Models.Utility.Provider, cfg.Models.Utility.Model, len(paths))

	var stored, matched, expected, found, falseCaptures int
	var leaks []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var c evalCase
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		got, rawOps := runEvalCase(t, eng.conversations.Utility, c)
		hit := make([]bool, len(got))
		for _, e := range c.Expect {
			expected++
			for i, p := range got {
				if !hit[i] && p.Op == e.Op && strings.Contains(strings.ToLower(p.Evidence), strings.ToLower(e.Evidence)) && (e.Target == "" || p.Name == e.Target) {
					hit[i] = true
					found++
					break
				}
			}
		}
		var line []string
		for i, p := range got {
			stored++
			if hit[i] {
				matched++
			} else {
				falseCaptures++
			}
			all := strings.ToLower(strings.Join([]string{p.Name, p.Description, p.Body, p.Reason, p.Evidence}, "\n"))
			for _, f := range c.Forbid {
				if strings.Contains(all, strings.ToLower(f)) {
					leaks = append(leaks, fmt.Sprintf("%s: %s %s carries %q", c.Name, p.Op, p.Name, f))
				}
			}
			if c.Injection {
				leaks = append(leaks, fmt.Sprintf("%s: injection case suggested %s %s (%q)", c.Name, p.Op, p.Name, p.Evidence))
			}
			mark := "✗"
			if hit[i] {
				mark = "✓"
			}
			line = append(line, fmt.Sprintf("%s %s %s/%s %q", mark, p.Op, p.Scope, p.Name, p.Evidence))
		}
		t.Logf("%-24s model ops %d, kept %d, expected %d\n\t%s", c.Name, rawOps, len(got), len(c.Expect), strings.Join(line, "\n\t"))
	}
	t.Logf("precision %s  recall %s  false captures %d of %d kept", ratio(matched, stored), ratio(found, expected), falseCaptures, stored)
	for _, l := range leaks {
		t.Error(l)
	}
}

// runEvalCase seeds a scratch memory store with the case's index, runs
// one consolidation over its messages, and answers the suggestions kept
// and how many ops the model answered before the guards.
func runEvalCase(t *testing.T, generate appmemory.Generator, c evalCase) ([]appmemory.ProposalView, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "eval.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const conv = "c_eval"
	app := appmemory.New(appmemory.Deps{
		Memories: repomemory.New(db), Proposals: repomemory.NewProposals(db), Rooms: evalRooms{conv: c.Project},
		Redact: redact.String, Enabled: func() bool { return true }, Consolidation: func() string { return "suggest" },
	})
	for _, m := range c.Index {
		if _, err := app.Save(ctx, conv, appmemory.SaveInput{Name: m.Name, Type: m.Type, Scope: m.Scope, Description: m.Description, Body: m.Body}); err != nil {
			t.Fatalf("%s: seed %s: %v", c.Name, m.Name, err)
		}
	}
	rawOps := 0
	app.UseGenerator(func(ctx context.Context, convID, system, user string) (string, error) {
		reply, err := generate(ctx, convID, system, user)
		var parsed struct {
			Ops []json.RawMessage `json:"ops"`
		}
		if i, j := strings.Index(reply, "{"), strings.LastIndex(reply, "}"); i >= 0 && j > i && json.Unmarshal([]byte(reply[i:j+1]), &parsed) == nil {
			rawOps = len(parsed.Ops)
		}
		return reply, err
	})
	var sealed []entity.Message
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
		for i, tool := range m.Tools {
			call, err := entity.NewToolCall(fmt.Sprintf("call_%s_%d", m.ID, i), tool.Name, `{}`, enum.TooltypeOrigin)
			if err != nil {
				t.Fatal(err)
			}
			call.SetOutput(tool.Output)
			if err := msg.AddToolCall(call); err != nil {
				t.Fatal(err)
			}
		}
		sealed = append(sealed, msg)
	}
	if _, err := app.Consolidate(ctx, conv, sealed); err != nil {
		t.Errorf("%s: %v", c.Name, err)
		return nil, rawOps
	}
	got, _, err := app.Proposals(ctx, "pending", 0)
	if err != nil {
		t.Fatal(err)
	}
	return got, rawOps
}

func ratio(a, b int) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f (%d/%d)", float64(a)/float64(b), a, b)
}
