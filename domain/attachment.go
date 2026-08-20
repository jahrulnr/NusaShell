package domain

import "strings"

// OmittedPlaceholderFor builds a placeholder that tells the model to call
// the matching read_* tool with the absolute file path to access the
// attachment. Only absolute paths are shown — relative paths are rejected
// to avoid ambiguity between the model's working directory and the actual
// file location. kind is "image" | "audio" | "video"; toolName is the
// matching read_* tool.
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
	return "[" + kind + " content omitted — this model does not support " + kind + " input. " +
		capKind + " file(s): " + list + ". " +
		"Call the " + toolName + " tool with file_path set to one of the absolute paths above to load the " + kind + " into your context.]"
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
