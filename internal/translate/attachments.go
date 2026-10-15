package translate

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ErrAttachmentConversion identifies attachment state that the selected target cannot preserve.
var ErrAttachmentConversion = errors.New("unsupported attachment conversion")

// AttachmentConversionError identifies the protocol edge and attachment field that would be lost.
type AttachmentConversionError struct {
	// Source identifies the client protocol.
	Source string
	// Destination identifies the provider protocol.
	Destination string
	// Kind identifies the attachment content type.
	Kind string
	// Field identifies the unrepresented attachment field.
	Field string
}

// Error describes unsupported attachment semantics without including attachment data.
func (e *AttachmentConversionError) Error() string {
	return fmt.Sprintf("cannot preserve %s attachment field %s from %s to %s", e.Kind, e.Field, e.Source, e.Destination)
}

// Unwrap preserves the common attachment conversion error identity.
func (e *AttachmentConversionError) Unwrap() error {
	return ErrAttachmentConversion
}

func validateRequestAttachments(source, destination string, body []byte) error {
	root, err := decodeObject(body)
	if err != nil {
		return err
	}
	if source == "claude" {
		for _, raw := range arrayValue(root["messages"]) {
			if err := validateClaudeAttachments(destination, objectValue(raw)["content"]); err != nil {
				return err
			}
		}
	} else if source == "openai-response" {
		for _, raw := range arrayValue(root["input"]) {
			item := objectValue(raw)
			content := item["content"]
			if kind := stringValue(item["type"]); kind == "function_call_output" || kind == "custom_tool_call_output" {
				content = item["output"]
				if kind == "custom_tool_call_output" {
					for _, raw := range arrayValue(content) {
						partKind := stringValue(objectValue(raw)["type"])
						if slices.Contains([]string{"input_image", "input_file", "audio", "input_audio", "video", "input_video"}, partKind) {
							return &AttachmentConversionError{Source: source, Destination: destination, Kind: partKind, Field: "output"}
						}
					}
				}
			}
			if err := validateResponsesAttachments(destination, content); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateClaudeAttachments(destination string, content any) error {
	for _, part := range claudeContentParts(content) {
		kind := stringValue(part["type"])
		err := &AttachmentConversionError{Source: "claude", Destination: destination, Kind: kind}
		if kind == "tool_result" {
			if err := validateClaudeAttachments(destination, part["content"]); err != nil {
				return err
			}
		} else if kind == "image" || kind == "document" {
			if kind == "document" && destination == "openai" {
				err.Field = "type"
				return err
			}
			allowed := []string{"type", "source"}
			if kind == "document" {
				allowed = append(allowed, "title")
			}
			if err := attachmentFields(part, allowed, err); err != nil {
				return err
			}
			if title, exists := part["title"]; exists {
				if _, ok := title.(string); !ok {
					err.Field = "title"
					return err
				}
			}
			source := objectValue(part["source"])
			var fields []string
			switch stringValue(source["type"]) {
			case "base64":
				fields = []string{"type", "media_type", "data"}
			case "url":
				fields = []string{"type", "url"}
			case "text":
				if kind == "document" {
					fields = []string{"type", "data"}
				}
			}
			if len(fields) == 0 {
				err.Field = "source.type"
				return err
			}
			if err := attachmentFields(source, fields, err); err != nil {
				return err
			}
			for _, field := range fields[1:] {
				if value, ok := source[field].(string); !ok || strings.TrimSpace(value) == "" {
					err.Field = "source." + field
					return err
				}
			}
		} else if kind == "audio" || kind == "input_audio" || kind == "video" || kind == "input_video" {
			err.Field = "type"
			return err
		}
	}
	return nil
}

func validateResponsesAttachments(destination string, content any) error {
	for _, raw := range arrayValue(content) {
		part := objectValue(raw)
		kind := stringValue(part["type"])
		err := &AttachmentConversionError{Source: "openai-response", Destination: destination, Kind: kind}
		if kind == "input_file" {
			if destination == "openai" {
				err.Field = "type"
				return err
			}
			if err := attachmentFields(part, []string{"type", "file_data"}, err); err != nil {
				return err
			}
			data := rawStringValue(part["file_data"])
			if !strings.HasPrefix(data, "data:application/pdf;base64,") {
				err.Field = "file_data"
				return err
			}
		} else if kind == "input_image" {
			allowed := []string{"type", "image_url"}
			if destination == "openai" {
				allowed = append(allowed, "detail")
				if detail, exists := part["detail"]; exists && !slices.Contains([]string{"auto", "low", "high"}, rawStringValue(detail)) {
					err.Field = "detail"
					return err
				}
			}
			if err := attachmentFields(part, allowed, err); err != nil {
				return err
			}
			if value, ok := part["image_url"].(string); !ok || strings.TrimSpace(value) == "" {
				err.Field = "image_url"
				return err
			}
		} else if kind == "input_audio" || kind == "audio" || kind == "video" || kind == "input_video" {
			err.Field = "type"
			return err
		}
	}
	return nil
}

func attachmentFields(value map[string]any, allowed []string, err *AttachmentConversionError) error {
	for _, field := range slices.Sorted(maps.Keys(value)) {
		if !slices.Contains(allowed, field) {
			err.Field = field
			return err
		}
	}
	return nil
}
