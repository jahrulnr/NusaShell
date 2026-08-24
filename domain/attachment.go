package domain

import "strings"

// OmittedPlaceholderFor builds a placeholder that tells the model to call
// the read_media tool with the absolute file path to access the
// attachment. Only absolute paths are shown — relative paths are rejected
// to avoid ambiguity between the model's working directory and the actual
// file location. kind is "image" | "audio" | "video" | "document";
// toolName is the matching tool (read_media).
func OmittedPlaceholderFor(kind, toolName string, atts []Attachment) string {
	if len(atts) == 0 {
		return "[" + kind + " content omitted — this model does not support " + kind + " input]"
	}
	paths := make([]string, 0, len(atts))
	for _, a := range atts {
		if a.FilePath != "" {
			paths = append(paths, a.FilePath)
		}
	}
	if len(paths) == 0 {
		return "[" + kind + " content omitted — this model does not support " + kind + " input]"
	}
	list := strings.Join(paths, ", ")
	capKind := strings.ToUpper(kind[:1]) + kind[1:]
	i2iHint := ""
	// Images only: even though a non-vision model cannot see the pixels, it
	// can still drive image-to-image generation as a prompt enhancer —
	// generate_image reads reference bytes from disk, so only the path is
	// needed.
	if kind == "image" {
		i2iHint = " To edit it or use it as a reference, pass its absolute path in referenced_image_paths when calling generate_image."
	}
	return "[" + kind + " content omitted — this model does not support " + kind + " input. " +
		capKind + " file(s): " + list + ". " +
		"Call the " + toolName + " tool with file_path set to one of the absolute paths above to load the " + kind + " into your context." + i2iHint + "]"
}

// VisionImagePathNote builds a path note for vision-capable models. Unlike
// OmittedPlaceholderFor — which tells a non-vision model the image was
// stripped and must be loaded via read_media — this note keeps the image
// pixels visible in the message and only surfaces the absolute file path(s)
// so the model can reference them for image-to-image editing via
// generate_image's referenced_image_paths. The wording is deliberately
// distinct ("attached and visible", not "content omitted") so the model does
// not mistake the note for a missing image. Returns "" when no image
// attachment carries a file path.
func VisionImagePathNote(atts []Attachment) string {
	paths := make([]string, 0, len(atts))
	for _, a := range atts {
		if a.Type == "image" && a.FilePath != "" {
			paths = append(paths, a.FilePath)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	list := strings.Join(paths, ", ")
	return "[Image attached and visible in this message: " + list + ". " +
		"To edit it or use it as a reference, pass its absolute path in " +
		"referenced_image_paths when calling generate_image.]"
}

// ContainsVisionImageNote reports whether content already carries the vision
// image path note, so chatMessages does not append it twice.
func ContainsVisionImageNote(content string) bool {
	return strings.Contains(content, "Image attached and visible")
}

// ImageOmittedPlaceholderFor is kept for backward compatibility with tests.

// FolderPlaceholderFor builds a text placeholder that tells the agent the
// absolute path of a dropped folder. The agent can use file tools
// (list_dir, read_file, etc.) to explore the directory. Folder attachments
// carry no bytes — only the path.
func FolderPlaceholderFor(atts []Attachment) string {
	if len(atts) == 0 {
		return ""
	}
	paths := make([]string, 0, len(atts))
	for _, a := range atts {
		if a.FilePath != "" {
			paths = append(paths, a.FilePath)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	if len(paths) == 1 {
		return "[Folder dropped: " + paths[0] + ". Use file tools to list and read its contents.]"
	}
	return "[Folders dropped: " + strings.Join(paths, ", ") + ". Use file tools to list and read their contents.]"
}

// HasImageAttachment reports whether atts contains any image attachment.

// HasAttachmentOfType reports whether atts contains any attachment of typ.
func HasAttachmentOfType(atts []Attachment, typ string) bool {
	for _, a := range atts {
		if a.Type == typ {
			return true
		}
	}
	return false
}

// StripImageAttachments removes image attachments from atts.

// StripAttachmentsByType removes attachments of typ from atts.
func StripAttachmentsByType(atts []Attachment, typ string) []Attachment {
	filtered := make([]Attachment, 0, len(atts))
	for _, a := range atts {
		if a.Type != typ {
			filtered = append(filtered, a)
		}
	}
	return filtered
}

// FilterImageAttachments returns only image attachments from atts.

// FilterAttachmentsByType returns only attachments of typ from atts.
func FilterAttachmentsByType(atts []Attachment, typ string) []Attachment {
	var out []Attachment
	for _, a := range atts {
		if a.Type == typ {
			out = append(out, a)
		}
	}
	return out
}

// ContainsImageOmissionNote reports whether content mentions an image
// omission placeholder.

// ContainsOmissionNote reports whether content mentions an omission
// placeholder for the given kind.
func ContainsOmissionNote(content, kind string) bool {
	return strings.Contains(content, kind+" content omitted")
}
