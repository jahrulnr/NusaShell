// Package appmedia runs media requests — images, speech, transcripts,
// video — on the models the owner assigned to each media role in
// Settings → Models. It implements dservice.MediaModels for the tools.
package appmedia

import (
	"context"
	"fmt"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Assignment names the provider and model a role uses; empty is off.
type Assignment struct {
	ProviderID string
	ModelID    string
}

// Roles is the owner's media assignment at one moment.
type Roles struct {
	Embedding     Assignment
	Image         Assignment
	Video         Assignment
	Speech        Assignment
	Transcription Assignment
}

// Adapters are the generation families; a nil one means the build
// cannot do that job.
type Adapters struct {
	Embedding     dservice.EmbeddingModelService
	Image         dservice.ImageModelService
	Speech        dservice.SpeechModelService
	Transcription dservice.TranscriptionModelService
	Video         dservice.VideoModelService
}

type App struct {
	providers drepo.Provider
	roles     func() (Roles, error)
	adapters  Adapters
}

// New builds the use case. roles is read on every request, so a change
// in Settings applies at once.
func New(providers drepo.Provider, roles func() (Roles, error), adapters Adapters) *App {
	return &App{providers: providers, roles: roles, adapters: adapters}
}

var _ dservice.MediaModels = (*App)(nil)

// Identity names the model the transcription role points at, "provider/model"
// — the identity a caller keys its transcript cache by, so switching models is
// a miss rather than a stale transcript from the previous reader. Empty when
// the role is off; the settings read's error when it cannot be read.
func (a *App) Identity(_ context.Context) (string, error) {
	roles, err := a.roles()
	if err != nil {
		return "", err
	}
	if roles.Transcription.ProviderID == "" || roles.Transcription.ModelID == "" {
		return "", nil
	}
	return roles.Transcription.ProviderID + "/" + roles.Transcription.ModelID, nil
}

func (a *App) GenerateImage(ctx context.Context, req dservice.ImageRequest) ([]dservice.Media, error) {
	if a.adapters.Image == nil {
		return nil, apperror.New(501, "image generation is not available in this build", nil)
	}
	prov, model, err := a.resolve(ctx, "image", enum.ModelTypeImage, func(r Roles) Assignment { return r.Image })
	if err != nil {
		return nil, err
	}
	if len(req.References) > 0 && !model.AcceptsInput(enum.ModalityImage) {
		return nil, apperror.New(400, fmt.Sprintf("the image model %s makes images from text only; it takes no reference images", model.ID()), nil)
	}
	return a.adapters.Image.GenerateImage(ctx, prov, model, req)
}

func (a *App) Speak(ctx context.Context, req dservice.SpeechRequest) (dservice.Media, error) {
	if a.adapters.Speech == nil {
		return dservice.Media{}, apperror.New(501, "speech is not available in this build", nil)
	}
	prov, model, err := a.resolve(ctx, "speech", enum.ModelTypeSpeech, func(r Roles) Assignment { return r.Speech })
	if err != nil {
		return dservice.Media{}, err
	}
	return a.adapters.Speech.Speak(ctx, prov, model, req)
}

func (a *App) Transcribe(ctx context.Context, req dservice.TranscriptionRequest) (string, error) {
	if a.adapters.Transcription == nil {
		return "", apperror.New(501, "transcription is not available in this build", nil)
	}
	prov, model, err := a.resolve(ctx, "speech-to-text", enum.ModelTypeTranscription, func(r Roles) Assignment { return r.Transcription })
	if err != nil {
		return "", err
	}
	return a.adapters.Transcription.Transcribe(ctx, prov, model, req)
}

func (a *App) GenerateVideo(ctx context.Context, req dservice.VideoRequest, progress dservice.VideoProgress) (dservice.Media, error) {
	if a.adapters.Video == nil {
		return dservice.Media{}, apperror.New(501, "video generation is not available in this build", nil)
	}
	prov, model, err := a.resolve(ctx, "video", enum.ModelTypeVideo, func(r Roles) Assignment { return r.Video })
	if err != nil {
		return dservice.Media{}, err
	}
	inputs := append([]dservice.Media{}, req.References...)
	for _, f := range []*dservice.Media{req.FirstFrame, req.LastFrame} {
		if f != nil {
			inputs = append(inputs, *f)
		}
	}
	for _, m := range inputs {
		mod := modality(m.MediaType)
		if !model.AcceptsInput(mod) {
			return dservice.Media{}, apperror.New(400, fmt.Sprintf("the video model %s takes no %s input", model.ID(), mod), nil)
		}
	}
	return a.adapters.Video.GenerateVideo(ctx, prov, model, req, progress)
}

// Embed turns texts into vectors on the embedding role's model. model
// names that model ("provider/model") so vectors made by another can be
// told apart. With no embedding model set it answers nothing and no
// error: callers without vectors fall back to text search.
func (a *App) Embed(ctx context.Context, texts []string, purpose dservice.EmbedPurpose) (vectors [][]float32, model string, err error) {
	roles, err := a.roles()
	if err != nil {
		return nil, "", apperror.New(500, "could not read the model settings", err)
	}
	if a.adapters.Embedding == nil || roles.Embedding.ProviderID == "" || roles.Embedding.ModelID == "" {
		return nil, "", nil
	}
	prov, m, err := a.resolve(ctx, "embedding", enum.ModelTypeEmbedding, func(r Roles) Assignment { return r.Embedding })
	if err != nil {
		return nil, "", err
	}
	vectors, err = a.adapters.Embedding.Embed(ctx, prov, m, texts, purpose)
	if err != nil {
		return nil, "", err
	}
	return vectors, prov.ID() + "/" + m.ID(), nil
}

// resolve reads the role's assignment and loads what it names: the
// provider with its credentials, and the catalog entry, which must be a
// model of the role's type (entity.Model.ServesType decides).
func (a *App) resolve(ctx context.Context, role string, kind enum.ModelType, pick func(Roles) Assignment) (*aggregate.Provider, entity.Model, error) {
	roles, err := a.roles()
	if err != nil {
		return nil, entity.Model{}, apperror.New(500, "could not read the model settings", err)
	}
	as := pick(roles)
	if as.ProviderID == "" || as.ModelID == "" {
		return nil, entity.Model{}, apperror.New(409, fmt.Sprintf("no %s model is set — the owner chooses one in Settings → Models", role), nil)
	}
	prov, err := a.providers.Load(ctx, as.ProviderID)
	if err != nil {
		return nil, entity.Model{}, err
	}
	model, ok := prov.Model(as.ModelID)
	if !ok {
		return nil, entity.Model{}, apperror.New(404, fmt.Sprintf("the %s model %s is no longer in provider %s's catalog — choose another in Settings → Models", role, as.ModelID, as.ProviderID), nil)
	}
	if !model.ServesType(kind) {
		return nil, entity.Model{}, apperror.New(400, fmt.Sprintf("the %s role names %s, which is not a %s model — choose another in Settings → Models", role, as.ModelID, kind), nil)
	}
	return prov, model, nil
}

// modality names an input's kind by the same rule attachments use.
func modality(mediaType string) enum.Modality {
	a, err := valueobject.NewAttachment("data:", mediaType)
	if err != nil {
		return enum.ModalityFile
	}
	return a.Modality()
}
