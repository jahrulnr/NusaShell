// Package gemini implements the application.AIProvider and
// application.ImageGenerator ports for Google's Gemini generateContent API.
//
// Gemini uses a single multimodal endpoint for both chat and image
// generation: the model returns image bytes as inlineData parts in its
// response. There is no separate /images/generations path.
//
// API reference: https://ai.google.dev/api/?lang=rest
// Auth: x-goog-api-key header (API key from Google AI Studio).
package gemini

// Wire types for the Google Gemini REST API.
//
// Reference: https://ai.google.dev/api/generate-content

// Content is a single message in a Gemini conversation.
// https://ai.google.dev/api/caching#Content
type Content struct {
	Role  string `json:"role,omitempty"` // "user" or "model"
	Parts []Part `json:"parts"`
}

// Part is a union — exactly one field is set.
// https://ai.google.dev/api/caching#Part
type Part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *Blob             `json:"inlineData,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

// Blob carries inline binary data (images, audio, video, PDF).
// https://ai.google.dev/api/caching#Blob
type Blob struct {
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"` // base64-encoded
}

// FunctionCall is a tool call from the model.
// https://ai.google.dev/api/caching#FunctionCall
type FunctionCall struct {
	ID               string                 `json:"id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Args             map[string]interface{} `json:"args,omitempty"`
	ThoughtSignature string                 `json:"thoughtSignature,omitempty"`
}

// FunctionResponse is the tool result returned to the model.
// https://ai.google.dev/api/caching#FunctionResponse
type FunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Name     string                 `json:"name,omitempty"`
	Response map[string]interface{} `json:"response,omitempty"`
}

// FunctionDeclaration describes a tool the model can call.
// https://ai.google.dev/api/caching#FunctionDeclaration
type FunctionDeclaration struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Parameters  *Schema `json:"parameters,omitempty"`
}

// Tool groups function declarations.
// https://ai.google.dev/api/caching#Tool
type Tool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// Schema is a JSON Schema for structured output / tool parameters.
// https://ai.google.dev/api/caching#Schema
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Description string             `json:"description,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
}

// GenerationConfig controls sampling and output.
// https://ai.google.dev/api/generate-content#GenerationConfig
type GenerationConfig struct {
	MaxOutputTokens    int      `json:"maxOutputTokens,omitempty"`
	Temperature        float64  `json:"temperature,omitempty"`
	TopP               float64  `json:"topP,omitempty"`
	TopK               int      `json:"topK,omitempty"`
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

// generateContentRequest is the request body for :generateContent and
// :streamGenerateContent.
type generateContentRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
}

// generateContentResponse is the non-streaming response.
type generateContentResponse struct {
	Candidates    []ResponseCandidate `json:"candidates"`
	UsageMetadata UsageMetadata       `json:"usageMetadata"`
	Error         *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

// ResponseCandidate is one candidate in the response.
// https://ai.google.dev/api/generate-content#v1beta.Candidate
type ResponseCandidate struct {
	Content      Content `json:"content"`
	FinishReason string  `json:"finishReason,omitempty"`
}

// UsageMetadata reports token usage.
// https://ai.google.dev/api/generate-content#UsageMetadata
type UsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

// streamChunk is one SSE chunk in a streamGenerateContent response.
type streamChunk struct {
	Candidates []struct {
		Content      Content `json:"content"`
		FinishReason string  `json:"finishReason,omitempty"`
	} `json:"candidates"`
	UsageMetadata *UsageMetadata `json:"usageMetadata,omitempty"`
}
