package repository

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
)

// Automation stores automations and their run history. A missing id is
// not found, reported with a stable error identity.
//
// Concurrency: every automation row carries a version the store bumps on
// each write. A write built on a stale read fails rather than overwriting,
// reported as aggregate.ErrAutomationChanged or a false flag per
// method. updated_at moves only on owner edits, never on claims or kept
// threads, so an open edit form is not invalidated by a run firing.
//
// At most one run of an automation is running at a time — a scheduled
// firing or a manual one. A firing that finds a run still going is not
// consumed: it fires once after that run ends, the same collapse a missed
// firing gets after downtime.
type Automation interface {
	// Create inserts a new automation; a conflict when the id exists.
	Create(ctx context.Context, a *aggregate.Automation) error
	// Update replaces a stored automation. readAt is the updatedAt the
	// change was based on and the version is the one read with it; when
	// either stored value differs the write is refused with
	// aggregate.ErrAutomationChanged.
	Update(ctx context.Context, a *aggregate.Automation, readAt time.Time) error
	// Get loads one automation.
	Get(ctx context.Context, id string) (*aggregate.Automation, error)
	// List returns every automation, newest first.
	List(ctx context.Context) ([]aggregate.Automation, error)
	// Due returns the enabled automations whose next run is at or before
	// now — expired automations and ones with a run still going are left.
	Due(ctx context.Context, now time.Time) ([]aggregate.Automation, error)
	// Delete removes an automation and its runs; not found when absent.
	Delete(ctx context.Context, id string) error

	// Claim stores a claimed automation (see aggregate.Automation.Claim)
	// and inserts its new run in one transaction. It reports false, with
	// nothing written, when the stored version is not the one the
	// automation was read with — a claim or an edit won — or when a run of
	// the automation is still going. On success the aggregate's version
	// is bumped.
	Claim(ctx context.Context, a *aggregate.Automation, run *entity.AutomationRun) (bool, error)
	// KeepConversation records the thread a "same" automation keeps,
	// touching nothing else. It reports false when the stored version is
	// not version — an edit won, and the next run opens its own thread.
	KeepConversation(ctx context.Context, id, conversationID string, version int64) (bool, error)
	// StartRun inserts run only when the automation has no run still
	// going; it reports false otherwise.
	StartRun(ctx context.Context, run *entity.AutomationRun) (bool, error)
	// SaveRun inserts or updates a run.
	SaveRun(ctx context.Context, run *entity.AutomationRun) error
	// Runs lists an automation's runs, newest first, at most limit.
	Runs(ctx context.Context, automationID string, limit int) ([]entity.AutomationRun, error)
	// PruneRuns drops all but the keep newest runs of an automation.
	PruneRuns(ctx context.Context, automationID string, keep int) error
	// InterruptRuns ends every run still marked running as interrupted at
	// at — called at start, when no run can still be going.
	InterruptRuns(ctx context.Context, at time.Time) error
}
