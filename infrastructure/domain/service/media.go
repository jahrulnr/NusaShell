package service

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
)

// Media is one payload crossing a media-model port: the bytes and their
// MIME type. Ports carry bytes because generation endpoints answer with
// them; persistence turns a payload into a stored attachment reference.
type Media struct {
	MediaType string
	Data      []byte
}

// ImageRequest asks for images from a prompt. References turn
// text-to-image into image-to-image: the model edits or draws from them.
type ImageRequest struct {
	Prompt      string
	References  []Media
	AspectRatio string // "16:9", "1:1", …; "" = the provider's default
	Size        string // "1024x1024" or a provider tier such as "2K"; "" = default
	Count       int    // images wanted; 0 means one
}

// SpeechRequest asks for Text read aloud.
type SpeechRequest struct {
	Text         string
	Voice        string   // provider voice id; "" = the provider's default
	Instructions string   // delivery guidance (tone, pace) for models that take it
	Speed        *float64 // nil = the provider's default
}

// TranscriptionRequest asks for the words spoken in Audio.
type TranscriptionRequest struct {
	Audio    Media
	Language string // ISO-639-1 hint; "" = detect
	Prompt   string // vocabulary or context hint
}

// VideoRequest asks for one video. FirstFrame and LastFrame pin the
// opening and closing images (image-to-video); References carry further
// images or a soundtrack (audio-to-video) for models that take them.
type VideoRequest struct {
	Prompt        string
	FirstFrame    *Media
	LastFrame     *Media
	References    []Media
	Duration      int    // seconds; 0 = the model's default
	AspectRatio   string // "16:9", "9:16", …; "" = default
	Resolution    string // "720p", "1080p", …; "" = default
	GenerateAudio *bool  // nil = the model's default
}

// VideoProgress reports a long generation's state while it runs — a
// live preview, never the result.
type VideoProgress func(status string)

// ImageModelService, SpeechModelService, TranscriptionModelService, and
// VideoModelService are one-attempt translators for generation surfaces,
// the media siblings of TextModelService: endpoint and credentials come
// from prov per call, and which provider and model to use is the
// application's decision. Failures return apperror.IAppError with a
// frontend-safe Message.
type ImageModelService interface {
	GenerateImage(ctx context.Context, prov *aggregate.Provider, model entity.Model, req ImageRequest) ([]Media, error)
}

type SpeechModelService interface {
	Speak(ctx context.Context, prov *aggregate.Provider, model entity.Model, req SpeechRequest) (Media, error)
}

type TranscriptionModelService interface {
	Transcribe(ctx context.Context, prov *aggregate.Provider, model entity.Model, req TranscriptionRequest) (string, error)
}

// VideoModelService.GenerateVideo submits a job and waits for it: video
// surfaces are asynchronous, and the wait ends with the finished bytes,
// a failure, or ctx. progress may be nil.
type VideoModelService interface {
	GenerateVideo(ctx context.Context, prov *aggregate.Provider, model entity.Model, req VideoRequest, progress VideoProgress) (Media, error)
}

// MediaModels runs media requests on the models the owner assigned to
// each media role, so a caller (a tool) asks for an image without
// choosing a provider. An unassigned role answers an error saying so.
type MediaModels interface {
	GenerateImage(ctx context.Context, req ImageRequest) ([]Media, error)
	Speak(ctx context.Context, req SpeechRequest) (Media, error)
	Transcribe(ctx context.Context, req TranscriptionRequest) (string, error)
	GenerateVideo(ctx context.Context, req VideoRequest, progress VideoProgress) (Media, error)
}

// EmbedPurpose says what a vector is for; retrieval models embed a query
// and the documents it searches differently.
type EmbedPurpose string

const (
	EmbedQuery    EmbedPurpose = "query"
	EmbedDocument EmbedPurpose = "document"
)

// EmbeddingModelService turns texts into vectors, one per text in order.
type EmbeddingModelService interface {
	Embed(ctx context.Context, prov *aggregate.Provider, model entity.Model, texts []string, purpose EmbedPurpose) ([][]float32, error)
}
