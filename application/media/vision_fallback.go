package media

import (
	"context"
	"fmt"

	"nusashell/domain"
	"nusashell/resources"
)

var defaultDescribeImagePrompt = resources.DescribeImagePrompt()

// DescribeImagesWithFallback converts image attachments into text descriptions
// using the configured vision fallback model. Original images are preserved;
// descriptions are appended as text attachments.
func (s *Service) DescribeImagesWithFallback(ctx context.Context, settings domain.Settings, atts []domain.Attachment) []domain.Attachment {
	if len(atts) == 0 {
		return atts
	}
	if settings.VisionProviderID == "" || settings.VisionModelID == "" {
		return atts
	}
	imageIdxs := domain.UndescribedMediaIndexes(atts, "image", domain.MediaDescPrefixVision)
	if len(imageIdxs) == 0 {
		return atts
	}

	provider, _, ok := s.resolve(settings.VisionProviderID)
	if !ok {
		s.write("warn", "vision", "vision fallback provider %q not found or disabled; skipping image description", s.name(settings.VisionProviderID))
		return atts
	}

	out := make([]domain.Attachment, 0, len(atts)+len(imageIdxs))
	out = append(out, atts...)
	maxOut := domain.ResolveMaxOutput(provider, settings.VisionModelID, settings)
	for _, idx := range imageIdxs {
		img := atts[idx]
		description, err := s.describe(ctx, settings.VisionProviderID, settings.VisionModelID, defaultDescribeImagePrompt, img, maxOut)
		if err != nil {
			s.write("warn", "vision", "image description failed for %q: %v", img.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      domain.MediaDescPrefixVision + img.Name,
			MediaType: "text/plain",
			Content:   fmt.Sprintf("[Image description for %s]\n%s", img.Name, description),
		})
	}
	return out
}
