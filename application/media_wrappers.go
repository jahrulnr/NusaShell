package application

import (
	"context"

	"nusashell/application/media"
	"nusashell/contracts"
	"nusashell/domain"
)

const (
	OfflineTTSProviderID  = media.OfflineTTSProviderID
	mediaDescPrefixVision = domain.MediaDescPrefixVision
	mediaDescPrefixAudio  = domain.MediaDescPrefixAudio
	mediaDescPrefixVideo  = domain.MediaDescPrefixVideo
)

type (
	ImageGenerator            = media.ImageGenerator
	ImageGenRequest           = media.ImageGenRequest
	ImageReference            = media.ImageReference
	GeneratedImage            = media.GeneratedImage
	ImageGenResult            = media.ImageGenResult
	ImageGeneratorFactory     = media.ImageGeneratorFactory
	SpeechSynthesizer         = media.SpeechSynthesizer
	TTSRequest                = media.TTSRequest
	TTSResult                 = media.TTSResult
	SpeechSynthesizerFactory  = media.SpeechSynthesizerFactory
	OfflineSynthesizer        = media.OfflineSynthesizer
	TTSInstaller              = media.TTSInstaller
	SpeechTranscriber         = media.SpeechTranscriber
	STTRequest                = media.STTRequest
	SpeechTranscriberFactory  = media.SpeechTranscriberFactory
	OfflineTranscriber        = media.OfflineTranscriber
	OfflineSTTRequest         = media.OfflineSTTRequest
	OfflineTranscriberStatus  = media.OfflineTranscriberStatus
	OfflineTranscriberFactory = media.OfflineTranscriberFactory
	STTInstaller              = media.STTInstaller
	VideoGenerator            = media.VideoGenerator
	VideoGenRequest           = media.VideoGenRequest
	VideoGenResult            = media.VideoGenResult
	VideoGeneratorFactory     = media.VideoGeneratorFactory
)

func mediaCall(run *TurnRun) media.Call {
	if run == nil {
		return media.Call{}
	}
	return media.Call{Ctx: run.Ctx, ConversationID: run.ConversationID, TurnID: run.ID}
}

func mediaCaps(caps ModelCapabilities) media.Caps {
	return media.Caps{Vision: caps.Vision, Audio: caps.Audio, Video: caps.Video, Document: caps.Document}
}

func (a *App) handleTTSSettingsInstallStatus() (any, *contracts.RPCError) {
	return a.mediaService().HandleTTSInstallStatus()
}

func (a *App) handleTTSSettingsInstallStart(req contracts.TTSInstallStartRequest) (any, *contracts.RPCError) {
	return a.mediaService().HandleTTSInstallStart(req)
}

func (a *App) ttsInstallRunning() bool {
	if a.mediaSvc == nil {
		return false
	}
	return a.mediaSvc.TTSInstallRunning()
}

func (a *App) handleSTTSettingsInstallStatus() (any, *contracts.RPCError) {
	return a.mediaService().HandleSTTInstallStatus()
}

func (a *App) handleSTTSettingsInstallStart(req contracts.STTInstallStartRequest) (any, *contracts.RPCError) {
	return a.mediaService().HandleSTTInstallStart(req)
}

func (a *App) handleSTTSettingsInstallCancel() (any, *contracts.RPCError) {
	return a.mediaService().HandleSTTInstallCancel()
}

func (a *App) sttInstallRunning() bool {
	if a.mediaSvc == nil {
		return false
	}
	return a.mediaSvc.STTInstallRunning()
}

func (a *App) executeGenerateMedia(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteGenerateMedia(mediaCall(run), toolCall, settings)
}

func (a *App) executeGenerateImage(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteGenerateImage(mediaCall(run), toolCall, settings)
}

func (a *App) executeGenerateSpeech(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteGenerateSpeech(mediaCall(run), toolCall, settings)
}

func (a *App) executeGenerateVideo(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteGenerateVideo(mediaCall(run), toolCall, settings)
}

func (a *App) executeReadDocument(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteReadDocument(mediaCall(run), toolCall, mediaCaps(caps), settings)
}

func (a *App) executeReadAudio(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteReadAudio(mediaCall(run), toolCall, mediaCaps(caps), settings)
}

func (a *App) executeReadVideo(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteReadVideo(mediaCall(run), toolCall, mediaCaps(caps), settings)
}

func (a *App) executeReadImage(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteReadImage(mediaCall(run), toolCall, mediaCaps(caps), settings)
}

func (a *App) describeImagesWithFallback(ctx context.Context, settings domain.Settings, attachments []domain.Attachment) []domain.Attachment {
	return a.mediaService().DescribeImagesWithFallback(ctx, settings, attachments)
}

func (a *App) describeAudiosWithFallback(ctx context.Context, settings domain.Settings, attachments []domain.Attachment) []domain.Attachment {
	return a.mediaService().DescribeAudiosWithFallback(ctx, settings, attachments)
}

func (a *App) describeVideosWithFallback(ctx context.Context, settings domain.Settings, attachments []domain.Attachment) []domain.Attachment {
	return a.mediaService().DescribeVideosWithFallback(ctx, settings, attachments)
}

func (a *App) saveGeneratedMedia(conversationID, baseName, kind string, data []byte, inline bool) (domain.Attachment, string, error) {
	return a.mediaService().SaveGenerated(conversationID, baseName, kind, data, inline)
}

func (a *App) persistGeneratedImages(conversationID, toolCallID string, result *ImageGenResult) ([]domain.Attachment, []string, error) {
	return a.mediaService().PersistGeneratedImages(conversationID, toolCallID, result)
}

func formatImageGenFailure(err error, kind domain.ProviderKind) string {
	return media.FormatImageGenFailure(err, kind)
}
