package translate

import (
	"context"
	"errors"
	"testing"

	"github.com/tidwall/gjson"
)

func TestHostedCallCannotBecomeClientFunction(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"call_a","name":"web_search","input":{"query":"synthetic"}}]}]}`)
	out, err := ClaudeToResponses("test-model", body, false)
	if err == nil || out != nil {
		t.Fatalf("payload present = %v, error present = %v", out != nil, err != nil)
	}
}

func TestResponsesContinuationCannotDisappear(t *testing.T) {
	out, err := RequestForEndpointFrom("openai-response", "test-model", []byte(`{"previous_response_id":"synthetic-response","input":"continue"}`), false, EndpointMessages)
	if err == nil || out != nil {
		t.Fatalf("payload present = %v, error present = %v", out != nil, err != nil)
	}
}

func TestHostedResponseCannotDisappear(t *testing.T) {
	out, err := ResponseFromEndpoint(context.Background(), EndpointResponses, "claude", "test-model", nil, nil, []byte(`{"output":[{"type":"web_search_call","id":"synthetic-call","status":"completed"}]}`))
	if err == nil || out != nil {
		t.Fatalf("payload present = %v, error present = %v", out != nil, err != nil)
	}
}

func TestUnrepresentedRequestStateReturnsTypedErrors(t *testing.T) {
	for _, test := range []struct{ source, body, kind, field string }{
		{"claude", `{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`, "web_search_20250305", "tools.type"},
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"call_a","name":"web_search","input":{}}]}]}`, "server_tool_use", "type"},
		{"claude", `{"messages":[{"role":"user","content":[{"type":"web_search_tool_result","tool_use_id":"call_a","content":[]}]}]}`, "web_search_tool_result", "type"},
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"synthetic-signature"}]}]}`, "thinking", "signature"},
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"synthetic-redacted"}]}]}`, "redacted_thinking", "type"},
		{"claude", `{"container":"synthetic-container","messages":[]}`, "request", "container"},
		{"claude", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","is_error":true,"content":"synthetic"}]}]}`, "tool_result", "is_error"},
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"inspect","input":{},"caller":{"type":"code_execution_20250825"}}]}]}`, "tool_use", "caller"},
		{"openai-response", `{"tools":[{"type":"web_search"}],"input":"inspect"}`, "web_search", "tools.type"},
		{"openai-response", `{"tools":[{"type":"custom","name":"inspect"}],"input":"inspect"}`, "custom", "tools.type"},
		{"openai-response", `{"input":[{"type":"web_search_call","id":"synthetic-call"}]}`, "web_search_call", "type"},
		{"openai-response", `{"input":[{"type":"item_reference","id":"synthetic-item"}]}`, "item_reference", "type"},
		{"openai-response", `{"input":[{"type":"custom_tool_call_output","call_id":"call_a","output":"synthetic"}]}`, "custom_tool_call_output", "type"},
		{"openai-response", `{"previous_response_id":"synthetic-response","input":"continue"}`, "request", "previous_response_id"},
		{"openai-response", `{"conversation":{"id":"synthetic-conversation"},"input":"continue"}`, "request", "conversation"},
		{"openai-response", `{"input":[{"type":"reasoning","summary":[],"encrypted_content":"synthetic-encrypted"}]}`, "reasoning", "encrypted_content"},
	} {
		for _, target := range []struct{ endpoint, format string }{{EndpointChatCompletions, "openai"}, {EndpointResponses, "openai-response"}, {EndpointMessages, "claude"}} {
			if target.format == test.source {
				continue
			}
			t.Run(test.source+"/"+test.kind+"/"+test.field+"/"+target.format, func(t *testing.T) {
				out, err := RequestForEndpointFrom(test.source, "test-model", []byte(test.body), false, target.endpoint)
				var detail *ProviderStateConversionError
				if out != nil || !errors.Is(err, ErrProviderStateConversion) || !errors.As(err, &detail) {
					t.Fatalf("payload present = %v, error type = %T", out != nil, err)
				}
				if detail.Source != test.source || detail.Destination != target.format || detail.Kind != test.kind || detail.Field != test.field {
					t.Fatalf("state error = %#v", detail)
				}
			})
		}
	}
}

func TestNativeStatePreservesOwnership(t *testing.T) {
	for _, test := range []struct{ source, endpoint, body, path, want string }{
		{"claude", EndpointMessages, `{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"synthetic-signature"}]}]}`, "messages.0.content.0.signature", "synthetic-signature"},
		{"openai-response", EndpointResponses, `{"previous_response_id":"synthetic-response","tools":[{"type":"web_search"}],"input":[{"type":"reasoning","encrypted_content":"synthetic-encrypted"}]}`, "input.0.encrypted_content", "synthetic-encrypted"},
	} {
		out, err := RequestForEndpointFrom(test.source, "test-model", []byte(test.body), false, test.endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if gjson.GetBytes(out, test.path).String() != test.want || gjson.GetBytes(out, "tools.0.type").String() != gjson.Get(test.body, "tools.0.type").String() {
			t.Fatalf("native fields = %s", out)
		}
		body := []byte(`{"output":[{"type":"web_search_call","id":"synthetic-call"}]}`)
		response, err := ResponseFromEndpoint(context.Background(), test.endpoint, test.source, "test-model", nil, nil, body)
		if err != nil || string(response) != string(body) {
			t.Fatalf("native body = %s, error type = %T", response, err)
		}
		var state any
		frame := []byte("data: {\"type\":\"native.synthetic\"}\n\n")
		frames, err := StreamFromEndpoint(context.Background(), test.endpoint, test.source, "test-model", nil, nil, frame, &state)
		if err != nil || len(frames) != 1 || string(frames[0]) != string(frame) {
			t.Fatalf("frames = %d, error type = %T", len(frames), err)
		}
	}
}

func TestHostedAndSignedResponsesReturnTypedErrors(t *testing.T) {
	for _, test := range []struct{ endpoint, destination, body, kind, field string }{
		{EndpointResponses, "claude", `{"output":[{"type":"web_search_call","id":"synthetic-call"}]}`, "web_search_call", "type"},
		{EndpointMessages, "openai-response", `{"content":[{"type":"server_tool_use","id":"call_a","name":"web_search","input":{}}]}`, "server_tool_use", "type"},
		{EndpointResponses, "claude", `{"output":[{"type":"reasoning","encrypted_content":"synthetic-encrypted"}]}`, "reasoning", "encrypted_content"},
		{EndpointMessages, "openai-response", `{"content":[{"type":"thinking","thinking":"synthetic","signature":"synthetic-signature"}]}`, "thinking", "signature"},
		{EndpointResponses, "claude", `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"synthetic","annotations":[{"type":"url_citation","url":"https://source.example.invalid"}]}]}]}`, "output_text", "annotations"},
	} {
		out, err := ResponseFromEndpoint(context.Background(), test.endpoint, test.destination, "test-model", nil, nil, []byte(test.body))
		var detail *ProviderStateConversionError
		if out != nil || !errors.Is(err, ErrProviderStateConversion) || !errors.As(err, &detail) || detail.Kind != test.kind || detail.Field != test.field {
			t.Fatalf("response present = %v, state error = %#v", out != nil, err)
		}
	}
}

func TestHostedAndSignedStreamEventsCannotCompleteSuccessfully(t *testing.T) {
	for _, test := range []struct{ endpoint, destination, event string }{
		{EndpointResponses, "claude", `{"type":"response.output_item.added","item":{"type":"web_search_call","id":"synthetic-call"}}`},
		{EndpointResponses, "claude", `{"type":"response.completed","response":{"output":[{"type":"web_search_call","id":"synthetic-call"}]}}`},
		{EndpointResponses, "claude", `{"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"synthetic-encrypted"}}`},
		{EndpointMessages, "openai-response", `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"call_a","name":"web_search","input":{}}}`},
		{EndpointResponses, "claude", `{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://source.example.invalid"}}`},
		{EndpointMessages, "openai-response", `{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location"}}}`},
		{EndpointMessages, "openai-response", `{"type":"message_delta","delta":{"stop_reason":"pause_turn"}}`},
		{EndpointMessages, "openai-response", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"synthetic-signature"}}`},
	} {
		var state any
		frames, err := StreamFromEndpoint(context.Background(), test.endpoint, test.destination, "test-model", nil, nil, []byte("data: "+test.event+"\n\n"), &state)
		if len(frames) != 0 || !errors.Is(err, ErrProviderStateConversion) {
			t.Fatalf("frames = %d, error type = %T", len(frames), err)
		}
	}
}
