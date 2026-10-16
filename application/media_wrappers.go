package application

import (
	"nusashell/application/media"
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

func (a *App) executeGenerateMedia(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	return a.mediaService().ExecuteGenerateMedia(mediaCall(run), toolCall, settings)
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
