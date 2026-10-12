package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const claudeHostedSearchRequest = `{"model":"claude-haiku-5.5","max_tokens":256,"messages":[{"role":"user","content":"Search."},{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_roundtrip_search","name":"web_search","input":{"query":"  exact query\n\t "}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_roundtrip_search","content":[{"type":"web_search_result","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_content":"fixture_opaque_replay_token","page_age":"one day","snippet":"Exact source."}]},{"type":"text","text":"Fixture answer.","citations":[{"type":"web_search_result_location","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_index":"fixture_opaque_citation_1","cited_text":"First excerpt."},{"type":"web_search_result_location","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_index":"fixture_opaque_citation_2","cited_text":"Second excerpt."}]}]},{"role":"user","content":"Continue without another search."}]}`

func TestClaudeHostedSearchRequestHistoryRoundTripPreservesOpaqueState(t *testing.T) {
	resultBodies := []string{
		`[{"type":"web_search_result","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_content":"fixture_opaque_replay_token","page_age":"one day","snippet":"Exact source."}]`,
		`[]`,
		`[{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}]`,
	}
	for index, results := range resultBodies {
		t.Run(fmt.Sprintf("results_%d", index), func(t *testing.T) {
			request, err := sjson.SetRaw(claudeHostedSearchRequest, "messages.1.content.1.content", results)
			if err != nil {
				t.Fatal(err)
			}
			responses, err := RequestForEndpointFrom("claude", "gpt-6-luna", []byte(request), false, EndpointResponses)
			if err != nil {
				t.Fatalf("Claude request to Responses: %v", err)
			}
			search := gjson.GetBytes(responses, "input.1")
			if search.Get("id").String() != "ws_srvtoolu_roundtrip_search" || search.Get("action.query").String() != "  exact query\n\t " {
				t.Fatalf("Responses hosted search changed its identity or exact query: %s", search.Raw)
			}
			requireLiveBodyJSONEqual(t, []byte(search.Get("results").Raw), []byte(results))
			requireLiveBodyJSONEqual(t, []byte(gjson.GetBytes(responses, "input.2.content.0.annotations").Raw), []byte(gjson.Get(request, "messages.1.content.2.citations").Raw))

			messages, err := RequestForEndpointFrom("openai-response", "claude-haiku-5.5", responses, false, EndpointMessages)
			if err != nil {
				t.Fatalf("Responses request history to Claude: %v", err)
			}
			blocks := assistantBlocks(t, messages)
			if len(blocks) != 3 {
				t.Fatalf("restored assistant blocks = %d, want search pair and answer: %s", len(blocks), messages)
			}
			requireLiveBodyJSONEqual(t, []byte(blocks[0].Raw), []byte(`{"type":"server_tool_use","id":"srvtoolu_roundtrip_search","name":"web_search","input":{"query":"  exact query\n\t "}}`))
			requireLiveBodyJSONEqual(t, []byte(blocks[1].Raw), []byte(`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_roundtrip_search","content":`+results+`}`))
			requireLiveBodyJSONEqual(t, []byte(blocks[2].Raw), []byte(gjson.Get(request, "messages.1.content.2").Raw))
		})
	}
}

func TestClaudeHostedSearchJSONResponseRoundTripPreservesOpaqueState(t *testing.T) {
	response := []byte(`{"id":"msg_roundtrip","type":"message","role":"assistant","model":"claude-haiku-5.5","content":[{"type":"server_tool_use","id":"srvtoolu_roundtrip_search","name":"web_search","input":{"query":"  exact query\n\t "}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_roundtrip_search","content":[{"type":"web_search_result","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_content":"fixture_opaque_replay_token","page_age":"one day","snippet":"Exact source."}]},{"type":"text","text":"Fixture answer.","citations":[{"type":"web_search_result_location","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_index":"fixture_opaque_citation_1","cited_text":"First excerpt."},{"type":"web_search_result_location","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_index":"fixture_opaque_citation_2","cited_text":"Second excerpt."}]}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":8}}`)
	responses, err := ResponseFromEndpoint(context.Background(), EndpointMessages, "openai-response", "claude-haiku-5.5", nil, nil, response)
	if err != nil {
		t.Fatalf("Claude JSON response to Responses: %v", err)
	}
	search := gjson.GetBytes(responses, `output.#(type==web_search_call)`)
	if search.Get("id").String() != "ws_srvtoolu_roundtrip_search" || search.Get("action.query").String() != "  exact query\n\t " {
		t.Fatalf("Responses response changed hosted-search identity or query: %s", search.Raw)
	}
	requireLiveBodyJSONEqual(t, []byte(search.Get("results").Raw), []byte(gjson.GetBytes(response, "content.1.content").Raw))
	annotations := gjson.GetBytes(responses, `output.#(type==message).content.0.annotations`)
	requireLiveBodyJSONEqual(t, []byte(annotations.Raw), []byte(gjson.GetBytes(response, "content.2.citations").Raw))

	messages, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "claude-haiku-5.5", nil, nil, responses)
	if err != nil {
		t.Fatalf("Responses JSON response to Claude: %v", err)
	}
	blocks := gjson.GetBytes(messages, "content").Array()
	if len(blocks) != 3 {
		t.Fatalf("restored response blocks = %d, want search pair and cited answer: %s", len(blocks), messages)
	}
	requireLiveBodyJSONEqual(t, []byte(blocks[0].Raw), []byte(gjson.GetBytes(response, "content.0").Raw))
	requireLiveBodyJSONEqual(t, []byte(blocks[1].Raw), []byte(gjson.GetBytes(response, "content.1").Raw))
	requireLiveBodyJSONEqual(t, []byte(blocks[2].Raw), []byte(gjson.GetBytes(response, "content.2").Raw))
}

func TestClaudeHostedSearchJSONResponseKeepsCitationsAcrossSearchOrder(t *testing.T) {
	response := []byte(`{"id":"msg_order","type":"message","role":"assistant","model":"claude-haiku-5.5","content":[{"type":"text","text":"Before.","citations":[{"type":"web_search_result_location","url":"https://example.test/before","encrypted_index":"citation_before","cited_text":"Before."}]},{"type":"server_tool_use","id":"srvtoolu_order","name":"web_search","input":{"query":"  ordered query  "}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_order","content":[]},{"type":"text","text":"After.","citations":[{"type":"web_search_result_location","url":"https://example.test/after","encrypted_index":"citation_after","cited_text":"After."}]}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":4}}`)
	responses, err := ResponseFromEndpoint(context.Background(), EndpointMessages, "openai-response", "claude-haiku-5.5", nil, nil, response)
	if err != nil {
		t.Fatalf("Claude JSON response to Responses: %v", err)
	}
	if gjson.GetBytes(responses, "output.0.type").String() != "message" || gjson.GetBytes(responses, "output.1.type").String() != "web_search_call" || gjson.GetBytes(responses, "output.2.type").String() != "message" {
		t.Fatalf("Responses output order changed around web search: %s", responses)
	}
	messages, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "claude-haiku-5.5", nil, nil, responses)
	if err != nil {
		t.Fatalf("Responses JSON response to Claude: %v", err)
	}
	blocks := gjson.GetBytes(messages, "content").Array()
	if len(blocks) != 4 {
		t.Fatalf("restored block order length = %d: %s", len(blocks), messages)
	}
	for index, sourceIndex := range []int{0, 1, 2, 3} {
		requireLiveBodyJSONEqual(t, []byte(blocks[index].Raw), []byte(gjson.GetBytes(response, fmt.Sprintf("content.%d", sourceIndex)).Raw))
	}
}

func TestClaudeHostedSearchSSEResponseRoundTripPreservesFragments(t *testing.T) {
	query := "  exact query\n\t "
	encodedQuery, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	fragments := [][]byte{encodedQuery[:9], encodedQuery[9:17], encodedQuery[17:]}
	for _, result := range []string{
		`[]`,
		`[{"type":"web_search_tool_result_error","error_code":"rate_limited"}]`,
		`[{"type":"web_search_result","url":"https://example.test/report?source=fixture","title":"Fixture report","encrypted_content":"fixture_opaque_replay_token","page_age":"one day"}]`,
	} {
		t.Run(result, func(t *testing.T) {
			claudeFrames := [][]byte{
				claudeHostedFrame(t, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_stream", "model": "claude-haiku-5.5", "usage": map[string]any{"input_tokens": 12, "output_tokens": 0}}}),
				claudeHostedFrame(t, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "server_tool_use", "id": "srvtoolu_roundtrip_search", "name": "web_search", "input": map[string]any{}}}),
			}
			for _, fragment := range fragments {
				claudeFrames = append(claudeFrames, claudeHostedFrame(t, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(fragment)}}))
			}
			resultBytes, err := json.Marshal(json.RawMessage(result))
			if err != nil {
				t.Fatal(err)
			}
			var resultContent any
			if err := json.Unmarshal(resultBytes, &resultContent); err != nil {
				t.Fatal(err)
			}
			claudeFrames = append(claudeFrames,
				claudeHostedFrame(t, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}),
				claudeHostedFrame(t, "content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "web_search_tool_result", "tool_use_id": "srvtoolu_roundtrip_search", "content": resultContent}}),
				claudeHostedFrame(t, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 1}),
				claudeHostedFrame(t, "content_block_start", map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "text", "text": ""}}),
				claudeHostedFrame(t, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "text_delta", "text": "Fixture "}}),
				claudeHostedFrame(t, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "text_delta", "text": "answer."}}),
				claudeHostedFrame(t, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "citations_delta", "citation": map[string]any{"type": "web_search_result_location", "url": "https://example.test/report?source=fixture", "title": "Fixture report", "encrypted_index": "fixture_opaque_citation", "cited_text": "Fixture answer."}}}),
				claudeHostedFrame(t, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 2}),
				claudeHostedFrame(t, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"input_tokens": 12, "output_tokens": 8}}),
				claudeHostedFrame(t, "message_stop", map[string]any{"type": "message_stop"}),
			)

			var responsesState any
			var responseFrames [][]byte
			for _, frame := range claudeFrames {
				translated, err := StreamFromEndpoint(context.Background(), EndpointMessages, "openai-response", "gpt-6-luna", nil, nil, frame, &responsesState)
				if err != nil {
					t.Fatalf("Claude SSE to Responses: %v", err)
				}
				responseFrames = append(responseFrames, translated...)
			}
			if len(responseFrames) == 0 {
				t.Fatal("Claude SSE produced no Responses events")
			}
			var completed gjson.Result
			for _, frame := range responseFrames {
				_, data, _, err := parseSSEFrame(frame)
				if err != nil {
					t.Fatal(err)
				}
				if gjson.GetBytes(data, "type").String() == "response.completed" {
					completed = gjson.GetBytes(data, "response")
				}
			}
			if !completed.Exists() {
				t.Fatalf("translated Responses SSE has no completed snapshot: %q", responseFrames)
			}
			search := gjson.Get(completed.Raw, `output.#(type==web_search_call)`)
			if search.Get("id").String() != "ws_srvtoolu_roundtrip_search" || search.Get("action.query").String() != query {
				t.Fatalf("Responses SSE changed search identity or fragmented query: %s", search.Raw)
			}
			requireLiveBodyJSONEqual(t, []byte(search.Get("results").Raw), []byte(result))
			citation := gjson.Get(completed.Raw, `output.#(type==message).content.0.annotations.0`)
			if citation.Get("encrypted_index").String() != "fixture_opaque_citation" || citation.Get("cited_text").String() != "Fixture answer." {
				t.Fatalf("Responses SSE changed citation: %s", citation.Raw)
			}

			var claudeState any
			var clientFrames [][]byte
			for _, frame := range responseFrames {
				translated, err := StreamFromEndpoint(context.Background(), EndpointResponses, "claude", "claude-haiku-5.5", nil, nil, frame, &claudeState)
				if err != nil {
					t.Fatalf("Responses SSE to Claude: %v", err)
				}
				clientFrames = append(clientFrames, translated...)
			}
			if len(clientFrames) == 0 {
				t.Fatal("Responses SSE produced no Claude events")
			}
			var output strings.Builder
			var sawSearchUse, sawSearchResult, sawCitation bool
			for _, frame := range clientFrames {
				output.Write(frame)
				_, data, _, err := parseSSEFrame(frame)
				if err != nil || len(data) == 0 {
					continue
				}
				payload, err := decodeObject(data)
				if err != nil {
					t.Fatal(err)
				}
				if stringValue(payload["type"]) == "content_block_start" {
					block := objectValue(payload["content_block"])
					switch stringValue(block["type"]) {
					case "server_tool_use":
						sawSearchUse = true
						if rawStringValue(objectValue(block["input"])["query"]) != query {
							t.Fatalf("Claude SSE changed exact search query: %s", data)
						}
					case "web_search_tool_result":
						sawSearchResult = true
						content, err := json.Marshal(block["content"])
						if err != nil {
							t.Fatal(err)
						}
						requireLiveBodyJSONEqual(t, content, []byte(result))
					}
				}
				if gjson.GetBytes(data, "delta.type").String() == "citations_delta" {
					sawCitation = true
				}
			}
			streamText := output.String()
			if !sawSearchUse || !sawSearchResult || !sawCitation {
				t.Fatalf("Claude SSE changed hosted search or citation fields:\n%s", streamText)
			}
			if strings.Index(streamText, `"type":"server_tool_use"`) > strings.Index(streamText, `"type":"web_search_tool_result"`) || strings.Index(streamText, `"type":"web_search_tool_result"`) > strings.Index(streamText, `"type":"text"`) {
				t.Fatalf("Claude SSE changed hosted-search/text order:\n%s", streamText)
			}
		})
	}
}

func TestClaudeHostedSearchRejectsAmbiguousPairsAndLossyFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "unmatched call", body: `{"type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_one","name":"web_search","input":{"query":"q"}}]}`},
		{name: "unmatched result", body: `{"type":"message","content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_one","content":[]}]}`},
		{name: "nonadjacent pair", body: `{"type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_one","name":"web_search","input":{"query":"q"}},{"type":"text","text":"between"},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_one","content":[]}]}`},
		{name: "duplicate call", body: `{"type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_one","name":"web_search","input":{"query":"q"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_one","content":[]},{"type":"server_tool_use","id":"srvtoolu_one","name":"web_search","input":{"query":"q"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_one","content":[]}]}`},
		{name: "extra search field", body: `{"type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_one","name":"web_search","input":{"query":"q","extra":"loss"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_one","content":[]}]}`},
		{name: "native bash", body: `{"type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_one","name":"bash_code_execution","input":{"command":"pwd"}}]}`},
		{name: "citation extra field", body: `{"type":"message","content":[{"type":"text","text":"cited","citations":[{"type":"web_search_result_location","url":"https://example.test","encrypted_index":"opaque","provider_state":"loss"}]}]}`},
		{name: "citation without replay token", body: `{"type":"message","content":[{"type":"text","text":"cited","citations":[{"type":"web_search_result_location","url":"https://example.test"}]}]}`},
		{name: "result without replay token", body: `{"type":"message","content":[{"type":"server_tool_use","id":"srvtoolu_one","name":"web_search","input":{"query":"q"}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_one","content":[{"type":"web_search_result","url":"https://example.test"}]}]}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if out, err := ResponseFromEndpoint(context.Background(), EndpointMessages, "openai-response", "claude-haiku-5.5", nil, nil, []byte(test.body)); err == nil || len(out) != 0 {
				t.Fatalf("lossy Claude response accepted: %s, error=%v", out, err)
			}
		})
	}
}

func TestResponsesHostedSearchOutputRejectsNativeAndIncompleteState(t *testing.T) {
	bodies := []string{
		`{"status":"completed","output":[{"type":"web_search_call","id":"ws_native_gpt_search","status":"completed","action":{"type":"search","query":"q"},"results":[]}]}`,
		`{"status":"completed","output":[{"type":"web_search_call","id":"ws_srvtoolu_incomplete","status":"in_progress","action":{"type":"search","query":"q"}}]}`,
		`{"status":"completed","output":[{"type":"web_search_call","id":"ws_srvtoolu_extra","status":"completed","action":{"type":"search","query":"q","queries":["other"]},"results":[]}]}`,
		`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"cited","annotations":[{"type":"url_citation","url":"https://example.test","encrypted_index":"opaque"}]}]}]}`,
		`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"cited","annotations":[{"type":"web_search_result_location","url":"https://example.test","encrypted_index":"opaque","extra":"loss"}]}]}]}`,
	}
	for _, body := range bodies {
		if out, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "claude-haiku-5.5", nil, nil, []byte(body)); err == nil || len(out) != 0 {
			t.Fatalf("unsupported Responses hosted-search/citation state accepted: %s, error=%v", out, err)
		}
		frame := claudeHostedFrame(t, "response.completed", map[string]any{"type": "response.completed", "response": json.RawMessage(body)})
		if out, err := StreamFromEndpoint(context.Background(), EndpointResponses, "claude", "claude-haiku-5.5", nil, nil, frame, new(any)); err == nil || len(out) != 0 {
			t.Fatalf("unsupported Responses SSE hosted-search/citation state accepted: %s, error=%v", out, err)
		}
	}
}

func TestClaudeHostedSearchSSERejectsIncompleteAndAmbiguousState(t *testing.T) {
	start := func(t *testing.T, id, name string, input map[string]any) []byte {
		return claudeHostedFrame(t, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "server_tool_use", "id": id, "name": name, "input": input}})
	}
	result := func(t *testing.T, id string) []byte {
		return claudeHostedFrame(t, "content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "web_search_tool_result", "tool_use_id": id, "content": []any{}}})
	}
	for _, test := range []struct {
		name   string
		frames [][]byte
	}{
		{name: "incomplete query JSON", frames: [][]byte{start(t, "srvtoolu_incomplete", "web_search", map[string]any{}), claudeHostedFrame(t, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"query":"unfinished"`}}), claudeHostedFrame(t, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})}},
		{name: "unmatched result", frames: [][]byte{result(t, "srvtoolu_missing")}},
		{name: "native bash", frames: [][]byte{start(t, "srvtoolu_bash", "bash_code_execution", map[string]any{"command": "pwd"})}},
		{name: "extra call field", frames: [][]byte{claudeHostedFrame(t, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "server_tool_use", "id": "srvtoolu_extra", "name": "web_search", "input": map[string]any{}, "provider_state": "unmapped"}})}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state any
			for _, frame := range test.frames {
				_, err := StreamFromEndpoint(context.Background(), EndpointMessages, "openai-response", "gpt-6-luna", nil, nil, frame, &state)
				if err != nil {
					return
				}
			}
			t.Fatal("incomplete or ambiguous Claude search state was accepted")
		})
	}
}

func assistantBlocks(t *testing.T, body []byte) []gjson.Result {
	t.Helper()
	var blocks []gjson.Result
	for _, message := range gjson.GetBytes(body, "messages").Array() {
		if message.Get("role").String() == "assistant" {
			blocks = append(blocks, message.Get("content").Array()...)
		}
	}
	return blocks
}

func claudeHostedFrame(t *testing.T, event string, payload map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return []byte("event: " + event + "\ndata: " + string(data) + "\n\n")
}
