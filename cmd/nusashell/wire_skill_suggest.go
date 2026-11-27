package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	appskill "github.com/jahrulnr/nusashell/application/skill"
	httpskill "github.com/jahrulnr/nusashell/infrastructure/delivery/http/skill"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
)

// learnedSkillRoot is the nusashell root, a sibling of builtin-skills, and
// only when the mode is suggest. Off and observe return "" and create
// nothing, so their catalog stays the three roots it has today. Suggest
// creates the directory and sweeps write temps a crash left behind.
func learnedSkillRoot(mode, storageDir string) (string, error) {
	if mode != "suggest" {
		return "", nil
	}
	root := filepath.Join(storageDir, "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if err := reposkill.SweepTemps(root); err != nil {
		return "", err
	}
	return root, nil
}

// learnedWriter is the write port an accept goes through. Suggest needs it, so
// a catalog that cannot write refuses suggest at compose, before the database
// opens. Off and observe need no writer and get nil.
func learnedWriter(mode string, catalog drepo.Skills) (drepo.SkillWriter, error) {
	if mode != "suggest" {
		return nil, nil
	}
	writer, ok := catalog.(drepo.SkillWriter)
	if !ok {
		return nil, errors.New("skills.improvement is suggest, but the skill catalog cannot write learned skills, so an accepted proposal would have no writer")
	}
	return writer, nil
}

// skillSurface is what suggest adds. Off and observe return the zero value:
// no page, no finished hook, no edit evidence from this path. Observe keeps
// the hook wireSkillObserve registers; this function does not add one.
// Suggest serves the Skills page, records owner rewrites for the pass to
// cite, and runs one candidate pass per finished turn.
type skillSurface struct {
	Page  httpskill.Deps
	Hook  func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error)
	Edits appjob.EditEvidence
}

func (e *engine) skillSurface(mode, fallback string) skillSurface {
	if e == nil || mode != "suggest" {
		return skillSurface{}
	}
	places := appskill.FolderSources{Conversations: e.convs, Projects: e.projectRepo, Fallback: fallback}
	proposals := reposkill.NewProposals(e.db)
	ownership := reposkill.NewOwnership(e.db)
	decisions := reposkill.NewDecisions(e.db)
	ev := appskill.NewEvidence()
	log := slog.Default()
	// One figure for the pass and for an accept. It was copied from
	// skills.catalog_max_bytes when this engine was composed as suggest.
	budget := e.skillBudget
	learned := &learnedSkills{
		deps: appskill.CandidateDeps{
			Catalog:   e.skills,
			Proposals: proposals,
			Ownership: ownership,
			Decisions: decisions,
			Generate:  e.conversations.Utility,
			Budget:    budget,
			Log:       log,
			Folders:   places,
			Writer:    e.skillWriter,
		},
		convs:    e.convs,
		projects: e.projectRepo,
		ev:       ev,
		fallback: fallback,
		log:      log,
	}
	// One pass is in flight per room: a second finish of this room waits its
	// turn. Two rooms finishing together both run, and that cross-room race
	// is accepted — the store still refuses a second pending row of one name.
	pass := &skillPass{run: learned.pass}
	return skillSurface{
		Page: httpskill.Deps{
			Catalog:   e.skills,
			Ownership: ownership,
			Proposals: proposals,
			Decisions: decisions,
			Writer:    e.skillWriter,
			Folders:   places,
			Budget:    budget,
			Log:       e.logger,
		},
		Hook:  pass.hook,
		Edits: ev,
	}
}

// skillPass serializes learned.pass per conversation. The lock lives here,
// not in the pass, so a test can watch the queue without a model call.
type skillPass struct {
	mu    sync.Mutex
	rooms map[string]*sync.Mutex
	run   func(convID, kind string, origin valueobject.TurnOrigin)
}

func (p *skillPass) lock(id string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rooms == nil {
		p.rooms = map[string]*sync.Mutex{}
	}
	m := p.rooms[id]
	if m == nil {
		m = &sync.Mutex{}
		p.rooms[id] = m
	}
	return m
}

func (p *skillPass) hook(convID, kind string, origin valueobject.TurnOrigin, _ bool, err error) {
	if p == nil || p.run == nil || err != nil || convID == "" {
		return
	}
	go p.one(convID, kind, origin)
}

func (p *skillPass) one(convID, kind string, origin valueobject.TurnOrigin) {
	// One pass is in flight per room. A second finish of this room waits.
	// Two rooms finishing together both run; that cross-room race is accepted.
	p.lock(convID).Lock()
	defer p.lock(convID).Unlock()
	p.run(convID, kind, origin)
}

type learnedSkills struct {
	deps     appskill.CandidateDeps
	convs    drepo.Conversation
	projects drepo.Project
	ev       *appskill.Evidence
	fallback string
	log      *slog.Logger
}

func (s *learnedSkills) pass(convID, kind string, origin valueobject.TurnOrigin) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conv, err := s.convs.Load(ctx, convID)
	if err != nil {
		s.log.Warn("skill_candidate_failed", "conv", convID, "err", redact.String(err.Error()))
		return
	}
	msgs := slices.Clone(conv.Messages())
	w, ok := appskill.TurnWindow(msgs)
	rewrote := false
	if ok && kind == "edit" && s.ev != nil {
		_, rewrote = s.ev.Take(convID, w.AnchorID)
	}
	if !ok || !appskill.TurnEligible(w, origin) {
		return
	}
	appskill.Candidate(ctx, s.deps, appskill.CandidateInput{
		ConvID:    convID,
		Workspace: skillWorkspace(ctx, conv, s.projects, s.fallback),
		Messages:  msgs,
		Rewrote:   rewrote,
		Now:       time.Now(),
	})
}

// skillWorkspace is the folder the pass reads as focus: the project's primary
// folder while the room is in a project, otherwise the room's own workspace,
// otherwise the environment fallback. A project that cannot be read falls
// through; the pass still runs against the room's own folder.
func skillWorkspace(ctx context.Context, conv *aggregate.Conversation, projects interface {
	Get(context.Context, string) (*aggregate.Project, error)
}, fallback string) string {
	if conv != nil && conv.ProjectID() != "" && projects != nil {
		if p, err := projects.Get(ctx, conv.ProjectID()); err == nil && len(p.Folders()) > 0 {
			return p.Primary()
		}
	}
	if conv != nil {
		if dir := strings.TrimSpace(conv.Workspace()); dir != "" {
			return dir
		}
	}
	return fallback
}
