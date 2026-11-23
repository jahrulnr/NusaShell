package appmedia

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type providers struct {
	drepo.Provider
	prov *aggregate.Provider
}

func (p providers) Load(_ context.Context, id string) (*aggregate.Provider, error) {
	if p.prov == nil || id != p.prov.ID() {
		return nil, apperror.New(404, "provider not found", nil)
	}
	return p.prov, nil
}

type images struct {
	got   entity.Model
	calls int
}

func (i *images) GenerateImage(_ context.Context, _ *aggregate.Provider, m entity.Model, _ dservice.ImageRequest) ([]dservice.Media, error) {
	i.got, i.calls = m, i.calls+1
	return []dservice.Media{{MediaType: "image/png", Data: []byte("png")}}, nil
}

type videos struct{ calls int }

func (v *videos) GenerateVideo(context.Context, *aggregate.Provider, entity.Model, dservice.VideoRequest, dservice.VideoProgress) (dservice.Media, error) {
	v.calls++
	return dservice.Media{MediaType: "video/mp4"}, nil
}

func catalog(t *testing.T) *aggregate.Provider {
	t.Helper()
	p, err := aggregate.NewProvider("or", "", "https://openrouter.ai/api/v1", enum.StyleResponses)
	if err != nil {
		t.Fatal(err)
	}
	textOnly, _ := entity.NewModel("recraft")
	textOnly.SetModelType(enum.ModelTypeImage)
	textOnly.SetInputModalities([]enum.Modality{enum.ModalityText})
	edit, _ := entity.NewModel("nano")
	edit.SetModelType(enum.ModelTypeImage)
	chat, _ := entity.NewModel("chat")
	chat.SetModelType(enum.ModelTypeText)
	veo, _ := entity.NewModel("veo")
	veo.SetModelType(enum.ModelTypeVideo)
	veo.SetInputModalities([]enum.Modality{enum.ModalityText, enum.ModalityImage})
	p.SetModels([]entity.Model{textOnly, edit, chat, veo})
	return &p
}

func TestImageRoleResolution(t *testing.T) {
	var roles Roles
	img := &images{}
	app := New(providers{prov: catalog(t)}, func() (Roles, error) { return roles, nil }, Adapters{Image: img})
	ctx := context.Background()
	ref := dservice.Media{MediaType: "image/png", Data: []byte("x")}
	status := func(err error) int {
		var ae *apperror.AppError
		if !errors.As(err, &ae) {
			return 0
		}
		return ae.StatusCode()
	}

	if _, err := app.GenerateImage(ctx, dservice.ImageRequest{Prompt: "p"}); status(err) != 409 || !strings.Contains(err.Error(), "Settings → Models") {
		t.Fatalf("unassigned: %v", err)
	}
	roles.Image = Assignment{ProviderID: "or", ModelID: "gone"}
	if _, err := app.GenerateImage(ctx, dservice.ImageRequest{Prompt: "p"}); status(err) != 404 {
		t.Fatalf("missing model: %v", err)
	}
	roles.Image = Assignment{ProviderID: "or", ModelID: "chat"}
	if _, err := app.GenerateImage(ctx, dservice.ImageRequest{Prompt: "p"}); status(err) != 400 {
		t.Fatalf("chat model: %v", err)
	}
	roles.Image = Assignment{ProviderID: "or", ModelID: "recraft"}
	if _, err := app.GenerateImage(ctx, dservice.ImageRequest{Prompt: "p", References: []dservice.Media{ref}}); status(err) != 400 {
		t.Fatalf("text-only model took references: %v", err)
	}
	if img.calls != 0 {
		t.Fatalf("adapter called %d times before a valid request", img.calls)
	}
	if _, err := app.GenerateImage(ctx, dservice.ImageRequest{Prompt: "p"}); err != nil || img.got.ID() != "recraft" {
		t.Fatalf("t2i: %v, model %q", err, img.got.ID())
	}
	roles.Image = Assignment{ProviderID: "or", ModelID: "nano"}
	if _, err := app.GenerateImage(ctx, dservice.ImageRequest{Prompt: "p", References: []dservice.Media{ref}}); err != nil || img.got.ID() != "nano" {
		t.Fatalf("i2i: %v", err)
	}
	if _, err := app.Speak(ctx, dservice.SpeechRequest{Text: "x"}); status(err) != 501 {
		t.Fatalf("missing adapter: %v", err)
	}
}

func TestVideoChecksInputKinds(t *testing.T) {
	vid := &videos{}
	app := New(providers{prov: catalog(t)}, func() (Roles, error) {
		return Roles{Video: Assignment{ProviderID: "or", ModelID: "veo"}}, nil
	}, Adapters{Video: vid})
	frame := &dservice.Media{MediaType: "image/png"}
	if _, err := app.GenerateVideo(context.Background(), dservice.VideoRequest{Prompt: "p", FirstFrame: frame}, nil); err != nil {
		t.Fatal(err)
	}
	_, err := app.GenerateVideo(context.Background(), dservice.VideoRequest{Prompt: "p", References: []dservice.Media{{MediaType: "audio/mpeg"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "audio") || vid.calls != 1 {
		t.Fatalf("audio into an image-only model: %v (calls %d)", err, vid.calls)
	}
}
