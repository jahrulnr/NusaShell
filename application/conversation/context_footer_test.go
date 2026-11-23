package appconversation

import (
	"strings"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repoprovider "github.com/jahrulnr/nusashell/infrastructure/repository/provider"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
)

func TestContextFooterStats(t *testing.T) {
	prov, err := aggregate.NewProvider("gw1", "stub", "http://x", enum.StyleCompletion)
	if err != nil {
		t.Fatal(err)
	}
	prov.SetApiKey("sk")
	m, _ := entity.NewModel("m1")
	m.SetContextWindow(1_000_000)
	prov.SetModels([]entity.Model{m})
	secrets, err := reposecret.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secrets.Close() })
	providers := repoprovider.NewProviderRepository(t.TempDir(), secrets)
	if err := providers.Save(t.Context(), &prov); err != nil {
		t.Fatal(err)
	}
	convs := repoconversation.NewConversationRepository(t.TempDir())
	conv, err := aggregate.NewConversation("room")
	if err != nil {
		t.Fatal(err)
	}
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	user, _ := entity.NewMessage(enum.RoleUser, strings.Repeat("x", 800_000))
	if _, err := convs.AppendMessage(t.Context(), "room", user); err != nil {
		t.Fatal(err)
	}

	app := New(convs, providers, map[enum.Environment]appjob.EnvironmentDriver{
		enum.EnvironmentLocal: appjob.EnvironmentBundle{},
	}, config.NewConfig(), nil, "", nil, nil)
	stats, err := app.ContextFooterStats(t.Context(), "room", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// The footer's total is the input allowance: the window less the
	// reply's headroom, which a 1M window keeps too.
	if stats.Total != 1_000_000-entity.DefaultUnknownMaxOutputTokens {
		t.Fatalf("total = %d", stats.Total)
	}
	if stats.Estimate < 190_000 {
		t.Fatalf("estimate = %d, want ~200k from chars/4", stats.Estimate)
	}
	if err := conv.SetEnvironment(enum.EnvironmentSSH); err != nil {
		t.Fatal(err)
	}
	if err := convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ContextFooterStats(t.Context(), "room", "", ""); err == nil {
		t.Fatal("unavailable driver produced a misleading token estimate")
	}
}

func TestContextFooterStatsUnknownWindow(t *testing.T) {
	prov, err := aggregate.NewProvider("gw1", "stub", "http://x", enum.StyleCompletion)
	if err != nil {
		t.Fatal(err)
	}
	prov.SetApiKey("sk")
	m, _ := entity.NewModel("m1")
	prov.SetModels([]entity.Model{m})
	secrets, err := reposecret.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { secrets.Close() })
	providers := repoprovider.NewProviderRepository(t.TempDir(), secrets)
	if err := providers.Save(t.Context(), &prov); err != nil {
		t.Fatal(err)
	}
	convs := repoconversation.NewConversationRepository(t.TempDir())
	conv, err := aggregate.NewConversation("room")
	if err != nil {
		t.Fatal(err)
	}
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}

	app := New(convs, providers, map[enum.Environment]appjob.EnvironmentDriver{
		enum.EnvironmentLocal: appjob.EnvironmentBundle{},
	}, config.NewConfig(), nil, "", nil, nil)
	stats, err := app.ContextFooterStats(t.Context(), "room", "", "")
	if err != nil {
		t.Fatal(err)
	}
	const wantTotal = entity.DefaultUnknownContextWindow - entity.DefaultUnknownMaxOutputTokens
	if stats.Total != wantTotal {
		t.Fatalf("total = %d, want %d", stats.Total, wantTotal)
	}
}
