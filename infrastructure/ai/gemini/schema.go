package gemini

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// sanitizeSchema converts a JSON Schema into the OpenAPI-subset Schema object
// the Gemini API accepts. Ported from litellm's _build_vertex_schema and its
// helpers (litellm/llms/vertex_ai/common_utils.py).
//
// Applied in upstream order:
//  1. $defs are unwrapped and $ref nodes inlined.
//  2. anyOf branches holding null are removed; the remaining branches become
//     nullable.
//  3. Types are upper-cased and JSON-Schema type arrays become anyOf branches.
//  4. Empty enum values are dropped; enums survive only on string nodes.
//  5. Arrays always carry an items schema.
//  6. Nodes without a type become OBJECT; object nodes without properties drop
//     the empty properties/required pair.
//  7. Only fields Gemini documents survive — additionalProperties, $schema,
//     strict, and other JSON-Schema keywords are dropped.
//  8. Structured-output schemas gain propertyOrdering so field order is stable.
//
// Go maps have no insertion order, so propertyOrdering is alphabetical, which
// matches the order Gemini would use without an explicit ordering.
func sanitizeSchema(raw json.RawMessage, propertyOrdering bool) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("schema must be a JSON object: %w", err)
	}
	defs, _ := root["$defs"].(map[string]any)
	node := convertSchemaNode(root, defs, propertyOrdering, 0)
	data, err := json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return data, nil
}

const maxSchemaDepth = 32

// allowedSchemaFields lists the fields of Gemini's Schema object. Everything
// else in the input is dropped before the request is sent.
var allowedSchemaFields = map[string]bool{
	"type":             true,
	"format":           true,
	"title":            true,
	"description":      true,
	"nullable":         true,
	"default":          true,
	"items":            true,
	"minItems":         true,
	"maxItems":         true,
	"enum":             true,
	"properties":       true,
	"propertyOrdering": true,
	"required":         true,
	"minProperties":    true,
	"maxProperties":    true,
	"minimum":          true,
	"maximum":          true,
	"minLength":        true,
	"maxLength":        true,
	"pattern":          true,
	"example":          true,
	"anyOf":            true,
}

// typeSpecificFields move into the matching anyOf branch when a JSON-Schema
// type array is expanded.
var typeSpecificFields = []string{
	"properties", "required", "items", "minItems", "maxItems", "minProperties", "maxProperties",
}

func convertSchemaNode(node map[string]any, defs map[string]any, propertyOrdering bool, depth int) map[string]any {
	if depth > maxSchemaDepth {
		return map[string]any{"type": "OBJECT"}
	}
	node = resolveReference(node, defs)

	if rawType, ok := node["type"]; ok {
		node = expandTypeArray(node, rawType)
	}
	if rawType, ok := node["type"].(string); ok {
		node["type"] = strings.ToUpper(strings.TrimSpace(rawType))
	}

	if properties, ok := node["properties"].(map[string]any); ok {
		converted := make(map[string]any, len(properties))
		for name, child := range properties {
			childNode, ok := child.(map[string]any)
			if !ok {
				continue
			}
			converted[name] = convertSchemaNode(childNode, defs, propertyOrdering, depth+1)
		}
		node["properties"] = converted
	}
	if items, ok := node["items"].(map[string]any); ok {
		node["items"] = convertSchemaNode(items, defs, propertyOrdering, depth+1)
	} else if items != nil {
		delete(node, "items")
	}

	if rawBranches, ok := node["anyOf"].([]any); ok {
		node = convertAnyOfNode(node, rawBranches, defs, propertyOrdering, depth)
	}

	normalizeEnum(node)
	if format, ok := node["format"].(string); ok && format != "enum" && format != "date-time" {
		delete(node, "format")
	}
	if required, ok := node["required"].([]any); ok {
		names := make([]string, 0, len(required))
		for _, value := range required {
			if name, ok := value.(string); ok && name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			delete(node, "required")
		} else {
			node["required"] = names
		}
	}

	ensureObjectType(node)
	if typ, _ := node["type"].(string); typ == "ARRAY" {
		if _, ok := node["items"]; !ok {
			node["items"] = map[string]any{"type": "OBJECT"}
		}
	}
	if typ, _ := node["type"].(string); typ == "OBJECT" {
		properties, _ := node["properties"].(map[string]any)
		switch {
		case len(properties) == 0:
			// Gemini rejects empty properties for object types.
			delete(node, "properties")
			delete(node, "required")
		case propertyOrdering:
			if _, ok := node["propertyOrdering"]; !ok {
				node["propertyOrdering"] = sortedKeys(properties)
			}
		}
	}

	filtered := make(map[string]any, len(node))
	for key, value := range node {
		if allowedSchemaFields[key] {
			filtered[key] = value
		}
	}
	return filtered
}

// resolveReference inlines a local $ref from $defs, keeping sibling keywords
// (description and friends) of the referencing node.
func resolveReference(node map[string]any, defs map[string]any) map[string]any {
	ref, _ := node["$ref"].(string)
	if ref == "" {
		return node
	}
	name := ref[strings.LastIndex(ref, "/")+1:]
	target, ok := defs[name].(map[string]any)
	if !ok {
		return node
	}
	merged := make(map[string]any, len(target)+len(node))
	for key, value := range target {
		merged[key] = value
	}
	for key, value := range node {
		if key == "$ref" {
			continue
		}
		merged[key] = value
	}
	return merged
}

// expandTypeArray turns a JSON-Schema type array (["string","null"]) into the
// anyOf form Gemini accepts, moving object/array keywords into their branch.
func expandTypeArray(node map[string]any, rawType any) map[string]any {
	types, ok := rawType.([]any)
	if !ok || len(types) == 0 {
		return node
	}
	branches := make([]any, 0, len(types))
	containsNull := false
	for _, value := range types {
		name, _ := value.(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if name == "null" {
			containsNull = true
			continue
		}
		branch := map[string]any{"type": name}
		for _, field := range typeSpecificFields {
			if fieldValue, ok := node[field]; ok {
				branch[field] = fieldValue
			}
		}
		branches = append(branches, branch)
	}
	delete(node, "type")
	for _, field := range typeSpecificFields {
		delete(node, field)
	}
	if len(branches) == 0 {
		return node
	}
	node["anyOf"] = branches
	if containsNull {
		markNullable(branches)
	}
	return node
}

// convertAnyOfNode drops null branches, normalizes the survivors, and returns
// an anyOf-only node: Gemini rejects anyOf mixed with sibling keywords.
func convertAnyOfNode(node map[string]any, rawBranches []any, defs map[string]any, propertyOrdering bool, depth int) map[string]any {
	branches := make([]any, 0, len(rawBranches))
	containsNull := false
	for _, value := range rawBranches {
		branch, ok := value.(map[string]any)
		if !ok {
			continue
		}
		branchType, _ := branch["type"].(string)
		if strings.EqualFold(strings.TrimSpace(branchType), "null") {
			containsNull = true
			continue
		}
		if len(branch) == 0 {
			branch["type"] = "object"
		}
		branches = append(branches, convertSchemaNode(branch, defs, propertyOrdering, depth+1))
	}
	if len(branches) == 0 {
		return map[string]any{"type": "OBJECT"}
	}
	if containsNull {
		markNullable(branches)
	}
	title, _ := node["title"].(string)
	description, _ := node["description"].(string)
	for _, value := range branches {
		branch, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if title != "" {
			branch["title"] = title
		}
		if description != "" {
			branch["description"] = description
		}
	}
	return map[string]any{"anyOf": branches}
}

func markNullable(branches []any) {
	for _, value := range branches {
		if branch, ok := value.(map[string]any); ok {
			branch["nullable"] = true
		}
	}
}

// normalizeEnum keeps enums only on string nodes and removes empty values,
// which Gemini rejects.
func normalizeEnum(node map[string]any) {
	rawEnum, ok := node["enum"].([]any)
	if !ok {
		return
	}
	if !isStringTyped(node) {
		delete(node, "enum")
		return
	}
	values := make([]any, 0, len(rawEnum))
	for _, value := range rawEnum {
		name, ok := value.(string)
		if !ok || name == "" {
			continue
		}
		values = append(values, name)
	}
	if len(values) == 0 {
		delete(node, "enum")
		return
	}
	node["enum"] = values
}

func isStringTyped(node map[string]any) bool {
	if typ, ok := node["type"].(string); ok {
		return typ == "STRING"
	}
	branches, ok := node["anyOf"].([]any)
	if !ok {
		return false
	}
	for _, value := range branches {
		branch, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if typ, ok := branch["type"].(string); ok && typ == "STRING" {
			return true
		}
	}
	return false
}

// ensureObjectType applies litellm's add_object_type: every node must declare a
// type (Gemini function parameters must be objects).
func ensureObjectType(node map[string]any) {
	if _, ok := node["type"]; ok {
		return
	}
	if _, ok := node["anyOf"]; ok {
		return
	}
	node["type"] = "OBJECT"
}

func sortedKeys(properties map[string]any) []string {
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
