package application

import (
	"sort"
	"strings"

	"nusashell/application/service/toolpresentation"
	"nusashell/contracts"
)

// handleToolContracts returns the frontend contract for the same interactive
// roster used to build provider tool definitions. Keeping both paths on the
// ToolFactory prevents a tool from being executable but invisible to the UI,
// or advertised to the UI without being executable.
func (a *App) handleToolContracts(req contracts.ToolContractsRequest) (any, *contracts.RPCError) {
	workspace := strings.TrimSpace(req.Workspace)
	defs := toolFactoryFor(a).Get(AgentConversation, workspace)
	tools := make([]contracts.ToolContractDTO, 0, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.Name) == "" {
			continue
		}
		tools = append(tools, buildToolContract(def))
	}
	return contracts.ToolContractsResult{
		Version: contracts.ToolContractVersion,
		Tools:   tools,
	}, nil
}

func buildToolContract(def ToolDef) contracts.ToolContractDTO {
	variants := toolContractVariants(def.Name)
	formats := toolContractFormats(def.Name)
	inputSchema := def.InputSchema
	if inputSchema == nil {
		inputSchema = map[string]any{}
	}
	attachmentTypes := toolContractAttachmentTypes(def.Name)
	resultFields := toolContractResultFields(formats)
	if len(attachmentTypes) > 0 {
		resultFields = addToolContractField(resultFields, "attachments")
	}
	return contracts.ToolContractDTO{
		Name:        def.Name,
		Description: def.Description,
		ID:          toolpresentation.ToolContractID(def.Name),
		Version:     contracts.ToolContractVersion,
		CSSClass:    toolpresentation.ToolContractCSSClass(def.Name),
		InputSchema: inputSchema,
		Presentation: contracts.ToolContractPresentationDTO{
			Variants:        variants,
			Formats:         formats,
			RequestFields:   toolContractRequestFields(inputSchema),
			ResultFields:    resultFields,
			AttachmentTypes: attachmentTypes,
		},
	}
}

func toolContractVariants(name string) []string {
	switch name {
	case "skill", "memory", "docs", "memory_project", "automation":
		return []string{"collection", "document", "status"}
	case "automation_schedule":
		return []string{"status"}
	default:
		return []string{toolpresentation.ToolPresentationVariant(name, "")}
	}
}

func toolContractFormats(name string) []string {
	switch name {
	case "skill", "memory", "docs", "memory_project", "automation":
		return []string{"list", "document", "status"}
	case "automation_schedule":
		return []string{"status"}
	default:
		return []string{toolpresentation.ToolPresentationFormat(name, "")}
	}
}

func toolContractRequestFields(schema map[string]any) []string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	fields := make([]string, 0, len(properties))
	for name := range properties {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

func toolContractResultFields(formats []string) []string {
	fields := []string{"format"}
	seen := map[string]bool{"format": true}
	add := func(values ...string) {
		for _, value := range values {
			if !seen[value] {
				seen[value] = true
				fields = append(fields, value)
			}
		}
	}
	for _, format := range formats {
		switch format {
		case "list":
			add("summary", "meta", "items", "text")
		case "code":
			add("summary", "meta", "text", "language")
		case "document":
			add("summary", "meta", "text")
		case "media":
			add("summary", "meta", "text", "attachments")
		case "terminal":
			add("summary", "text")
		default:
			add("summary", "meta")
		}
	}
	return fields
}

func addToolContractField(fields []string, field string) []string {
	for _, value := range fields {
		if value == field {
			return fields
		}
	}
	return append(fields, field)
}

func toolContractAttachmentTypes(name string) []string {
	switch name {
	case "read_media":
		return []string{"image", "audio", "video", "file"}
	case "generate_media":
		return []string{"image", "audio", "video"}
	case "generate_image":
		return []string{"image"}
	case "generate_speech":
		return []string{"audio"}
	case "generate_video":
		return []string{"video"}
	case "show":
		return []string{"image", "audio", "video"}
	default:
		return nil
	}
}
