package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type responseRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions"`
	Input           []json.RawMessage `json:"input"`
	Tools           []functionTool    `json:"tools"`
	PromptCacheKey  string            `json:"prompt_cache_key"`
	MaxOutputTokens int               `json:"max_output_tokens"`
	Store           bool              `json:"store"`
	Include         []string          `json:"include,omitempty"`
}

type functionTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

type responsesAPIResponse struct {
	Status           string            `json:"status"`
	IncompleteDetail *incompleteDetail `json:"incomplete_details"`
	Error            *responseError    `json:"error"`
	Usage            tokenUsage        `json:"usage"`
	Output           []json.RawMessage `json:"output"`
}

type incompleteDetail struct {
	Reason string `json:"reason"`
}

type responseError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type tokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

func (usage *tokenUsage) add(other tokenUsage) {
	usage.InputTokens += other.InputTokens
	usage.OutputTokens += other.OutputTokens
	usage.TotalTokens += other.TotalTokens
}

func buildResponseRequest(model, cacheKey string, history []json.RawMessage) responseRequest {
	return responseRequest{
		Model:           model,
		Instructions:    chatInstructions,
		Input:           history,
		Tools:           []functionTool{getCurrentTimeTool()},
		PromptCacheKey:  cacheKey,
		MaxOutputTokens: 512,
		Store:           false,
		// When store=false, encrypted reasoning content must be included if
		// it will be replayed in a later request. The sample keeps the full
		// response-item history so the tool loop remains self-contained.
		Include: []string{"reasoning.encrypted_content"},
	}
}

func getCurrentTimeTool() functionTool {
	return functionTool{
		Type:        "function",
		Name:        "getCurrentTime",
		Description: "Return the current local time and UTC time. This tool takes no arguments.",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []string{},
			"additionalProperties": false,
		},
		Strict: true,
	}
}

func callResponses(ctx context.Context, client *http.Client, baseURL, apiKey string, request responseRequest) (responsesAPIResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return responsesAPIResponse{}, fmt.Errorf("encode Responses request: %w", err)
	}
	endpoint := responsesEndpoint(baseURL)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return responsesAPIResponse{}, fmt.Errorf("create Responses request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	res, err := client.Do(httpRequest)
	if err != nil {
		return responsesAPIResponse{}, fmt.Errorf("OpenAI request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBodyBytes))
		return responsesAPIResponse{}, fmt.Errorf("OpenAI Responses request failed (%s): %s", res.Status, strings.TrimSpace(string(errorBody)))
	}

	responseBody, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return responsesAPIResponse{}, fmt.Errorf("read OpenAI response: %w", err)
	}
	if len(responseBody) > maxResponseBytes {
		return responsesAPIResponse{}, fmt.Errorf("OpenAI response exceeded %d bytes", maxResponseBytes)
	}

	var response responsesAPIResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return responsesAPIResponse{}, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if response.Error != nil && response.Error.Message != "" {
		return responsesAPIResponse{}, fmt.Errorf("OpenAI Responses error: %s", response.Error.Message)
	}
	if response.Status != "" && response.Status != "completed" {
		reason := "unknown status"
		if response.IncompleteDetail != nil && response.IncompleteDetail.Reason != "" {
			reason = response.IncompleteDetail.Reason
		}
		return responsesAPIResponse{}, fmt.Errorf("OpenAI response status %s: %s", response.Status, reason)
	}
	return response, nil
}

func responsesEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/responses"
	}
	return base + "/v1/responses"
}

type outputHeader struct {
	Type string `json:"type"`
}

type functionCall struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	CallID    string `json:"call_id"`
	Arguments string `json:"arguments"`
}

func responseFunctionCalls(output []json.RawMessage) ([]functionCall, error) {
	var calls []functionCall
	for _, item := range output {
		var header outputHeader
		if err := json.Unmarshal(item, &header); err != nil {
			return nil, fmt.Errorf("decode response output item: %w", err)
		}
		if header.Type != "function_call" {
			continue
		}
		var call functionCall
		if err := json.Unmarshal(item, &call); err != nil {
			return nil, fmt.Errorf("decode function call: %w", err)
		}
		if call.CallID == "" || call.Name == "" {
			return nil, fmt.Errorf("function call is missing call_id or name")
		}
		calls = append(calls, call)
	}
	return calls, nil
}

func executeFunctionCall(call functionCall) (json.RawMessage, error) {
	if call.CallID == "" {
		return nil, fmt.Errorf("function call is missing call_id")
	}
	if call.Name != "getCurrentTime" {
		return makeToolOutput(call.CallID, `{"error":"tool call rejected: function is not allowlisted"}`)
	}

	if err := validateNoArguments(call.Arguments); err != nil {
		return makeToolOutput(call.CallID, `{"error":"tool call rejected: getCurrentTime takes no arguments"}`)
	}

	now := timeNow()
	result, err := json.Marshal(map[string]string{
		"local":    now.local,
		"timezone": now.timezone,
		"utc":      now.utc,
	})
	if err != nil {
		return nil, fmt.Errorf("encode getCurrentTime result: %w", err)
	}
	return makeToolOutput(call.CallID, string(result))
}

func validateNoArguments(arguments string) error {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		arguments = "{}"
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &values); err != nil {
		return err
	}
	if values == nil || len(values) != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	return nil
}

type currentTime struct {
	local    string
	timezone string
	utc      string
}

var timeNow = func() currentTime {
	now := time.Now()
	return currentTime{
		local:    now.Format(time.RFC3339),
		timezone: now.Location().String(),
		utc:      now.UTC().Format(time.RFC3339),
	}
}

func makeToolOutput(callID, output string) (json.RawMessage, error) {
	return json.Marshal(map[string]string{
		"type":    "function_call_output",
		"call_id": callID,
		"output":  output,
	})
}

type outputContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type outputMessage struct {
	Type    string          `json:"type"`
	Content []outputContent `json:"content"`
}

func responseOutputText(output []json.RawMessage) (string, error) {
	var text strings.Builder
	for _, item := range output {
		var message outputMessage
		if err := json.Unmarshal(item, &message); err != nil {
			return "", fmt.Errorf("decode message output: %w", err)
		}
		if message.Type != "message" {
			continue
		}
		for _, part := range message.Content {
			switch part.Type {
			case "output_text":
				text.WriteString(part.Text)
			case "refusal":
				text.WriteString(part.Refusal)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("response completed without usable output text")
	}
	return text.String(), nil
}
