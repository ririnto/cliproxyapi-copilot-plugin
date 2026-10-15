package translate

import (
	"context"
	"errors"
	"testing"

	"github.com/tidwall/gjson"
)

func TestDirectCallerRemainsClientFunction(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"inspect","input":{"value":17},"caller":{"type":"direct"}}]}]}`)
	out, err := ClaudeToResponses("test-model", body, false)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(out, "input.0.type").String() != "function_call" || gjson.GetBytes(out, "input.0.call_id").String() != "call_a" || gjson.GetBytes(out, "input.0.arguments").String() != `{"value":17}` {
		t.Fatalf("function fields = %s", out)
	}
}

func TestDirectHelpersCannotConsumeReplayCarriers(t *testing.T) {
	for _, test := range []struct{ source, body string }{
		{"claude", `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"synthetic","signature":"cpa-copilot-chat:v1:synthetic"}]}]}`},
		{"openai-response", `{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"synthetic"}],"encrypted_content":"cpa-copilot-chat:v1:synthetic"}]}`},
	} {
		out, err := RequestForEndpointFrom(test.source, "test-model", []byte(test.body), false, EndpointChatCompletions)
		if out != nil || !errors.Is(err, ErrProviderStateConversion) {
			t.Fatalf("payload present = %v, error type = %T", out != nil, err)
		}
	}
}

func TestChatAnnotationsCannotDisappear(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"synthetic","annotations":[{"type":"url_citation","url_citation":{"url":"https://source.example.invalid"}}]},"finish_reason":"stop"}]}`)
	for _, destination := range []string{"claude", "openai-response"} {
		out, err := ResponseFromEndpoint(context.Background(), EndpointChatCompletions, destination, "test-model", nil, nil, body)
		if out != nil || !errors.Is(err, ErrProviderStateConversion) {
			t.Fatalf("payload present = %v, error type = %T", out != nil, err)
		}
		frame := []byte("data: " + `{"choices":[{"index":0,"delta":{"role":"assistant","content":"synthetic","annotations":[{"type":"url_citation"}]}}]}` + "\n\n")
		var ordinary any
		frames, err := StreamFromEndpoint(context.Background(), EndpointChatCompletions, destination, "test-model", nil, nil, frame, &ordinary)
		if len(frames) != 0 || !errors.Is(err, ErrProviderStateConversion) {
			t.Fatalf("frames = %d, error type = %T", len(frames), err)
		}
		var replayState CopilotChatStream
		replay := NewCopilotReplay("account", "test-model", EndpointChatCompletions, "https://api.example.invalid", "synthetic-credential")
		frames, err = replay.Stream(context.Background(), destination, "test-model", nil, []byte(`{"messages":[]}`), frame, &replayState)
		if len(frames) != 0 || !errors.Is(err, ErrProviderStateConversion) {
			t.Fatalf("replay frames = %d, error type = %T", len(frames), err)
		}
		frames, err = replay.FinishStream(context.Background(), destination, "test-model", nil, nil, &replayState)
		if len(frames) != 0 || !errors.Is(err, ErrProviderStateConversion) {
			t.Fatalf("terminal frames = %d, error type = %T", len(frames), err)
		}
	}
}

func TestStreamContainerCannotDisappear(t *testing.T) {
	var state any
	frame := []byte("data: " + `{"type":"message_delta","delta":{"container":{"id":"synthetic-container"}}}` + "\n\n")
	frames, err := StreamFromEndpoint(context.Background(), EndpointMessages, "openai-response", "test-model", nil, nil, frame, &state)
	var detail *ProviderStateConversionError
	if len(frames) != 0 || !errors.As(err, &detail) || detail.Kind != "response" || detail.Field != "container" {
		t.Fatalf("frames = %d, error = %#v", len(frames), err)
	}
}
