package translate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrProviderStateConversion identifies state without a compatible target representation.
var ErrProviderStateConversion = errors.New("unsupported provider state conversion")

// ProviderStateConversionError identifies the protocol edge and unrepresented state field.
type ProviderStateConversionError struct {
	Source      string
	Destination string
	Kind        string
	Field       string
}

// Error describes an incompatible state shape without including its contents.
func (e *ProviderStateConversionError) Error() string {
	return fmt.Sprintf("cannot preserve %s state field %s from %s to %s", e.Kind, e.Field, e.Source, e.Destination)
}

// Unwrap preserves the common provider state conversion identity.
func (e *ProviderStateConversionError) Unwrap() error {
	return ErrProviderStateConversion
}

func validateRequestState(source, destination string, body []byte) error {
	root, err := decodeObject(body)
	if err != nil {
		return err
	}
	reject := func(kind, field string) error {
		return &ProviderStateConversionError{Source: source, Destination: destination, Kind: kind, Field: field}
	}
	for _, raw := range arrayValue(root["tools"]) {
		kind := stringValue(objectValue(raw)["type"])
		if source == "claude" && kind != "" && kind != "custom" || source == "openai-response" && kind != "function" {
			return reject(kind, "tools.type")
		}
	}
	if source == "claude" {
		for _, field := range []string{"container", "mcp_servers", "context_management"} {
			if value, exists := root[field]; exists && value != nil {
				return reject("request", field)
			}
		}
		for _, raw := range arrayValue(root["messages"]) {
			if err := validateClaudeState(source, destination, objectValue(raw)["content"], true); err != nil {
				return err
			}
		}
	} else if source == "openai-response" {
		for _, field := range []string{"previous_response_id", "conversation"} {
			if value, exists := root[field]; exists && value != nil && value != "" {
				return reject("request", field)
			}
		}
		for _, raw := range arrayValue(root["input"]) {
			if err := validateResponsesState(source, destination, objectValue(raw), true); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateClaudeState(source, destination string, content any, request bool) error {
	for _, part := range claudeContentParts(content) {
		kind := stringValue(part["type"])
		field := "type"
		reject := false
		switch kind {
		case "text", "image", "document":
			reject = len(arrayValue(part["citations"])) > 0
			field = "citations"
		case "thinking":
			signature := rawStringValue(part["signature"])
			reject = signature != "" && !(request && destination == "openai" && strings.HasPrefix(signature, copilotReplayPrefix))
			field = "signature"
		case "tool_use":
			_, reject = part["caller"]
			field = "caller"
		case "tool_result":
			if part["is_error"] == true {
				reject, field = true, "is_error"
			} else if err := validateClaudeState(source, destination, part["content"], request); err != nil {
				return err
			}
		default:
			reject = true
		}
		if reject {
			return &ProviderStateConversionError{Source: source, Destination: destination, Kind: kind, Field: field}
		}
	}
	return nil
}

func validateResponsesState(source, destination string, item map[string]any, request bool) error {
	kind := stringValue(item["type"])
	if kind == "" && stringValue(item["role"]) != "" {
		kind = "message"
	}
	field := "type"
	reject := false
	switch kind {
	case "message":
		for _, raw := range arrayValue(item["content"]) {
			part := objectValue(raw)
			if len(arrayValue(part["annotations"])) > 0 {
				return &ProviderStateConversionError{Source: source, Destination: destination, Kind: stringValue(part["type"]), Field: "annotations"}
			}
		}
	case "function_call":
	case "function_call_output":
		reject = !request
	case "reasoning":
		signature := rawStringValue(item["encrypted_content"])
		reject = signature != "" && !(request && destination == "openai" && strings.HasPrefix(signature, copilotReplayPrefix))
		field = "encrypted_content"
	default:
		reject = true
	}
	if reject {
		return &ProviderStateConversionError{Source: source, Destination: destination, Kind: kind, Field: field}
	}
	return nil
}

func validateResponseState(source, destination string, body []byte) error {
	if source != "claude" && source != "openai-response" {
		return nil
	}
	root, err := decodeObject(body)
	if err != nil {
		return err
	}
	if source == "claude" {
		if stringValue(root["stop_reason"]) == "pause_turn" {
			return &ProviderStateConversionError{Source: source, Destination: destination, Kind: "response", Field: "stop_reason"}
		}
		if root["container"] != nil {
			return &ProviderStateConversionError{Source: source, Destination: destination, Kind: "response", Field: "container"}
		}
		return validateClaudeState(source, destination, root["content"], false)
	}
	for _, raw := range arrayValue(root["output"]) {
		if err := validateResponsesState(source, destination, objectValue(raw), false); err != nil {
			return err
		}
	}
	return nil
}

func validateStreamState(source, destination string, frame []byte) error {
	if source != "claude" && source != "openai-response" {
		return nil
	}
	_, data, done, err := parseSSEFrame(frame)
	if err != nil || done || len(data) == 0 {
		return err
	}
	root, err := decodeObject(data)
	if err != nil {
		return err
	}
	if source == "claude" {
		if block, ok := root["content_block"]; ok {
			return validateClaudeState(source, destination, []any{block}, false)
		}
		delta := objectValue(root["delta"])
		if stringValue(delta["type"]) == "signature_delta" {
			return &ProviderStateConversionError{Source: source, Destination: destination, Kind: "thinking", Field: "signature"}
		}
		if stringValue(delta["type"]) == "citations_delta" {
			return &ProviderStateConversionError{Source: source, Destination: destination, Kind: "text", Field: "citations"}
		}
		if stringValue(delta["stop_reason"]) == "pause_turn" {
			return &ProviderStateConversionError{Source: source, Destination: destination, Kind: "response", Field: "stop_reason"}
		}
		if message, ok := root["message"]; ok {
			body, err := json.Marshal(message)
			if err != nil {
				return err
			}
			return validateResponseState(source, destination, body)
		}
	} else if item, ok := root["item"]; ok {
		return validateResponsesState(source, destination, objectValue(item), false)
	}
	if _, ok := root["annotation"]; ok && source == "openai-response" {
		return &ProviderStateConversionError{Source: source, Destination: destination, Kind: "output_text", Field: "annotations"}
	}
	if part, ok := root["part"]; ok && source == "openai-response" {
		return validateResponsesState(source, destination, map[string]any{"type": "message", "content": []any{part}}, false)
	}
	if response, ok := root["response"]; ok {
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		return validateResponseState(source, destination, body)
	}
	return nil
}
