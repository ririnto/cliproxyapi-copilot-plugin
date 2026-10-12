package translate

import (
	"bytes"
	"fmt"
	"strings"

	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type responsesClaudeReasoningPayload struct {
	marker       string
	encrypted    string
	thinking     string
	redacted     bool
	redactedData string
}

type responsesClaudeToolPayload struct {
	marker     string
	claudeID   string
	callID     string
	outputSeen bool
}

func responsesRequestToClaude(model string, body []byte, stream bool) ([]byte, error) {
	input := gjson.GetBytes(body, "input")
	temporary := body
	reasoningRestore := make(map[string]responsesClaudeReasoningPayload)
	toolRestore := make(map[string]responsesClaudeToolPayload)
	toolCallsByCallID := make(map[string]responsesClaudeToolPayload)
	toolCallsByClaudeID := make(map[string]struct{})
	webSearchRestore := make(map[string]gjson.Result)
	if input.IsArray() {
		for index, item := range input.Array() {
			switch item.Get("type").String() {
			case "web_search_call":
				webSearchRestore[strings.TrimPrefix(item.Get("id").String(), "ws_")] = item
			case "reasoning":
				encrypted := item.Get("encrypted_content")
				if encrypted.Type != gjson.String || encrypted.String() == "" {
					continue
				}
				payload := responsesClaudeReasoningPayload{encrypted: encrypted.String()}
				if strings.HasPrefix(payload.encrypted, redactedThinkingPrefix) {
					payload.redacted = true
					payload.redactedData = strings.TrimPrefix(payload.encrypted, redactedThinkingPrefix)
				} else {
					itemObject, err := decodeObject([]byte(item.Raw))
					if err != nil {
						return nil, fmt.Errorf("decode Responses reasoning item for Claude translation")
					}
					payload.thinking = responsesReasoningText(itemObject)
				}
				payload.marker = temporaryResponsesMarker(body, "reasoning", index)
				reasoningRestore[payload.marker] = payload
				updated, err := sjson.SetBytes(temporary, fmt.Sprintf("input.%d.encrypted_content", index), redactedThinkingPrefix+payload.marker)
				if err != nil {
					return nil, fmt.Errorf("prepare Responses reasoning for Claude translation")
				}
				temporary = updated
			case "function_call", "custom_tool_call":
				payload, ok, err := responsesClaudeToolPayloadFromItem(item)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				if _, exists := toolCallsByCallID[payload.callID]; exists {
					return nil, fmt.Errorf("Responses tool call identity is ambiguous for Claude translation")
				}
				if _, exists := toolCallsByClaudeID[payload.claudeID]; exists {
					return nil, fmt.Errorf("Responses tool call identity is ambiguous for Claude translation")
				}
				payload.marker = temporaryResponsesMarker(body, "tool", index)
				toolCallsByCallID[payload.callID] = payload
				toolCallsByClaudeID[payload.claudeID] = struct{}{}
				toolRestore[payload.marker] = payload
				updated, err := sjson.SetBytes(temporary, fmt.Sprintf("input.%d.call_id", index), payload.marker)
				if err != nil {
					return nil, fmt.Errorf("prepare Responses tool identity for Claude translation")
				}
				temporary = updated
			}
		}
		for index, item := range input.Array() {
			typ := item.Get("type").String()
			if typ != "function_call_output" && typ != "custom_tool_call_output" {
				continue
			}
			callID := responsesRequestItemCallID(item)
			payload, exists := toolCallsByCallID[callID]
			if !exists {
				continue
			}
			if payload.outputSeen {
				return nil, fmt.Errorf("Responses tool output identity is ambiguous for Claude translation")
			}
			payload.outputSeen = true
			toolCallsByCallID[callID] = payload
			toolRestore[payload.marker] = payload
			updated, err := sjson.SetBytes(temporary, fmt.Sprintf("input.%d.call_id", index), payload.marker)
			if err != nil {
				return nil, fmt.Errorf("prepare Responses tool output identity for Claude translation")
			}
			temporary = updated
		}
	}
	temporary, err := normalizeResponsesWebSearchPreviewForClaude(temporary)
	if err != nil {
		return nil, err
	}
	out, err := translateSDKRequest(sdktranslator.FormatOpenAIResponse, sdktranslator.FormatClaude, model, temporary, stream)
	if err != nil {
		return nil, err
	}
	if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() {
		if temperature.Type != gjson.Number || temperature.Float() < 0 || temperature.Float() > 1 {
			return nil, fmt.Errorf("Responses temperature cannot be represented by Claude Messages")
		}
		out, err = sjson.SetRawBytes(out, "temperature", []byte(temperature.Raw))
		if err != nil {
			return nil, fmt.Errorf("preserve Responses temperature for Claude Messages: %w", err)
		}
	}
	toolChoice := gjson.GetBytes(body, "tool_choice")
	if toolChoice.Type == gjson.String && toolChoice.String() == "required" {
		translatedTools := gjson.GetBytes(out, "tools")
		if !translatedTools.IsArray() || len(translatedTools.Array()) == 0 || gjson.GetBytes(out, "tool_choice.type").String() != "any" {
			return nil, fmt.Errorf("Responses tool_choice required cannot be preserved by Claude Messages without translated tools")
		}
	}
	if len(reasoningRestore) == 0 && len(toolRestore) == 0 && len(webSearchRestore) == 0 {
		return out, nil
	}
	reasoningCounts := make(map[string]int, len(reasoningRestore))
	toolUseCounts := make(map[string]int, len(toolRestore))
	toolResultCounts := make(map[string]int, len(toolRestore))
	webSearchUseCounts := make(map[string]int, len(webSearchRestore))
	webSearchResultCounts := make(map[string]int, len(webSearchRestore))
	for messageIndex, message := range gjson.GetBytes(out, "messages").Array() {
		for blockIndex, block := range message.Get("content").Array() {
			var restored []byte
			var err error
			switch block.Get("type").String() {
			case "server_tool_use":
				id := block.Get("id").String()
				item, ok := webSearchRestore[id]
				if !ok || block.Get("name").String() != "web_search" {
					return nil, fmt.Errorf("Responses web search call could not be restored exactly in Claude request")
				}
				restored, err = sjson.SetBytes([]byte(block.Raw), "input.query", item.Get("action.query").String())
				webSearchUseCounts[id]++
			case "web_search_tool_result":
				id := block.Get("tool_use_id").String()
				item, ok := webSearchRestore[id]
				if !ok {
					return nil, fmt.Errorf("Responses web search result could not be restored exactly in Claude request")
				}
				restored, err = sjson.SetRawBytes([]byte(block.Raw), "content", []byte(item.Get("results").Raw))
				webSearchResultCounts[id]++
			case "redacted_thinking":
				payload, ok := reasoningRestore[block.Get("data").String()]
				if !ok {
					continue
				}
				restored = []byte(block.Raw)
				if payload.redacted {
					restored, err = sjson.SetBytes(restored, "data", payload.redactedData)
				} else {
					restored, err = sjson.SetBytes(restored, "type", "thinking")
					if err == nil {
						restored, err = sjson.SetBytes(restored, "thinking", payload.thinking)
					}
					if err == nil {
						restored, err = sjson.SetBytes(restored, "signature", payload.encrypted)
					}
					if err == nil {
						restored, err = sjson.DeleteBytes(restored, "data")
					}
				}
				if err != nil {
					return nil, fmt.Errorf("restore Responses reasoning in Claude request")
				}
				reasoningCounts[payload.marker]++
			case "tool_use":
				payload, ok := toolRestore[block.Get("id").String()]
				if !ok {
					continue
				}
				restored, err = sjson.SetBytes([]byte(block.Raw), "id", payload.claudeID)
				if err == nil {
					toolUseCounts[payload.marker]++
				}
			case "tool_result":
				payload, ok := toolRestore[block.Get("tool_use_id").String()]
				if !ok {
					continue
				}
				restored, err = sjson.SetBytes([]byte(block.Raw), "tool_use_id", payload.claudeID)
				if err == nil {
					toolResultCounts[payload.marker]++
				}
			default:
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("restore Responses identity in Claude request")
			}
			updated, err := sjson.SetRawBytes(out, fmt.Sprintf("messages.%d.content.%d", messageIndex, blockIndex), restored)
			if err != nil {
				return nil, fmt.Errorf("restore Responses identity in Claude request")
			}
			out = updated
		}
	}
	for marker := range reasoningRestore {
		if reasoningCounts[marker] != 1 {
			return nil, fmt.Errorf("Responses reasoning could not be restored exactly in Claude request")
		}
	}
	for marker, payload := range toolRestore {
		if toolUseCounts[marker] != 1 || (payload.outputSeen && toolResultCounts[marker] != 1) || toolResultCounts[marker] > 1 {
			return nil, fmt.Errorf("Responses tool identity could not be restored exactly in Claude request")
		}
	}
	for id := range webSearchRestore {
		if webSearchUseCounts[id] != 1 || webSearchResultCounts[id] != 1 {
			return nil, fmt.Errorf("Responses web search pair could not be restored exactly in Claude request")
		}
	}
	return out, nil
}

func normalizeResponsesWebSearchPreviewForClaude(body []byte) ([]byte, error) {
	root := gjson.ParseBytes(body)
	updated, err := normalizeResponsesWebSearchPreviewToolArray(body, root.Get("tools"), "tools")
	if err != nil {
		return nil, err
	}
	for index, item := range root.Get("input").Array() {
		if item.Get("type").String() != "additional_tools" {
			continue
		}
		updated, err = normalizeResponsesWebSearchPreviewToolArray(updated, item.Get("tools"), fmt.Sprintf("input.%d.tools", index))
		if err != nil {
			return nil, err
		}
	}
	return updated, nil
}

func normalizeResponsesWebSearchPreviewToolArray(body []byte, tools gjson.Result, path string) ([]byte, error) {
	for index, tool := range tools.Array() {
		if tool.Get("type").String() != "web_search_preview" {
			continue
		}
		updated, err := sjson.SetBytes(body, fmt.Sprintf("%s.%d.type", path, index), "web_search")
		if err != nil {
			return nil, fmt.Errorf("normalize Responses web_search_preview tool for Claude Messages")
		}
		body = updated
	}
	return body, nil
}

func temporaryResponsesMarker(body []byte, kind string, index int) string {
	marker := fmt.Sprintf("cpa_bridge_%s_%d", kind, index)
	for bytes.Contains(body, []byte(marker)) {
		marker += "_"
	}
	return marker
}

func responsesClaudeToolPayloadFromItem(item gjson.Result) (responsesClaudeToolPayload, bool, error) {
	callID := responsesRequestItemCallID(item)
	itemID := item.Get("id").String()
	if callID == "" && itemID == "" {
		return responsesClaudeToolPayload{}, false, nil
	}
	if callID == "" {
		callID = itemID
	}
	carrierCallID := ""
	for _, field := range []string{"call_id", "tool_call_id", "callId"} {
		value := item.Get(field)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			carrierCallID = value.String()
			break
		}
	}
	claudeID := claudeToolIDFromResponses(map[string]any{"id": itemID, "call_id": carrierCallID})
	if carrierCallID != "" {
		decodedCallID, encoded, err := unwrapClaudeToolIDCarrier(itemID, carrierCallID)
		if err != nil {
			return responsesClaudeToolPayload{}, false, fmt.Errorf("Responses tool call has an invalid Claude identity")
		}
		if encoded {
			claudeID = decodedCallID
		} else {
			itemPrefix := ""
			switch item.Get("type").String() {
			case "function_call":
				itemPrefix = "fc_"
			case "custom_tool_call":
				itemPrefix = "ctc_"
			}
			if itemPrefix != "" && itemID == itemPrefix+carrierCallID {
				claudeID = carrierCallID
			}
		}
	}
	return responsesClaudeToolPayload{claudeID: claudeID, callID: callID}, true, nil
}

func unwrapClaudeToolIDCarrier(itemID, value string) (string, bool, error) {
	if !strings.HasPrefix(value, bridgeToolIDPrefix) {
		return value, false, nil
	}
	carrierItemID, callID, wrapped, err := parseClaudeToolID(value)
	if err != nil || !wrapped || callID == "" || strings.HasPrefix(callID, bridgeToolIDPrefix) || (carrierItemID != "" && carrierItemID != itemID) {
		return "", false, errInvalidClaudeToolIDCarrier
	}
	return callID, true, nil
}

func responsesRequestItemCallID(item gjson.Result) string {
	for _, field := range []string{"call_id", "tool_call_id", "callId"} {
		value := item.Get(field)
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			return strings.TrimSpace(value.String())
		}
	}
	id := strings.TrimSpace(item.Get("id").String())
	if strings.HasPrefix(id, "fco_") {
		return ""
	}
	return id
}
