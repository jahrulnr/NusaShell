package media

import (
	"context"
	"fmt"
	"sync"

	"nusashell/domain"
)

const maxConcurrentImageGens = 2

var errDescribeUnwired = fmt.Errorf("media describe port is not wired")

// Service owns media generation, ingestion, and TTS/STT install single-flight.
type Service struct {
	imageGen      ImageGeneratorFactory
	prepareCodex  func(conversationID string, provider *domain.Provider, apiKey string) (string, error)
	failoverCodex func(ctx context.Context, conversationID string, provider *domain.Provider, apiKey string, genErr error) (string, bool, error)
	speechSynth   SpeechSynthesizerFactory
	offlineSynth  OfflineSynthesizer
	speechSTT     SpeechTranscriberFactory
	offlineSTT    OfflineTranscriberFactory
	videoGen      VideoGeneratorFactory
	ttsInstaller  TTSInstaller
	sttInstaller  STTInstaller
	attachments   Attachments
	resolveFn     Resolve
	providerName  ProviderName
	describeFn    Describe
	settings      Settings
	log           Logger
	bus           Bus
	goFn          GoFunc
	retryDelay    RetryDelay
	waitRetry     WaitRetry

	imageGenSem chan struct{}

	ttsInstallMu     sync.Mutex
	ttsInstallActive bool

	sttInstallMu     sync.Mutex
	sttInstallActive bool
	sttInstallCancel func()
	sttInstallDoneCh chan struct{}
}

// New builds a media Service from Deps.
func New(d Deps) *Service {
	goFn := d.Go
	if goFn == nil {
		goFn = func(_ string, fn func()) { go fn() }
	}
	return &Service{
		imageGen:      d.ImageGen,
		prepareCodex:  d.PrepareCodexAPIKey,
		failoverCodex: d.FailoverCodexAPIKey,
		speechSynth:   d.SpeechSynth,
		offlineSynth:  d.OfflineSynth,
		speechSTT:     d.SpeechSTT,
		offlineSTT:    d.OfflineSTT,
		videoGen:      d.VideoGen,
		ttsInstaller:  d.TTSInstaller,
		sttInstaller:  d.STTInstaller,
		attachments:   d.Attachments,
		resolveFn:     d.Resolve,
		providerName:  d.ProviderName,
		describeFn:    d.Describe,
		settings:      d.Settings,
		log:           d.Log,
		bus:           d.Bus,
		goFn:          goFn,
		retryDelay:    d.RetryDelay,
		waitRetry:     d.WaitRetry,
		imageGenSem:   make(chan struct{}, maxConcurrentImageGens),
	}
}

func (s *Service) write(level, source, format string, args ...any) {
	if s.log != nil {
		s.log(level, source, format, args...)
	}
}

func (s *Service) emit(event string, payload any) {
	if s.bus != nil {
		s.bus.Emit(event, payload)
	}
}

func (s *Service) resolve(id string) (*domain.Provider, string, bool) {
	if s.resolveFn == nil {
		return nil, "", false
	}
	return s.resolveFn(id)
}

func (s *Service) name(id string) string {
	if s.providerName == nil {
		return id
	}
	return s.providerName(id)
}

func (s *Service) describe(ctx context.Context, providerID, model, prompt string, att domain.Attachment, maxTokens int) (string, error) {
	if s.describeFn == nil {
		return "", errDescribeUnwired
	}
	return s.describeFn(ctx, providerID, model, prompt, att, maxTokens)
}
