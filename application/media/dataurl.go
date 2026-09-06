package media

import (
	"encoding/base64"
	"fmt"
	"strings"
)

func decodeAttachmentDataURL(dataURL string) ([]byte, error) {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URL")
	}
	return base64.StdEncoding.DecodeString(data)
}
