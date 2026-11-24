package appconversation

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// Settings → Models → "New threads" reaches every submitted job: the broker
// reloads the file on each post, so a thread started without a picker — a
// spawn, an automation run — starts on the value the owner set, and a change
// applies without a restart.
func TestNewThreadsEffortRidesTheConfigReload(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	writeModelsThreadsEffort(t, cfgPath, "max")
	app := fixtureWithConfig(t, cfgPath)

	if _, err := app.Send(t.Context(), "effort-room", "gw1", "m1", "ping", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := app.depsSnapshot().ThreadsEffort; got != enum.EffortMax {
		t.Fatalf("thread effort after the first post = %q, want the file's max", got)
	}

	// The next post picks up a changed setting.
	writeModelsThreadsEffort(t, cfgPath, "low")
	if _, err := app.Send(t.Context(), "effort-room", "gw1", "m1", "ping", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := app.depsSnapshot().ThreadsEffort; got != enum.EffortLow {
		t.Fatalf("thread effort after the reload = %q, want the file's low", got)
	}
}

// writeModelsThreadsEffort rewrites the config file with one thread effort,
// keeping the conversation values the fixture's other reads expect. The
// schema only accepts an effort beside the provider and model it applies to.
func writeModelsThreadsEffort(t *testing.T, path, effort string) {
	t.Helper()
	body := fmt.Sprintf(`{"conversation":{"mailbox_cap":128,"max_turn_steps":512},"models":{"threads":{"provider":"gw1","model":"m1","effort":%q}}}`, effort)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
